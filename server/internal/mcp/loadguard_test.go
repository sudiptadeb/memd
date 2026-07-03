package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// These tests pin the load-first guard: a storage primitive called before
// memory_load is still served, but its first result carries a one-time
// reminder to call memory_load; later calls come back clean, memory_load
// satisfies the guard outright, and a fresh session re-arms it for the next
// conversation.

// loadReminderMarker is the prefix the guard prepends to the first guarded
// result of a session that has not called memory_load.
const loadReminderMarker = "[memd: memory_load has not been called"

func TestMCPLoadGuardRemindsOncePerSession(t *testing.T) {
	mux, conn := testMCPServer(t)
	initializeMCP(t, mux, conn.Token)

	// First guarded call before memory_load → the tool result is served,
	// prefixed with the load reminder. Not an error: no retry round trip.
	text, isErr, rpcErrored := callTool(t, mux, conn.Token, "memory_search", map[string]any{"query": "anything"})
	if rpcErrored || isErr {
		t.Fatalf("memory_search before memory_load errored: isErr=%v text=%q, want the served result with a reminder", isErr, text)
	}
	if !strings.Contains(text, loadReminderMarker) {
		t.Fatalf("first guarded result = %q, want the memory_load reminder prefix", text)
	}
	if !strings.Contains(text, "(no matches)") {
		t.Fatalf("first guarded result = %q, want the actual search result served alongside the reminder", text)
	}

	// The reminder fires once: the next call comes back clean.
	text, isErr, rpcErrored = callTool(t, mux, conn.Token, "memory_search", map[string]any{"query": "anything"})
	if rpcErrored || isErr {
		t.Fatalf("memory_search retry failed: isErr=%v text=%q", isErr, text)
	}
	if strings.Contains(text, loadReminderMarker) {
		t.Fatalf("second guarded result = %q, want no repeated reminder", text)
	}

	// A new initialize marks a new session and re-arms the reminder.
	initializeMCP(t, mux, conn.Token)
	text, isErr, rpcErrored = callTool(t, mux, conn.Token, "memory_search", map[string]any{"query": "anything"})
	if rpcErrored || isErr {
		t.Fatalf("memory_search after re-initialize errored: isErr=%v text=%q", isErr, text)
	}
	if !strings.Contains(text, loadReminderMarker) {
		t.Fatalf("guard did not re-arm after initialize: text=%q, want the reminder", text)
	}
}

func TestMCPLoadGuardSatisfiedByMemoryLoad(t *testing.T) {
	mux, conn := testMCPServer(t)
	initializeMCP(t, mux, conn.Token)

	text, isErr, rpcErrored := callTool(t, mux, conn.Token, "memory_load", map[string]any{})
	if rpcErrored || isErr {
		t.Fatalf("memory_load failed: isErr=%v text=%q", isErr, text)
	}

	text, isErr, rpcErrored = callTool(t, mux, conn.Token, "memory_search", map[string]any{"query": "anything"})
	if rpcErrored || isErr {
		t.Fatalf("memory_search after memory_load failed: isErr=%v text=%q", isErr, text)
	}
	if strings.Contains(text, loadReminderMarker) {
		t.Fatalf("memory_search after memory_load = %q, want no reminder", text)
	}
}

// TestMCPLoadGuardSurvivesReinitialize pins the mobile-reconnect fix: when a
// client echoes the server-issued Mcp-Session-Id, a later initialize (a
// reconnect after a transient transport error, or a parallel client on the
// same connector) must not wipe the first session's loaded state. Before
// session keying, every initialize reset the per-connector flag, so agents on
// flaky connections got "call memory_load first" nudges forever — even
// immediately after a successful memory_load.
func TestMCPLoadGuardSurvivesReinitialize(t *testing.T) {
	mux, conn := testMCPServer(t)
	sidA := initializeMCP(t, mux, conn.Token)

	text, isErr, rpcErrored := callToolSession(t, mux, conn.Token, sidA, "memory_load", map[string]any{})
	if rpcErrored || isErr {
		t.Fatalf("memory_load failed: isErr=%v text=%q", isErr, text)
	}

	// Reconnect: a second initialize starts its own session.
	sidB := initializeMCP(t, mux, conn.Token)
	if sidB == sidA {
		t.Fatalf("initialize reused session ID %q; each session must get its own", sidA)
	}

	// Session A already loaded — the new initialize must not have reset it.
	text, isErr, rpcErrored = callToolSession(t, mux, conn.Token, sidA, "memory_search", map[string]any{"query": "anything"})
	if rpcErrored || isErr {
		t.Fatalf("memory_search in session A failed after sibling initialize: isErr=%v text=%q", isErr, text)
	}
	if strings.Contains(text, loadReminderMarker) {
		t.Fatalf("session A got a load reminder after sibling initialize: %q", text)
	}

	// The new session has its own fresh guard.
	text, isErr, rpcErrored = callToolSession(t, mux, conn.Token, sidB, "memory_search", map[string]any{"query": "anything"})
	if rpcErrored || isErr {
		t.Fatalf("memory_search in fresh session B errored: isErr=%v text=%q", isErr, text)
	}
	if !strings.Contains(text, loadReminderMarker) {
		t.Fatalf("fresh session B skipped memory_load without a reminder (text=%q)", text)
	}
}

