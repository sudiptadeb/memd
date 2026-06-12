package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// These tests pin the load-first guard: a storage primitive called before
// memory_load gets a one-time soft error pointing at memory_load, the retry
// passes through (no livelock), memory_load satisfies the guard outright,
// and a fresh initialize re-arms it for the next conversation.

func TestMCPLoadGuardNudgesOncePerSession(t *testing.T) {
	mux, conn := testMCPServer(t)
	initializeMCP(t, mux, conn.Token)

	// First guarded call before memory_load → soft error nudge.
	text, isErr, rpcErrored := callTool(t, mux, conn.Token, "memory_search", map[string]any{"query": "anything"})
	if rpcErrored {
		t.Fatalf("memory_search returned a JSON-RPC envelope error; want a tool result error")
	}
	if !isErr {
		t.Fatalf("memory_search before memory_load: isError=false, want the nudge (text=%q)", text)
	}
	if !strings.Contains(text, "memory_load") {
		t.Fatalf("nudge text = %q, want it to point at memory_load", text)
	}

	// The nudge fires once: retrying the same call passes through.
	text, isErr, rpcErrored = callTool(t, mux, conn.Token, "memory_search", map[string]any{"query": "anything"})
	if rpcErrored || isErr {
		t.Fatalf("memory_search retry after nudge failed: isErr=%v text=%q", isErr, text)
	}

	// A new initialize marks a new session and re-arms the guard.
	initializeMCP(t, mux, conn.Token)
	_, isErr, rpcErrored = callTool(t, mux, conn.Token, "memory_search", map[string]any{"query": "anything"})
	if rpcErrored {
		t.Fatalf("memory_search returned a JSON-RPC envelope error; want a tool result error")
	}
	if !isErr {
		t.Fatalf("guard did not re-arm after initialize: isError=false, want the nudge")
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
		t.Fatalf("memory_search after memory_load got nudged: isErr=%v text=%q", isErr, text)
	}
}

func TestMCPLoadGuardSkipsIntrospectionAndWorkflows(t *testing.T) {
	mux, conn := testMCPServer(t)
	initializeMCP(t, mux, conn.Token)

	// Introspection tools and workflows don't depend on loaded memory
	// content (workflow bodies tell the agent to load), so they must not
	// consume or trigger the nudge.
	for _, name := range []string{"memory_directories", "memory_status", "memd_housekeep"} {
		text, isErr, rpcErrored := callTool(t, mux, conn.Token, name, map[string]any{})
		if rpcErrored || isErr {
			t.Fatalf("%s before memory_load failed: isErr=%v text=%q", name, isErr, text)
		}
	}

	// The nudge is still armed for the first guarded primitive.
	text, isErr, rpcErrored := callTool(t, mux, conn.Token, "memory_search", map[string]any{"query": "anything"})
	if rpcErrored {
		t.Fatalf("memory_search returned a JSON-RPC envelope error; want a tool result error")
	}
	if !isErr {
		t.Fatalf("memory_search before memory_load: isError=false, want the nudge (text=%q)", text)
	}
}

// initializeMCP drives one initialize JSON-RPC request, marking the start of a
// client session for the connector.
func initializeMCP(t *testing.T, mux *http.ServeMux, token string) {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize"}`
	req := httptest.NewRequest(http.MethodPost, "/mcp/"+token, strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("initialize status = %d, body=%s", rec.Code, rec.Body.String())
	}
}
