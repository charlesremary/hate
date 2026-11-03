// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ticket

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Project format version.
//
// A project's format is the schema_version in .tkt/config.json. Its major
// number is the hate major version that last changed the format: hate 2.x
// writes format 2, and so on, so there is one number to track (a test pins
// FormatMajor to AppVersion's major). hate refuses to change a project whose
// format major is newer than its own: Go drops JSON fields it doesn't know,
// so an older hate editing a newer project would lose data without an error.
// Such a project stays readable.
//
// Within a major, new top-level ticket and config fields are kept on write by
// an older hate (see extraFields), so adding a field is not a format change.
// Renaming, removing or re-meaning a field, or adding one inside a nested
// object (time entries, test cases, ...), is: bump the major and add a
// migration.

// FormatMajor is the project format this hate writes. A project at an older
// format is readable (translated in memory) but must be migrated before it
// can be changed; see migrate.go.
const FormatMajor = 2

// NewerFormatError: the project was changed by a newer hate.
type NewerFormatError struct {
	Root    string
	Version string // the project's schema_version
}

func (e *NewerFormatError) Error() string {
	return fmt.Sprintf("This project was updated by hate %s or newer. This hate can show it but not change it. Update hate (brew upgrade --cask hate), then try again.",
		formatLabel(e.Version))
}

// formatLabel turns a schema_version ("2.0.0") into the hate release that
// writes it ("2.0").
func formatLabel(v string) string {
	if m, err := formatMajorOf(v); err == nil {
		return fmt.Sprintf("%d.0", m)
	}
	return v
}

// NeedsMigrationError: the project is at an older format; it must get its
// one-time update (Migrate) before this hate changes it.
type NeedsMigrationError struct {
	Root    string
	Version string
}

func (e *NeedsMigrationError) Error() string {
	return fmt.Sprintf("This project needs a one-time update to the hate %d.0 format before it can be changed. Open it in hate and click Update project (or call hate_migrate_project); you'll see exactly what changes first.", FormatMajor)
}

// IsNeedsMigration reports whether err is (or wraps) a NeedsMigrationError.
func IsNeedsMigration(err error) bool {
	var e *NeedsMigrationError
	return errors.As(err, &e)
}

// IsNewerFormat reports whether err is (or wraps) a NewerFormatError.
func IsNewerFormat(err error) bool {
	var e *NewerFormatError
	return errors.As(err, &e)
}

// formatMajorOf parses the major number of a schema_version; "" is format 1.
func formatMajorOf(v string) (int, error) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if v == "" {
		return 1, nil
	}
	major, _, _ := strings.Cut(v, ".")
	n, err := strconv.Atoi(major)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("bad schema_version %q", v)
	}
	return n, nil
}

// ProjectFormat returns the project's schema_version as written ("" when the
// config is missing or has none).
func ProjectFormat(repoRoot string) string {
	data, err := os.ReadFile(ConfigPath(repoRoot))
	if err != nil {
		return ""
	}
	return FormatOfConfig(data)
}

// FormatOfConfig reads schema_version out of config.json bytes.
func FormatOfConfig(data []byte) string {
	var v struct {
		SchemaVersion string `json:"schema_version"`
	}
	_ = json.Unmarshal(data, &v)
	return v.SchemaVersion
}

// FormatMajorOf is the format major of a schema_version ("" is format 1); an
// unparseable version is reported as newer than this hate.
func FormatMajorOf(version string) int {
	m, err := formatMajorOf(version)
	if err != nil {
		return FormatMajor + 1
	}
	return m
}

// FormatIsNewer reports whether a schema_version is newer than FormatMajor.
// An unparseable version counts as newer: better read-only than lossy.
func FormatIsNewer(version string) bool {
	m, err := formatMajorOf(version)
	return err != nil || m > FormatMajor
}

