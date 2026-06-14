package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sudiptadeb/memd/server/internal/config"
	"github.com/sudiptadeb/memd/server/internal/registry"
)

// TestMCPReadOnlyConnectorBlocksMutationsAllowsReads pins the memory-poisoning
// mitigation: a connector granted to a shared/reference directory can be marked
// read-only, and the MCP tools/call JSON-RPC path must refuse every mutating
// tool while still serving every read tool.
//
// HTTP-level read-only is already covered by http_test.go; this is the missing
// MCP (JSON-RPC tools/call) coverage. The four mutating tools must each return
// a tool-level error (result.isError == true, message "connector is read-only")
// and leave the filesystem byte-for-byte unchanged. The four read tools must
// succeed against the same connector.
func TestMCPReadOnlyConnectorBlocksMutationsAllowsReads(t *testing.T) {
	mux, conn, dir, dirID := testReadOnlyMCPServer(t)

	// Seed an existing page and folder so read/move/delete have real targets,
	// and so we can prove mutating calls don't change anything on disk.
	seedPath := filepath.Join(dir, "memory", "seed.md")
	if err := os.MkdirAll(filepath.Dir(seedPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	seedBody := "---\ntopic: shared-reference\n---\n# Seed\n\nimmutable reference content\n"
	if err := os.WriteFile(seedPath, []byte(seedBody), 0o644); err != nil {
		t.Fatalf("seed write: %v", err)
	}
	// Doctrine: memory_load is the session's first call. It also satisfies
	// the load-first guard, so the calls below exercise read-only
	// enforcement rather than the before-load nudge. It runs before the
	// snapshot because loading bumps MEMORY.md's managed read stats —
	// read-path bookkeeping, not a mutation gated by Write.
	if text, isErr, rpcErrored := callTool(t, mux, conn.Token, "memory_load", map[string]any{}); rpcErrored || isErr {
		t.Fatalf("memory_load failed: isErr=%v text=%q", isErr, text)
	}
	before := snapshotTree(t, dir)

	// --- Mutating tools must all be refused with the read-only error. ---
	mutating := []struct {
		name string
		args map[string]any
	}{
		{"memory_write", map[string]any{"directory_id": dirID, "path": "memory/new.md", "content": "# Poison\n"}},
		{"memory_move", map[string]any{"directory_id": dirID, "src": "memory/seed.md", "dst": "memory/moved.md"}},
		{"memory_delete", map[string]any{"directory_id": dirID, "path": "memory/seed.md"}},
		{"memory_delete_folder", map[string]any{"directory_id": dirID, "path": "memory"}},
	}
	for _, m := range mutating {
		t.Run("blocks_"+m.name, func(t *testing.T) {
			text, isErr, rpcErrored := callTool(t, mux, conn.Token, m.name, m.args)
			if rpcErrored {
				t.Fatalf("%s returned a JSON-RPC envelope error; want a tool result error", m.name)
			}
			if !isErr {
				t.Fatalf("%s on read-only connector: isError=false, want true (text=%q)", m.name, text)
			}
			if !strings.Contains(text, "read-only") {
				t.Fatalf("%s error text = %q, want it to mention read-only", m.name, text)
			}
		})
	}

	// Filesystem must be untouched by every blocked mutation.
	after := snapshotTree(t, dir)
	if !equalSnapshots(before, after) {
		t.Fatalf("read-only connector mutated the filesystem:\nbefore=%v\nafter=%v", before, after)
	}

	// --- Read tools must all still work on the same read-only connector. ---
	t.Run("allows_memory_load", func(t *testing.T) {
		text, isErr, rpcErrored := callTool(t, mux, conn.Token, "memory_load", map[string]any{})
		if rpcErrored || isErr {
			t.Fatalf("memory_load failed on read-only connector: isErr=%v text=%q", isErr, text)
		}
		if !strings.Contains(text, "# Active Memory") {
			t.Fatalf("memory_load missing active memory: %q", text)
		}
	})

	t.Run("allows_memory_read", func(t *testing.T) {
		text, isErr, rpcErrored := callTool(t, mux, conn.Token, "memory_read",
			map[string]any{"directory_id": dirID, "path": "memory/seed.md"})
		if rpcErrored || isErr {
			t.Fatalf("memory_read failed on read-only connector: isErr=%v text=%q", isErr, text)
		}
		if !strings.Contains(text, "immutable reference content") {
			t.Fatalf("memory_read body missing seed content: %q", text)
		}
	})

	t.Run("allows_memory_search", func(t *testing.T) {
		text, isErr, rpcErrored := callTool(t, mux, conn.Token, "memory_search",
			map[string]any{"directory_id": dirID, "query": "immutable"})
		if rpcErrored || isErr {
			t.Fatalf("memory_search failed on read-only connector: isErr=%v text=%q", isErr, text)
		}
		if !strings.Contains(text, "seed.md") {
			t.Fatalf("memory_search did not find seed.md: %q", text)
		}
	})

	t.Run("allows_memory_list", func(t *testing.T) {
		text, isErr, rpcErrored := callTool(t, mux, conn.Token, "memory_list",
			map[string]any{"directory_id": dirID, "path": "memory"})
		if rpcErrored || isErr {
			t.Fatalf("memory_list failed on read-only connector: isErr=%v text=%q", isErr, text)
		}
		if !strings.Contains(text, "seed.md") {
			t.Fatalf("memory_list did not list seed.md: %q", text)
		}
	})

	// A read tool that ran (memory_read bumps stats) is allowed to change the
	// seed file's managed metadata; that is read-path bookkeeping, not a
	// mutation gated by Write. The mutation snapshot above was taken BEFORE any
	// read tool ran, so it isolates the four write-gated tools.
}

// callTool drives one tools/call JSON-RPC request and returns the tool result
// text, its isError flag, and whether the response was a JSON-RPC envelope
// error (as opposed to a tool-level error inside result).
func callTool(t *testing.T, mux *http.ServeMux, token, name string, args map[string]any) (text string, isErr, rpcErrored bool) {
	t.Helper()
	params := map[string]any{"name": name, "arguments": args}
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  json.RawMessage(paramsJSON),
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/mcp/"+token, strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s HTTP status = %d, body=%s", name, rec.Code, rec.Body.String())
	}

	var resp struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
		Result *struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("%s decode response: %v (body=%s)", name, err, rec.Body.String())
	}
	if resp.Error != nil {
		return resp.Error.Message, true, true
	}
	if resp.Result == nil {
		t.Fatalf("%s response had neither result nor error: %s", name, rec.Body.String())
	}
	var sb strings.Builder
	for _, c := range resp.Result.Content {
		sb.WriteString(c.Text)
	}
	return sb.String(), resp.Result.IsError, false
}

