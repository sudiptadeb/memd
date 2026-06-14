package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sudiptadeb/memd/server/internal/config"
	"github.com/sudiptadeb/memd/server/internal/registry"
)

func TestMCPConnectorSupportsAuthorizationHeader(t *testing.T) {
	mux, conn := testMCPServer(t)

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	req.Header.Set("Authorization", "Bearer "+conn.Token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("header-auth MCP status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"name":"memd"`) {
		t.Fatalf("initialize response missing server info: %s", rec.Body.String())
	}
}

func TestMCPConnectorStillSupportsTokenInURL(t *testing.T) {
	mux, conn := testMCPServer(t)

	req := httptest.NewRequest(http.MethodPost, "/mcp/"+conn.Token, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("token URL MCP status = %d, body=%s", rec.Code, rec.Body.String())
	}
}

func testMCPServer(t *testing.T) (*http.ServeMux, config.Connector) {
	t.Helper()
	dir := t.TempDir()
	reg := registry.NewEphemeral()
	dirID, err := reg.AddDirectory(config.Directory{Name: "test", Backend: "local", LocalPath: dir})
	if err != nil {
		t.Fatalf("AddDirectory: %v", err)
	}
	conn, err := reg.AddConnector(config.Connector{Name: "mcp", Kind: config.ConnectorKindMCP, DirectoryIDs: []string{dirID}, Write: true})
	if err != nil {
		t.Fatalf("AddConnector: %v", err)
	}
	t.Cleanup(func() { _ = reg.Close() })

	server := newTestServer(reg)
	mux := http.NewServeMux()
	server.Mount(mux, "/mcp/")
	return mux, conn
}
