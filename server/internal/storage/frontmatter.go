package storage

import (
	"bytes"
	"strconv"
	"strings"
	"time"
)

// dateFmt is the canonical date string used in the memd: subtree.
const dateFmt = "2006-01-02"

// MemdStats is the server-reserved memd: front matter subtree. The agent
// reads it but never writes it; the server overwrites whatever the agent
// puts in this subtree.
type MemdStats struct {
	CreatedAt   time.Time
	UpdatedAt   time.Time
	LastReadAt  time.Time
	AccessCount int
}

// IsZero reports whether the stats are unset (all timestamps zero, count 0).
func (m MemdStats) IsZero() bool {
	return m.CreatedAt.IsZero() &&
		m.UpdatedAt.IsZero() &&
		m.LastReadAt.IsZero() &&
		m.AccessCount == 0
}

// Page is a parsed memory page: server-managed stats, agent-managed raw FM
// blocks (preserved verbatim), and the body.
type Page struct {
	Stats   MemdStats
	AgentFM string // raw text of agent-managed FM blocks (without the --- delimiters or memd: subtree)
	HasFM   bool   // whether the original page had front matter
	Body    []byte // everything after the closing --- (or the whole file if no FM)
}

// ParsePage splits raw page bytes into FM components and body. If the page
// has no recognised front matter, AgentFM is empty, Stats is zero, HasFM is
// false, and Body is the entire raw input.
func ParsePage(raw []byte) Page {
	s := string(raw)
	var marker string
	switch {
	case strings.HasPrefix(s, "---\n"):
		marker = "---\n"
	case strings.HasPrefix(s, "---\r\n"):
		marker = "---\r\n"
	default:
		return Page{Body: raw}
	}
	rest := s[len(marker):]

	closeStart, closeEnd, ok := findClosingMarker(rest)
	if !ok {
		// Opened FM but never closed — treat whole file as body.
		return Page{Body: raw}
	}
	fmText := rest[:closeStart]
	body := []byte(rest[closeEnd:])
	stats, agentFM := parseFMBlocks(fmText)
	return Page{Stats: stats, AgentFM: agentFM, HasFM: true, Body: body}
}

// findClosingMarker scans `rest` for a line that is exactly "---" (with
// optional trailing \r). Returns the byte index where that line starts and
// where the next line begins, plus ok=true if found.
func findClosingMarker(rest string) (start, end int, ok bool) {
	i := 0
	for i < len(rest) {
		nl := strings.IndexByte(rest[i:], '\n')
		var line string
		var lineEnd int
		if nl < 0 {
			line = rest[i:]
			lineEnd = len(rest)
		} else {
			line = rest[i : i+nl]
			lineEnd = i + nl + 1
		}
		if strings.TrimRight(line, "\r") == "---" {
			return i, lineEnd, true
		}
		if nl < 0 {
			return 0, 0, false
		}
		i = nl + 1 + i
	}
	return 0, 0, false
}

// parseFMBlocks walks the FM text line by line, extracts the memd: subtree
// (if present) into MemdStats, and preserves all other top-level blocks
// verbatim into agentFM. A "block" is a top-level key line plus all
// immediately following indented lines.
func parseFMBlocks(fmText string) (MemdStats, string) {
	var stats MemdStats
	var agent strings.Builder

	lines := splitKeepEnding(fmText, '\n')
	i := 0
	for i < len(lines) {
		line := lines[i]
		stripped := strings.TrimRight(line, "\r\n")
		if stripped == "" {
			agent.WriteString(line)
			i++
			continue
		}
		if isIndented(line) {
			// Stray indented line outside any top-level key — preserve.
			agent.WriteString(line)
			i++
			continue
		}
		// Top-level key. Collect this line and any following indented lines.
		key := topLevelKey(stripped)
		start := i
		i++
		for i < len(lines) {
			if !isIndented(lines[i]) {
				break
			}
			i++
		}
		block := strings.Join(lines[start:i], "")
		if key == "memd" {
			stats = parseMemdBlock(block)
		} else {
			agent.WriteString(block)
		}
	}
	return stats, agent.String()
}

// splitKeepEnding splits s on sep but keeps the separator at the end of each
// piece (so re-joining is lossless).
func splitKeepEnding(s string, sep byte) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			out = append(out, s[start:i+1])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func isIndented(line string) bool {
	return len(line) > 0 && (line[0] == ' ' || line[0] == '\t')
}

func topLevelKey(line string) string {
	idx := strings.Index(line, ":")
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(line[:idx])
}

func parseMemdBlock(block string) MemdStats {
	var s MemdStats
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimRight(line, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed == "memd:" {
			continue
		}
		idx := strings.Index(trimmed, ":")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(trimmed[:idx])
		val := strings.TrimSpace(trimmed[idx+1:])
		switch key {
		case "created_at":
			if t, err := time.Parse(dateFmt, val); err == nil {
				s.CreatedAt = t
			}
		case "updated_at":
			if t, err := time.Parse(dateFmt, val); err == nil {
				s.UpdatedAt = t
			}
		case "last_read_at":
			if t, err := time.Parse(dateFmt, val); err == nil {
				s.LastReadAt = t
			}
		case "access_count":
			if n, err := strconv.Atoi(val); err == nil {
				s.AccessCount = n
			}
		}
	}
	return s
}

// Render serialises the page back to bytes. The memd: subtree is always
// written first; agent FM blocks follow verbatim. If Body has no leading
// newline a single \n is inserted between the closing --- and the body.
func (p Page) Render() []byte {
	var buf bytes.Buffer
	buf.WriteString("---\n")
	buf.WriteString("memd:\n")
	buf.WriteString("  created_at: ")
	buf.WriteString(p.Stats.CreatedAt.Format(dateFmt))
	buf.WriteByte('\n')
	buf.WriteString("  updated_at: ")
	buf.WriteString(p.Stats.UpdatedAt.Format(dateFmt))
	buf.WriteByte('\n')
	buf.WriteString("  last_read_at: ")
	buf.WriteString(p.Stats.LastReadAt.Format(dateFmt))
	buf.WriteByte('\n')
	buf.WriteString("  access_count: ")
	buf.WriteString(strconv.Itoa(p.Stats.AccessCount))
	buf.WriteByte('\n')
	if p.AgentFM != "" {
		agent := p.AgentFM
		if !strings.HasSuffix(agent, "\n") {
			agent += "\n"
		}
		buf.WriteString(agent)
	}
	buf.WriteString("---\n")
	body := p.Body
	if len(body) > 0 && body[0] != '\n' {
		buf.WriteByte('\n')
	}
	buf.Write(body)
	return buf.Bytes()
}

// today returns today's date (UTC) with hours/minutes/seconds zeroed.
func today() time.Time {
	t := time.Now().UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// StripAgentMemdSubtree removes any `memd:` block the agent may have included
// in its write payload. Used by the server before merging in its own
// authoritative MemdStats.
func StripAgentMemdSubtree(raw []byte) []byte {
	p := ParsePage(raw)
	if !p.HasFM {
		return raw
	}
	// Re-render with empty stats and the same agent FM — caller is expected
	// to fill in stats before persisting.
	return Page{AgentFM: p.AgentFM, Body: p.Body, HasFM: true}.Render()
}
