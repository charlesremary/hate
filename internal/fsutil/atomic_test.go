// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

// HATE-2yqw: writes go through a temp file + rename; no temp file is left
// behind, the parent dir is created and the permissions are applied.
func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "a.json")
	for _, content := range []string{"one\n", "two, longer\n"} {
		if err := WriteFileAtomic(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(p)
		if err != nil || string(got) != content {
			t.Fatalf("content = %q (%v), want %q", got, err, content)
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 1 {
		t.Errorf("leftover files: %v", entries)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0644 {
		t.Errorf("mode = %v", fi.Mode().Perm())
	}
	// A write into an unwritable place fails without touching the target.
	if err := WriteFileAtomic(filepath.Join(p, "x"), []byte("x"), 0644); err == nil {
		t.Error("writing under a file should fail")
	}
}
