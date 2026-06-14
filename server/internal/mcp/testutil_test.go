package mcp

import (
	"github.com/sudiptadeb/memd/server/internal/doctrine"
	"github.com/sudiptadeb/memd/server/internal/feature"
	"github.com/sudiptadeb/memd/server/internal/registry"
)

// newTestServer builds an MCP server with a live doctrine store seeded with a
// trivial global doctrine and the built-in feature doctrines.
func newTestServer(reg *registry.Registry) *Server {
	live := doctrine.NewLive()
	live.Register(doctrine.GlobalID, "Global doctrine", "doctrine")
	features := feature.Builtins()
	feature.RegisterDoctrines(live, features)
	return New(reg, live, features, "memd", "test")
}
