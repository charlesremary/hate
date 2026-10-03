// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package fsutil holds small filesystem helpers shared by every package that
// writes project files.
package fsutil

import (
	"os"
	"path/filepath"
)

// WriteFileAtomic writes data to path so that readers (and a crash) only ever
// see the old content or the new content, never a partial file: the data goes
// to a temp file in the same directory, is synced, then renamed over path.
// The parent directory is created when missing.
//
// The temp file is named ".<base>.tmp-<random>", so it never matches a
// "*.json" listing (tickets/, snapshots/) while it exists.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, perm); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	ok = true
	return nil
}
