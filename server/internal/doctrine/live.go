package doctrine

import "sync"

// Live is an in-memory registry of editable doctrine texts: the global doctrine
// plus each built-in feature's base doctrine. A super admin can override any of
// them at runtime for quick experimentation; overrides are deliberately NOT
// persisted, so a restart reverts every doctrine to its compiled default.
type Live struct {
	mu        sync.RWMutex
	order     []string
	labels    map[string]string
	defaults  map[string]string
	overrides map[string]string
}

// EntryView is the resolved state of one doctrine for display/editing.
type EntryView struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Text       string `json:"text"`       // current effective text (override or default)
	Overridden bool   `json:"overridden"` // true when a runtime override is active
}

// NewLive returns an empty store. Register the global and feature doctrines on it.
func NewLive() *Live {
	return &Live{
		labels:    map[string]string{},
		defaults:  map[string]string{},
		overrides: map[string]string{},
	}
}

// Register adds a doctrine with its compiled-default text. Re-registering an ID
// updates its label/default but keeps any active override.
func (l *Live) Register(id, label, defaultText string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.defaults[id]; !ok {
		l.order = append(l.order, id)
	}
	l.labels[id] = label
	l.defaults[id] = defaultText
}

// Get returns the effective text for id: the override if set, else the default.
func (l *Live) Get(id string) string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if v, ok := l.overrides[id]; ok {
		return v
	}
	return l.defaults[id]
}

// Set installs a runtime override for id (in memory only). Reports false for an
// unknown id.
func (l *Live) Set(id, text string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.defaults[id]; !ok {
		return false
	}
	l.overrides[id] = text
	return true
}

// Reset clears any override for id, reverting to the compiled default. Reports
// false for an unknown id.
func (l *Live) Reset(id string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.defaults[id]; !ok {
		return false
	}
	delete(l.overrides, id)
	return true
}

// List returns every registered doctrine with its effective text, in
// registration order.
func (l *Live) List() []EntryView {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]EntryView, 0, len(l.order))
	for _, id := range l.order {
		text, overridden := l.overrides[id]
		if !overridden {
			text = l.defaults[id]
		}
		out = append(out, EntryView{
			ID:         id,
			Label:      l.labels[id],
			Text:       text,
			Overridden: overridden,
		})
	}
	return out
}