// snapshotTree records relative path -> content for every regular file under
// root, so we can prove no mutating call altered the tree.
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", root, err)
	}
	return out
}

func equalSnapshots(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		if vb, ok := b[k]; !ok || vb != va {
			return false
		}
	}
	return true
}

// testReadOnlyMCPServer mounts an MCP server whose sole connector has
// Write:false, returning the mux, connector, the backing local dir, and its
// directory id. Distinct name from auth_test.go's testMCPServer (which is
// write-enabled) so both coexist in package mcp.
func testReadOnlyMCPServer(t *testing.T) (*http.ServeMux, config.Connector, string, string) {
	t.Helper()
	dir := t.TempDir()
	reg := registry.NewEphemeral()
	dirID, err := reg.AddDirectory(config.Directory{Name: "shared", Description: "shared reference memory", Backend: "local", LocalPath: dir})
	if err != nil {
		t.Fatalf("AddDirectory: %v", err)
	}
	conn, err := reg.AddConnector(config.Connector{Name: "readonly", Kind: config.ConnectorKindMCP, DirectoryIDs: []string{dirID}, Write: false})
	if err != nil {
		t.Fatalf("AddConnector: %v", err)
	}
	t.Cleanup(func() { _ = reg.Close() })

	server := newTestServer(reg)
	mux := http.NewServeMux()
	server.Mount(mux, "/mcp/")
	return mux, conn, dir, dirID
}
