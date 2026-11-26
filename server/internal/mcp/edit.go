package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/debkosh/memd/server/internal/registry"
)

// fileEdit is one small change applied by memory_edit. Op picks which of the
// other fields are read:
//
//	replace        old → new; old must occur exactly once
//	insert_after   text as new line(s) after the line where anchor ends
//	insert_before  text as new line(s) before the line where anchor starts
//	append         text as new line(s) at the end of the file
//	delete_lines   remove every line containing match; at least one must
type fileEdit struct {
	Op     string `json:"op"`
	Old    string `json:"old"`
	New    string `json:"new"`
	Anchor string `json:"anchor"`
	Text   string `json:"text"`
	Match  string `json:"match"`
}

// applyEdits applies edits in order to content. It is all or nothing: the
// first edit that cannot apply returns an error naming it, and the caller
// writes nothing.
func applyEdits(content string, edits []fileEdit) (string, error) {
	if len(edits) == 0 {
		return "", errors.New("edits is empty")
	}
	for i, e := range edits {
		var err error
		content, err = applyEdit(content, e)
		if err != nil {
			return "", fmt.Errorf("edit %d (%s): %v; nothing was written", i+1, e.Op, err)
		}
	}
	return content, nil
}

func applyEdit(content string, e fileEdit) (string, error) {
	switch e.Op {
	case "replace":
		at, err := uniqueIndex(content, e.Old, "old")
		if err != nil {
			return "", err
		}
		return content[:at] + e.New + content[at+len(e.Old):], nil
	case "insert_after":
		if e.Text == "" {
			return "", errors.New("text is empty")
		}
		at, err := uniqueIndex(content, e.Anchor, "anchor")
		if err != nil {
			return "", err
		}
		end := at + len(e.Anchor)
		if strings.HasSuffix(e.Anchor, "\n") {
			end-- // the anchor already ends its line
		}
		nl := strings.IndexByte(content[end:], '\n')
		if nl < 0 { // anchor is on the last line, which has no newline
			return content + "\n" + asLines(e.Text), nil
		}
		cut := end + nl + 1
		return content[:cut] + asLines(e.Text) + content[cut:], nil
	case "insert_before":
		if e.Text == "" {
			return "", errors.New("text is empty")
		}
		at, err := uniqueIndex(content, e.Anchor, "anchor")
		if err != nil {
			return "", err
		}
		start := strings.LastIndexByte(content[:at], '\n') + 1
		return content[:start] + asLines(e.Text) + content[start:], nil
	case "append":
		if e.Text == "" {
			return "", errors.New("text is empty")
		}
		if content != "" && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		return content + asLines(e.Text), nil
	case "delete_lines":
		if e.Match == "" {
			return "", errors.New("match is empty")
		}
		if strings.Contains(e.Match, "\n") {
			return "", errors.New("match must be on one line")
		}
		lines := strings.SplitAfter(content, "\n")
		kept := lines[:0]
		removed := 0
		for _, l := range lines {
			if strings.Contains(l, e.Match) {
				removed++
				continue
			}
			kept = append(kept, l)
		}
		if removed == 0 {
			return "", fmt.Errorf("no line contains %q", e.Match)
		}
		return strings.Join(kept, ""), nil
	case "":
		return "", errors.New("op is missing")
	default:
		return "", fmt.Errorf("unknown op %q (want replace, insert_after, insert_before, append or delete_lines)", e.Op)
	}
}

// uniqueIndex returns where needle occurs in content, failing unless it
// occurs exactly once.
func uniqueIndex(content, needle, field string) (int, error) {
	if needle == "" {
		return 0, fmt.Errorf("%s is empty", field)
	}
	at := strings.Index(content, needle)
	if at < 0 {
		return 0, fmt.Errorf("%s not found", field)
	}
	if n := strings.Count(content, needle); n > 1 {
		return 0, fmt.Errorf("%s occurs %d times; include more surrounding text so it is unique", field, n)
	}
	return at, nil
}

// asLines makes text a run of whole lines: it ends with exactly the newlines
// it was given, plus one if it had none.
func asLines(text string) string {
	if strings.HasSuffix(text, "\n") {
		return text
	}
	return text + "\n"
}

func (s *Server) toolEdit(conn *registry.Connector, args json.RawMessage) (string, bool) {
	if !conn.Write {
		return "connector is read-only", true
	}
	var a struct {
		DirectoryID string     `json:"directory_id"`
		Path        string     `json:"path"`
		Edits       []fileEdit `json:"edits"`
		Message     string     `json:"message"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "invalid arguments: " + err.Error(), true
	}
	d := s.reg.DirectoryForConnector(conn, a.DirectoryID)
	if d == nil {
		return "directory not accessible: " + a.DirectoryID, true
	}
	if !d.CanWrite {
		return "directory is read-only for you: " + a.DirectoryID, true
	}
	old, err := d.Backend.ReadRaw(a.Path)
	if err != nil {
		return err.Error(), true
	}
	updated, err := applyEdits(string(old), a.Edits)
	if err != nil {
		return err.Error(), true
	}
	if err := d.Backend.Write(a.Path, []byte(updated), a.Message); err != nil {
		return err.Error(), true
	}
	msg := fmt.Sprintf("applied %d edit(s) to %s (%d → %d bytes)", len(a.Edits), a.Path, len(old), len(updated))
	if a.Path == "MEMORY.md" && overPreloadBudget([]byte(updated)) {
		msg += " — warning: MEMORY.md exceeds the preload budget (200 lines / 25KB); only the first part will be preloaded by memory_load. Move detail into topic files."
	}
	return msg, false
}
