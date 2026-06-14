// Package tasks is the hardcoded grammar for the built-in "tasks" feature.
// It parses the markdown checklist files an agent keeps under a directory's
// `tasks/` folder into a structured model the dashboard can render, derives a
// live board (overview) from those files, and performs the surgical line edits
// the UI uses to check a box or add a task without re-serialising the file.
//
// The files are the single source of truth (see the feature design doc): parse
// is for display, edits are line-targeted, so notes, formatting and ordering an
// agent or human put in the file survive a dashboard round-trip untouched.
package tasks

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Task is one checklist item parsed from a list file. Line is the 1-based line
// number within the file as stored (front matter included), so the UI can ask
// the server to toggle exactly that line. Raw is the line text without its
// trailing newline; the UI echoes it back as an optimistic-concurrency guard so
// a stale board never toggles the wrong line.
type Task struct {
	File     string   `json:"file"`
	Line     int      `json:"line"`
	Raw      string   `json:"raw"`
	Done     bool     `json:"done"`
	Title    string   `json:"title"`
	Due      string   `json:"due,omitempty"`
	Prio     string   `json:"prio,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Link     string   `json:"link,omitempty"`
	Subtasks []Task   `json:"subtasks,omitempty"`
	Notes    []string `json:"notes,omitempty"`
}

// List is one parsed list file (e.g. tasks/inbox.md) with its top-level tasks.
type List struct {
	File  string `json:"file"`
	Name  string `json:"name"`
	Tasks []Task `json:"tasks"`
	Open  int    `json:"open"`
	Total int    `json:"total"`
}

// Board is the derived front-page overview: open work grouped by deadline, plus
// a per-list summary. It is recomputed from the files on every read, never
// trusted from a stored index.
type Board struct {
	Overdue []Task        `json:"overdue"`
	DueSoon []Task        `json:"due_soon"`
	Later   []Task        `json:"later"`
	NoDate  []Task        `json:"no_date"`
	Lists   []ListSummary `json:"lists"`
}

// ListSummary is one line of the board's list index.
type ListSummary struct {
	File  string `json:"file"`
	Name  string `json:"name"`
	Open  int    `json:"open"`
	Total int    `json:"total"`
}

var (
	// checklistRe matches a markdown checklist item, capturing leading
	// indentation, the box state, and the remaining text.
	checklistRe = regexp.MustCompile(`^(\s*)[-*] \[([ xX])\] ?(.*)$`)
	// linkRe matches a leading markdown link: [text](target).
	linkRe = regexp.MustCompile(`^\[([^\]]+)\]\(([^)]+)\)`)
	// dueRe validates a YYYY-MM-DD due date.
	dueRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// dateFmt is the canonical due-date format.
const dateFmt = "2006-01-02"

// ParseFile parses one list file's content into top-level tasks, with indented
// checklist lines attached as subtasks and any other indented line attached as
// a free-text note on the nearest task. Front matter and headings are skipped.
func ParseFile(file string, content []byte) []Task {
	lines := strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n")
	var tasks []Task
	// topIdx indexes the current top-level task; indented checklist lines become
	// its subtasks and any other indented (or `note:`) line becomes a note on it.
	// Free text belongs to the task, not the last subtask, so notes always attach
	// at the top level. -1 means no current task.
	topIdx := -1
	for i, line := range lines {
		m := checklistRe.FindStringSubmatch(line)
		if m == nil {
			if topIdx >= 0 && isNote(line) {
				if note := strings.TrimSpace(stripNotePrefix(line)); note != "" {
					tasks[topIdx].Notes = append(tasks[topIdx].Notes, note)
				}
				continue
			}
			// A non-indented, non-checklist line (heading, blank, prose) ends the
			// current grouping so a later note cannot leak onto a stale task.
			if strings.TrimSpace(line) == "" || !isIndented(line) {
				topIdx = -1
			}
			continue
		}
		t := parseTask(file, i+1, line, m)
		if indentWidth(m[1]) >= 2 && topIdx >= 0 {
			tasks[topIdx].Subtasks = append(tasks[topIdx].Subtasks, t)
			continue
		}
		tasks = append(tasks, t)
		topIdx = len(tasks) - 1
	}
	return tasks
}

// parseTask turns one matched checklist line into a Task, pulling out a leading
// markdown link and the trailing due:/prio:/#tag tokens.
func parseTask(file string, line int, raw string, m []string) Task {
	t := Task{
		File: file,
		Line: line,
		Raw:  strings.TrimRight(raw, "\r"),
		Done: m[2] == "x" || m[2] == "X",
	}
	text := strings.TrimSpace(m[3])
	if lm := linkRe.FindStringSubmatch(text); lm != nil {
		t.Link = lm[2]
		title := lm[1]
		rest := strings.TrimSpace(text[len(lm[0]):])
		extra, due, prio, tags := parseFields(rest)
		if extra != "" {
			title += " " + extra
		}
		t.Title, t.Due, t.Prio, t.Tags = strings.TrimSpace(title), due, prio, tags
		return t
	}
	t.Title, t.Due, t.Prio, t.Tags = parseFields(text)
	return t
}

// parseFields splits text on whitespace, extracting due:/prio:/#tag tokens and
// returning the remaining words as the title.
func parseFields(text string) (title, due, prio string, tags []string) {
	var words []string
	for _, tok := range strings.Fields(text) {
		switch {
		case strings.HasPrefix(tok, "due:"):
			v := tok[len("due:"):]
			if dueRe.MatchString(v) {
				due = v
				continue
			}
		case strings.HasPrefix(tok, "prio:"):
			if p := normalizePrio(tok[len("prio:"):]); p != "" {
				prio = p
				continue
			}
		case strings.HasPrefix(tok, "#") && len(tok) > 1:
			tags = append(tags, tok[1:])
			continue
		}
		words = append(words, tok)
	}
	return strings.Join(words, " "), due, prio, tags
}

func normalizePrio(v string) string {
	switch strings.ToLower(v) {
	case "high", "h":
		return "high"
	case "med", "medium", "m":
		return "med"
	case "low", "l":
		return "low"
	default:
		return ""
	}
}

func indentWidth(lead string) int {
	w := 0
	for _, r := range lead {
		if r == '\t' {
			w += 4
		} else {
			w++
		}
	}
	return w
}

func isIndented(line string) bool {
	return len(line) > 0 && (line[0] == ' ' || line[0] == '\t')
}

// isNote reports whether a non-checklist line should attach as a free-text note:
// an indented line, or a non-indented `note:` line.
func isNote(line string) bool {
	if isIndented(line) {
		return strings.TrimSpace(line) != ""
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "note:")
}

func stripNotePrefix(line string) string {
	s := strings.TrimSpace(line)
	if len(s) >= len("note:") && strings.EqualFold(s[:len("note:")], "note:") {
		return strings.TrimSpace(s[len("note:"):])
	}
	return line
}

// BuildList parses a file into a List with open/total counts (subtasks
// included). name is the display name (file base without extension).
func BuildList(file, name string, content []byte) List {
	tasks := ParseFile(file, content)
	open, total := count(tasks)
	return List{File: file, Name: name, Tasks: tasks, Open: open, Total: total}
}

func count(tasks []Task) (open, total int) {
	for _, t := range tasks {
		total++
		if !t.Done {
			open++
		}
		o, n := count(t.Subtasks)
		open += o
		total += n
	}
	return open, total
}

// BuildBoard derives the overview from the parsed lists, bucketing open
// top-level tasks by their due date relative to now and summarising each list.
func BuildBoard(lists []List, now time.Time) Board {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	soon := today.AddDate(0, 0, 7)
	var b Board
	for _, l := range lists {
		b.Lists = append(b.Lists, ListSummary{File: l.File, Name: l.Name, Open: l.Open, Total: l.Total})
		for _, t := range l.Tasks {
			if t.Done {
				continue
			}
			if t.Due == "" {
				b.NoDate = append(b.NoDate, t)
				continue
			}
			d, err := time.Parse(dateFmt, t.Due)
			switch {
			case err != nil:
				b.NoDate = append(b.NoDate, t)
			case d.Before(today):
				b.Overdue = append(b.Overdue, t)
			case d.Before(soon):
				b.DueSoon = append(b.DueSoon, t)
			default:
				b.Later = append(b.Later, t)
			}
		}
	}
	return b
}

// ToggleLine flips the checkbox on the 1-based line, returning the rewritten
// content. expect, when non-empty, must equal the current line (trailing CR
// ignored) or the edit is refused — a stale board cannot toggle the wrong task.
// Only the box marker changes; the rest of the line is left byte-for-byte intact.
func ToggleLine(content []byte, line int, expect string) ([]byte, error) {
	lines, ending := splitLines(string(content))
	idx := line - 1
	if idx < 0 || idx >= len(lines) {
		return nil, fmt.Errorf("line %d out of range", line)
	}
	cur := strings.TrimRight(lines[idx], "\r")
	if expect != "" && cur != expect {
		return nil, fmt.Errorf("task has changed; reload and try again")
	}
	m := checklistRe.FindStringSubmatchIndex(lines[idx])
	if m == nil {
		return nil, fmt.Errorf("line %d is not a task", line)
	}
	// m[4]:m[5] is the box-state capture (a single space or x/X).
	box := lines[idx][m[4]:m[5]]
	var repl string
	if box == " " {
		repl = "x"
	} else {
		repl = " "
	}
	lines[idx] = lines[idx][:m[4]] + repl + lines[idx][m[5]:]
	return []byte(strings.Join(lines, ending)), nil
}

// AppendTask returns content with a new open task line for title appended,
// ensuring the file ends with a newline before the new line. title is sanitised
// to a single line.
func AppendTask(content []byte, title string) []byte {
	title = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(title, "\r", " "), "\n", " "))
	line := "- [ ] " + title + "\n"
	if len(content) == 0 {
		return []byte(line)
	}
	s := string(content)
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	return []byte(s + line)
}

// splitLines splits s into lines, preserving whether "\r\n" or "\n" was the
// dominant ending so a rejoin round-trips. A trailing newline yields a final
// empty element, so Join restores it exactly.
func splitLines(s string) (lines []string, ending string) {
	ending = "\n"
	if strings.Contains(s, "\r\n") {
		ending = "\r\n"
		s = strings.ReplaceAll(s, "\r\n", "\n")
	}
	return strings.Split(s, "\n"), ending
}

// IsListFile reports whether a tasks/ entry is a user list file (a .md file that
// is not a feature/board marker).
func IsListFile(name string) bool {
	if !strings.HasSuffix(strings.ToLower(name), ".md") {
		return false
	}
	return !strings.HasPrefix(name, "_")
}

// DisplayName turns a list filename into a human label: drop the extension,
// turn separators into spaces, title-case lightly.
func DisplayName(name string) string {
	base := name
	if i := strings.LastIndex(base, "."); i >= 0 {
		base = base[:i]
	}
	base = strings.NewReplacer("-", " ", "_", " ").Replace(base)
	return strings.TrimSpace(base)
}
