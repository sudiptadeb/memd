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

// Feature is a built-in structured-memory module.
type Feature struct {
	Key          string // stable id, e.g. "tasks"
	Name         string // human label for the UI, e.g. "Tasks"
	Folder       string // root folder inside the directory, e.g. "tasks"
	AgentSummary string // one line the agent sees: what this memory holds
	ComingSoon   bool   // registered for discovery but not yet usable

	baseDoctrine  string // server-canonical doctrine (the stable base layer)
	prefsTemplate string // scaffolded into <Folder>/_feature.md on enable
}

// BaseDoctrine returns the server-canonical doctrine for the feature — the
// stable base that a directory's `_feature.md` preferences are appended to.
func (f Feature) BaseDoctrine() string { return f.baseDoctrine }

// PreferencesTemplate is the starter content written to <Folder>/_feature.md
// when the feature is first enabled. It is a user-preferences template, not a
// copy of the base doctrine.
func (f Feature) PreferencesTemplate() string { return f.prefsTemplate }

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
