// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ticket

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"mime"
	"os"
	"path/filepath"
	"strings"
)

// MaxAttachmentSize is the per-file upload cap in bytes. Soft guardrail to
// keep the project repo small — binary blobs live in git history forever.
const MaxAttachmentSize int64 = 25 * 1024 * 1024 // 25 MB

// AttachmentsDir returns the directory holding attachments for a given ticket.
// Files live under <repo>/attachments/<ticketID>/.
func AttachmentsDir(repoRoot, ticketID string) string {
	return filepath.Join(repoRoot, "attachments", ticketID)
}

// AttachmentPath returns the on-disk path for a specific attachment. The
// filename is prefixed with the attachment ID so two uploads with the same
// original name don't collide.
func AttachmentPath(repoRoot, ticketID, attachmentID, filename string) string {
	return filepath.Join(AttachmentsDir(repoRoot, ticketID), attachmentID+"-"+filename)
}

// SanitizeAttachmentFilename strips any path components and replaces characters
// that would be problematic on disk. The result is always a non-empty basename
// safe to join into a server-controlled directory.
func SanitizeAttachmentFilename(name string) string {
	name = filepath.Base(name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return "file"
	}
	// Replace path separators (shouldn't reach here after Base, but defensive)
	// and a small set of characters known to misbehave across filesystems.
	repl := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "\x00", "")
	name = repl.Replace(name)
	if len(name) > 200 {
		ext := filepath.Ext(name)
		stem := strings.TrimSuffix(name, ext)
		if len(stem) > 200-len(ext) {
			stem = stem[:200-len(ext)]
		}
		name = stem + ext
	}
	return name
}

// GenerateAttachmentID returns a random base36 ID for a new attachment.
// 8 chars = 36^8 ≈ 2.8 trillion possibilities — no collision check needed at
// realistic per-ticket attachment counts.
func GenerateAttachmentID() (string, error) {
	const n = 8
	suffix := make([]byte, n)
	for i := range suffix {
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(base36Chars))))
		if err != nil {
			return "", err
		}
		suffix[i] = base36Chars[idx.Int64()]
	}
	return string(suffix), nil
}

// GuessContentType returns a MIME type from the filename extension, falling
// back to application/octet-stream.
func GuessContentType(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	if ext == "" {
		return "application/octet-stream"
	}
	if ct := mime.TypeByExtension(ext); ct != "" {
		return ct
	}
	switch ext {
	case ".md", ".markdown":
		return "text/markdown; charset=utf-8"
	}
	return "application/octet-stream"
}

// AppendAttachment records an already-written attachment on the ticket JSON.
// The file is expected to be on disk before calling. Caller can pass uploadedBy
// as an empty string for unauthenticated contexts.
func AppendAttachment(repoRoot, ticketID string, att Attachment, author string) (*Ticket, error) {
	t, err := ReadTicket(repoRoot, ticketID)
	if err != nil {
		return nil, err
	}
	t.Attachments = append(t.Attachments, att)
	addActivity(t, author, "attachment_added", fmt.Sprintf("%s (%d bytes)", att.Filename, att.Size))
	if err := WriteTicket(repoRoot, t); err != nil {
		return nil, err
	}
	return t, nil
}

// RemoveAttachment deletes the file from disk and removes its entry from the
// ticket. Returns ErrNotFound if no matching attachment ID is present.
func RemoveAttachment(repoRoot, ticketID, attachmentID, author string) (*Ticket, error) {
	t, err := ReadTicket(repoRoot, ticketID)
	if err != nil {
		return nil, err
	}
	idx := -1
	for i, a := range t.Attachments {
		if a.ID == attachmentID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, fmt.Errorf("Attachment not found: %s", attachmentID)
	}
	att := t.Attachments[idx]
	path := AttachmentPath(repoRoot, ticketID, att.ID, att.Filename)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to remove attachment file: %w", err)
	}
	t.Attachments = append(t.Attachments[:idx], t.Attachments[idx+1:]...)
	addActivity(t, author, "attachment_removed", att.Filename)
	if err := WriteTicket(repoRoot, t); err != nil {
		return nil, err
	}
	return t, nil
}

// FindAttachment returns the attachment record on a ticket by ID.
func FindAttachment(t *Ticket, attachmentID string) (Attachment, bool) {
	for _, a := range t.Attachments {
		if a.ID == attachmentID {
			return a, true
		}
	}
	return Attachment{}, false
}