// CheckWritable returns a *NewerFormatError when a newer hate changed the
// project at repoRoot, a *NeedsMigrationError when it is at an older format,
// else nil (also for a folder with no config yet: a project being created).
func CheckWritable(repoRoot string) error {
	data, err := os.ReadFile(ConfigPath(repoRoot))
	if err != nil {
		return nil
	}
	v := FormatOfConfig(data)
	switch m := FormatMajorOf(v); {
	case m > FormatMajor:
		return &NewerFormatError{Root: repoRoot, Version: v}
	case m < FormatMajor:
		return &NeedsMigrationError{Root: repoRoot, Version: v}
	}
	return nil
}

// ---- keeping fields this hate doesn't know ----

// extraFields holds the top-level JSON members of a ticket or config that
// this hate has no field for, so writing the file back keeps them.
type extraFields map[string]json.RawMessage

// splitExtras decodes data into v (a pointer to a plain struct type, without
// custom JSON methods) and returns the members v has no field for.
func splitExtras(data []byte, v any) (extraFields, error) {
	if err := json.Unmarshal(data, v); err != nil {
		return nil, err
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, nil // not an object: nothing to keep
	}
	known := knownKeys(reflect.TypeOf(v))
	var extra extraFields
	for k, raw := range all {
		if known[k] {
			continue
		}
		if extra == nil {
			extra = extraFields{}
		}
		extra[k] = raw
	}
	return extra, nil
}

var knownKeysCache sync.Map // reflect.Type -> map[string]bool

// knownKeys is the set of JSON member names of a struct type (or pointer to
// one): every exported field's json name (json:"-" fields are not members).
func knownKeys(t reflect.Type) map[string]bool {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if m, ok := knownKeysCache.Load(t); ok {
		return m.(map[string]bool)
	}
	m := map[string]bool{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if name == "" {
			name = f.Name
		}
		m[name] = true
	}
	knownKeysCache.Store(t, m)
	return m
}

// joinExtras marshals v and appends the extra members after the known ones,
// in key order. With no extras the output is exactly json.Marshal(v).
func joinExtras(v any, extra extraFields) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil || len(extra) == 0 {
		return data, err
	}
	known := knownKeys(reflect.TypeOf(v))
	keys := make([]string, 0, len(extra))
	for k := range extra {
		if !known[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	buf := bytes.TrimSpace(data)
	buf = append([]byte{}, buf[:len(buf)-1]...) // drop the closing brace
	for _, k := range keys {
		if len(buf) > 1 {
			buf = append(buf, ',')
		}
		kb, _ := json.Marshal(k)
		buf = append(buf, kb...)
		buf = append(buf, ':')
		buf = append(buf, extra[k]...)
	}
	return append(buf, '}'), nil
}

// DecodeTicket parses a ticket file, keeping members this hate doesn't know.
func DecodeTicket(data []byte) (*Ticket, error) {
	type plain Ticket
	var p plain
	extra, err := splitExtras(data, &p)
	if err != nil {
		return nil, err
	}
	t := Ticket(p)
	t.extra = extra
	return &t, nil
}

// EncodeTicket is a ticket file's bytes: two-space indent, trailing newline,
// unknown members kept (after the known ones).
func EncodeTicket(t *Ticket) ([]byte, error) {
	type plain Ticket
	return encodeIndented(plain(*t), t.extra)
}

// DecodeConfig parses .tkt/config.json, keeping members this hate doesn't know.
func DecodeConfig(data []byte) (*ProjectConfig, error) {
	type plain ProjectConfig
	var p plain
	extra, err := splitExtras(data, &p)
	if err != nil {
		return nil, err
	}
	c := ProjectConfig(p)
	c.extra = extra
	return &c, nil
}

// EncodeConfig is config.json's bytes, like EncodeTicket.
func EncodeConfig(c *ProjectConfig) ([]byte, error) {
	type plain ProjectConfig
	return encodeIndented(plain(*c), c.extra)
}

func encodeIndented(v any, extra extraFields) ([]byte, error) {
	data, err := joinExtras(v, extra)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, data, "", "  "); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}
