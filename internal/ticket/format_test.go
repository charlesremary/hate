// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ticket

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestFormatIsNewer(t *testing.T) {
	for v, want := range map[string]bool{"": false, "1.0.0": false, "1.9.3": false, "v1.2": false, "0.9.0": false, "2.0.0": false, "3.0.0": true, "10.1.0": true, "garbage": true} {
		if got := FormatIsNewer(v); got != want {
			t.Errorf("FormatIsNewer(%q) = %v, want %v", v, got, want)
		}
	}
}

// HATE-189v: a project a newer hate changed is never written.
func TestNewerProjectIsReadOnly(t *testing.T) {
	root := t.TempDir()
	if err := WriteConfig(root, DefaultConfig("c", "P", "p", "P")); err != nil {
		t.Fatal(err)
	}
	tk := BlankTicket("P-0001", "task", "One", "a@x")
	if err := WriteTicket(root, tk); err != nil {
		t.Fatal(err)
	}
	cfgPath := ConfigPath(root)
	data, _ := os.ReadFile(cfgPath)
	newer := strings.Replace(string(data), `"schema_version": "2.0.0"`, `"schema_version": "3.0.0"`, 1)
	os.WriteFile(cfgPath, []byte(newer), 0644)
	before, _ := os.ReadFile(TicketPath(root, "P-0001"))

	got, err := ReadTicket(root, "P-0001")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got.Title = "changed"
	if err := WriteTicket(root, got); !IsNewerFormat(err) {
		t.Fatalf("WriteTicket err = %v, want NewerFormatError", err)
	}
	cfg, _ := ReadConfig(root)
	err = WriteConfig(root, cfg)
	if !IsNewerFormat(err) {
		t.Fatalf("WriteConfig err = %v", err)
	}
	if after, _ := os.ReadFile(TicketPath(root, "P-0001")); string(after) != string(before) {
		t.Error("ticket file changed")
	}
	if !strings.Contains(err.Error(), "hate 3.0 or newer") || !strings.Contains(err.Error(), "brew upgrade --cask hate") {
		t.Errorf("message: %s", err)
	}
}

// HATE-189v: members this hate doesn't know survive a read-edit-write, and a
// file without them is written byte-for-byte as before.
func TestUnknownFieldsKept(t *testing.T) {
	root := t.TempDir()
	if err := WriteConfig(root, DefaultConfig("c", "P", "p", "P")); err != nil {
		t.Fatal(err)
	}
	tk := BlankTicket("P-0001", "task", "One", "a@x")
	if err := WriteTicket(root, tk); err != nil {
		t.Fatal(err)
	}
	path := TicketPath(root, "P-0001")
	plain, _ := os.ReadFile(path)
	want, _ := json.MarshalIndent(tk, "", "  ")
	if string(plain) != string(want)+"\n" {
		t.Fatalf("format changed:\n%s\nwant\n%s", plain, want)
	}

	var m map[string]any
	json.Unmarshal(plain, &m)
	m["lifecycle"] = "Build"
	m["block"] = 3
	withExtra, _ := json.Marshal(m)
	os.WriteFile(path, withExtra, 0644)

	got, err := ReadTicket(root, "P-0001")
	if err != nil {
		t.Fatal(err)
	}
	got.Title = "edited"
	if err := WriteTicket(root, got); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(path)
	var back map[string]any
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("invalid JSON written: %v\n%s", err, out)
	}
	if back["lifecycle"] != "Build" || back["block"] != float64(3) || back["title"] != "edited" {
		t.Errorf("after write: lifecycle=%v block=%v title=%v", back["lifecycle"], back["block"], back["title"])
	}

	// Same for the config.
	cp := ConfigPath(root)
	cdata, _ := os.ReadFile(cp)
	var cm map[string]any
	json.Unmarshal(cdata, &cm)
	cm["block_label"] = "Sprint"
	cdata, _ = json.Marshal(cm)
	os.WriteFile(cp, cdata, 0644)
	cfg, err := ReadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ProjectName = "Renamed"
	if err := WriteConfig(root, cfg); err != nil {
		t.Fatal(err)
	}
	cdata, _ = os.ReadFile(cp)
	if !strings.Contains(string(cdata), `"block_label": "Sprint"`) || !strings.Contains(string(cdata), `"project_name": "Renamed"`) {
		t.Errorf("config after write:\n%s", cdata)
	}
}
