package mcp

import (
	"strings"
	"testing"
)

// memory_status leads with a server identity/uptime line so a transient
// gateway 502 can be classified after the fact: a fresh uptime means the
// server restarted, a long one means it never blinked and the failure was
// upstream of it.
func TestMemoryStatusReportsServerUptime(t *testing.T) {
	mux, conn := testMCPServer(t)
	initializeMCP(t, mux, conn.Token)

	text, isErr, rpcErrored := callTool(t, mux, conn.Token, "memory_status", map[string]any{})
	if rpcErrored || isErr {
		t.Fatalf("memory_status errored: isErr=%v text=%q", isErr, text)
	}
	first, _, _ := strings.Cut(text, "\n")
	if !strings.HasPrefix(first, "server: memd test (up ") || !strings.Contains(first, "started ") {
		t.Fatalf("memory_status first line = %q, want server identity/uptime line", first)
	}
}
