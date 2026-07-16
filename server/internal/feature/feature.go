// Package feature describes the built-in, file-first "structured memory"
// modules a directory can enable (tasks, calendar, …). To an agent these are
// presented as kinds of memory it can store in the directory — not as abstract
// "features" — so the language the agent sees (AgentSummary, BaseDoctrine) is
// deliberately memory-centric.
//
// A feature is, at heart, a folder in the directory plus a doctrine. For
// built-ins the canonical doctrine lives here in the server; a per-folder
// `_feature.md` holds the user's personal preferences, appended on top.
package feature

import "strings"

// Feature is a built-in structured-memory module.
type Feature struct {
	Key          string // stable id, e.g. "tasks"
	Name         string // human label for the UI, e.g. "Tasks"
	Folder       string // root folder inside the directory, e.g. "tasks"
	AgentSummary string // one line the agent sees: what this memory holds
	ComingSoon   bool   // registered for discovery but not yet usable

	baseDoctrine         string   // server-canonical doctrine (the stable base layer)
	prefsTemplate        string   // scaffolded into <Folder>/_feature.md on enable
	legacyPrefsTemplates []string // older scaffold texts still on disk in the wild
}

// BaseDoctrine returns the server-canonical doctrine for the feature — the
// stable base that a directory's `_feature.md` preferences are appended to.
func (f Feature) BaseDoctrine() string { return f.baseDoctrine }

// PreferencesTemplate is the starter content written to <Folder>/_feature.md
// when the feature is first enabled. It is a user-preferences template, not a
// copy of the base doctrine.
func (f Feature) PreferencesTemplate() string { return f.prefsTemplate }

// PreferencesOverlay distills a _feature.md body (front matter already
// stripped by the caller) down to the user's actual preferences. HTML
// comments — where the scaffold keeps its guidance — are removed, and a body
// that is just an untouched scaffold (current or legacy) yields "". The
// returned text is what memory_load appends under the directory's state and
// what memory_feature_guide layers on top of the base doctrine; an empty
// result means "no preferences set" and callers stay silent.
func (f Feature) PreferencesOverlay(body string) string {
	trimmed := strings.TrimSpace(body)
	for _, tpl := range append([]string{f.prefsTemplate}, f.legacyPrefsTemplates...) {
		if trimmed == strings.TrimSpace(tpl) {
			return ""
		}
	}
	stripped := strings.TrimSpace(stripHTMLComments(body))
	if !hasVisibleContent(stripped) {
		return ""
	}
	// Drop the scaffold's own title when the user wrote rules under it — the
	// preload labels the overlay itself, so the title is pure repetition. User
	// headings other than the scaffold's are kept.
	if title := strings.TrimSpace(firstLine(f.prefsTemplate)); title != "" {
		stripped = strings.TrimSpace(strings.TrimPrefix(stripped, title))
	}
	return collapseBlankRuns(stripped)
}

// firstLine returns s up to (not including) the first newline.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// collapseBlankRuns squeezes runs of blank lines down to a single blank line,
// tidying the holes left where comments and the scaffold title were removed.
func collapseBlankRuns(s string) string {
	var out []string
	blank := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) == "" {
			blank++
			if blank > 1 {
				continue
			}
			out = append(out, "")
			continue
		}
		blank = 0
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// stripHTMLComments removes <!-- ... --> spans (an unterminated comment runs
// to the end of the text) so scaffold guidance never reaches the agent.
func stripHTMLComments(s string) string {
	var sb strings.Builder
	for {
		open := strings.Index(s, "<!--")
		if open < 0 {
			sb.WriteString(s)
			return sb.String()
		}
		sb.WriteString(s[:open])
		rest := s[open+len("<!--"):]
		end := strings.Index(rest, "-->")
		if end < 0 {
			return sb.String()
		}
		s = rest[end+len("-->"):]
	}
}

// hasVisibleContent reports whether the text still says anything once headings
// and blank lines are ignored. The scaffold's "# Tasks — your preferences"
// title alone is not a preference.
func hasVisibleContent(s string) bool {
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		return true
	}
	return false
}

// Registry is an ordered set of features keyed by Key.
type Registry struct {
	order []string
	byKey map[string]Feature
}

// NewRegistry builds a registry from features in declaration order.
func NewRegistry(features ...Feature) *Registry {
	r := &Registry{byKey: make(map[string]Feature, len(features))}
	for _, f := range features {
		if _, dup := r.byKey[f.Key]; dup {
			continue
		}
		r.order = append(r.order, f.Key)
		r.byKey[f.Key] = f
	}
	return r
}

// Lookup returns the feature for key.
func (r *Registry) Lookup(key string) (Feature, bool) {
	f, ok := r.byKey[key]
	return f, ok
}

// Has reports whether key is a known feature.
func (r *Registry) Has(key string) bool {
	_, ok := r.byKey[key]
	return ok
}

// List returns the features in declaration order.
func (r *Registry) List() []Feature {
	out := make([]Feature, 0, len(r.order))
	for _, key := range r.order {
		out = append(out, r.byKey[key])
	}
	return out
}

// Builtins returns the registry of features memd ships.
func Builtins() *Registry {
	return NewRegistry(tasksFeature, calendarFeature)
}