// TestMCPLoadGuardUnknownSessionID covers a server restart: the client keeps
// sending a session ID the server no longer knows. The guard degrades to the
// documented soft path — one served-with-reminder result, then clean results —
// never a hard failure.
func TestMCPLoadGuardUnknownSessionID(t *testing.T) {
	mux, conn := testMCPServer(t)

	text, isErr, rpcErrored := callToolSession(t, mux, conn.Token, "stale-session-id", "memory_search", map[string]any{"query": "anything"})
	if rpcErrored || isErr {
		t.Fatalf("unknown session first guarded call errored: isErr=%v text=%q", isErr, text)
	}
	if !strings.Contains(text, loadReminderMarker) {
		t.Fatalf("unknown session first guarded call = %q, want the memory_load reminder", text)
	}

	text, isErr, rpcErrored = callToolSession(t, mux, conn.Token, "stale-session-id", "memory_load", map[string]any{})
	if rpcErrored || isErr {
		t.Fatalf("memory_load failed: isErr=%v text=%q", isErr, text)
	}
	text, isErr, rpcErrored = callToolSession(t, mux, conn.Token, "stale-session-id", "memory_search", map[string]any{"query": "anything"})
	if rpcErrored || isErr {
		t.Fatalf("memory_search after memory_load failed: isErr=%v text=%q", isErr, text)
	}
	if strings.Contains(text, loadReminderMarker) {
		t.Fatalf("memory_search after memory_load = %q, want no reminder", text)
	}
}

func TestMCPLoadGuardSkipsIntrospectionAndWorkflows(t *testing.T) {
	mux, conn := testMCPServer(t)
	initializeMCP(t, mux, conn.Token)

	// Introspection tools and workflows don't depend on loaded memory
	// content (workflow bodies tell the agent to load), so they must not
	// consume the one-time reminder or carry it.
	for _, name := range []string{"memory_directories", "memory_status", "memd_housekeep"} {
		text, isErr, rpcErrored := callTool(t, mux, conn.Token, name, map[string]any{})
		if rpcErrored || isErr {
			t.Fatalf("%s before memory_load failed: isErr=%v text=%q", name, isErr, text)
		}
		if strings.Contains(text, loadReminderMarker) {
			t.Fatalf("%s carried the load reminder; unguarded tools must not: %q", name, text)
		}
	}

	// The reminder is still armed for the first guarded primitive.
	text, isErr, rpcErrored := callTool(t, mux, conn.Token, "memory_search", map[string]any{"query": "anything"})
	if rpcErrored || isErr {
		t.Fatalf("memory_search before memory_load errored: isErr=%v text=%q", isErr, text)
	}
	if !strings.Contains(text, loadReminderMarker) {
		t.Fatalf("memory_search before memory_load: text=%q, want the reminder", text)
	}
}

// initializeMCP drives one initialize JSON-RPC request, marking the start of a
// client session for the connector, and returns the Mcp-Session-Id the server
// issued for it.
func initializeMCP(t *testing.T, mux *http.ServeMux, token string) string {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize"}`
	req := httptest.NewRequest(http.MethodPost, "/mcp/"+token, strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("initialize status = %d, body=%s", rec.Code, rec.Body.String())
	}
	sid := rec.Header().Get("Mcp-Session-Id")
	if sid == "" {
		t.Fatalf("initialize response missing Mcp-Session-Id header")
	}
	return sid
}
