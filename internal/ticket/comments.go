// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ticket

import (
	"fmt"
	"strconv"
	"strings"
)

// Activity actions for comments.
const (
	ActionComment       = "comment"
	ActionCommentEdited = "comment_edited"
)

// ErrNotAComment: only comment entries can be edited.
var ErrNotAComment = fmt.Errorf("only comments can be edited")

// commentNum parses "c12" -> 12 (0 if not a comment id).
func commentNum(id string) int {
	if !strings.HasPrefix(id, "c") {
		return 0
	}
	n, err := strconv.Atoi(id[1:])
	if err != nil {
		return 0
	}
	return n
}

// nextCommentID is one past the highest comment id on the ticket, so ids are
// never reused.
func nextCommentID(t *Ticket) string {
	max := 0
	for _, a := range t.Activity {
		if n := commentNum(a.ID); n > max {
			max = n
		}
	}
	return "c" + strconv.Itoa(max+1)
}

// normalizeCommentIDs runs on every read, so the ids are in place before any
// write persists them:
//
//   - Comments from before comment ids existed get one, in log order.
//   - Two entries with the same id and the same timestamp and author are one
//     comment that a sync merge kept twice (edited on one side): the copy with
//     the later edited_at wins and the other is dropped.
//   - Two different comments with the same id (added on two machines while
//     offline) keep the first; the later one gets a fresh id.
func normalizeCommentIDs(t *Ticket) {
	byID := map[string]int{} // id -> index in out
	out := make([]Activity, 0, len(t.Activity))
	var needID []int
	for _, a := range t.Activity {
		if a.Action != ActionComment {
			out = append(out, a)
			continue
		}
		if a.ID == "" {
			needID = append(needID, len(out))
			out = append(out, a)
			continue
		}
		if i, dup := byID[a.ID]; dup {
			prev := out[i]
			if prev.Timestamp == a.Timestamp && prev.Author == a.Author {
				if a.EditedAt > prev.EditedAt {
					out[i] = a
				}
				continue
			}
			a.ID = ""
			needID = append(needID, len(out))
			out = append(out, a)
			continue
		}
		byID[a.ID] = len(out)
		out = append(out, a)
	}
	t.Activity = out
	for _, i := range needID {
		t.Activity[i].ID = nextCommentID(t)
	}
}

// EditComment replaces a comment's text, stamps edited_at, and appends a
// comment_edited entry recording the comment id and the previous text, so the
// history is kept. The caller holds the project lock.
func EditComment(repoRoot, ticketID, commentID, message, author string) (*Ticket, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return nil, fmt.Errorf("message is required")
	}
	t, err := ReadTicket(repoRoot, ticketID)
	if err != nil {
		return nil, err
	}
	idx := -1
	for i, a := range t.Activity {
		if a.ID == commentID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, fmt.Errorf("comment not found: %s", commentID)
	}
	if t.Activity[idx].Action != ActionComment {
		return nil, ErrNotAComment
	}
	old := t.Activity[idx].Detail
	t.Activity[idx].Detail = message
	t.Activity[idx].EditedAt = NowISO()
	addActivity(t, author, ActionCommentEdited, fmt.Sprintf("%s, was: %s", commentID, old))
	if err := WriteTicket(repoRoot, t); err != nil {
		return nil, err
	}
	return t, nil
}
