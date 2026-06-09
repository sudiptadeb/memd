package oidc

import (
	"context"
	"sync"
)

// Manager holds the currently active OIDC provider and allows it to be
// (re)configured at runtime — the configuration lives in the database and is
// edited by a super admin, so the provider must be swappable without a restart.
// A nil provider means OIDC is disabled and the app falls back to local login.
type Manager struct {
	mu       sync.RWMutex
	provider *Provider
	cfg      Config
	enabled  bool
}

// NewManager returns an empty manager with OIDC disabled.
func NewManager() *Manager { return &Manager{} }

// Configure performs discovery for cfg and atomically swaps in the new provider.
// On error the previous provider is left untouched, so a bad edit in the admin
// UI does not take down a working configuration.
func (m *Manager) Configure(ctx context.Context, cfg Config) error {
	provider, err := New(ctx, cfg)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.provider = provider
	m.cfg = cfg
	m.enabled = true
	m.mu.Unlock()
	return nil
}

// Disable turns OIDC off (e.g. when a super admin clears the configuration).
func (m *Manager) Disable() {
	m.mu.Lock()
	m.provider = nil
	m.enabled = false
	m.mu.Unlock()
}

// Provider returns the active provider, or nil when OIDC is disabled.
func (m *Manager) Provider() *Provider {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.provider
}

// Enabled reports whether an IdP is currently configured.
func (m *Manager) Enabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.enabled
}
