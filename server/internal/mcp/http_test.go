package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sudiptadeb/memd/server/internal/config"
	"github.com/sudiptadeb/memd/server/internal/registry"
)

func TestHTTPConnectorMemoryLoadAndSkill(t *testing.T) {
	srv, conn := testHTTPServer(t, true)

	req := httptest.NewRequest(http.MethodGet, "/http/"+conn.Token+"/memory_load", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("memory_load status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if got := rec.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("Referrer-Policy = %q, want no-referrer", got)
	}
	if !strings.Contains(rec.Body.String(), "# Active Memory") {
		t.Fatalf("memory_load body missing active memory: %s", rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/http/"+conn.Token, nil)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("skill status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "memd HTTP Skill") {
		t.Fatalf("skill body missing expected text: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "/http/"+conn.Token+"/memory_load") {
		t.Fatalf("skill body missing tokenized memory_load URL: %s", rec.Body.String())
	}
}

func TestHTTPEndpointRejectsMCPConnector(t *testing.T) {
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

	server := New(reg, "doctrine", "memd", "test")
	mux := http.NewServeMux()
	server.MountHTTP(mux, "/http/")

	req := httptest.NewRequest(http.MethodGet, "/http/"+conn.Token+"/memory_load", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

func TestHTTPWriteRequiresPostAndWriteAccess(t *testing.T) {
	srv, conn := testHTTPServer(t, false)

	req := httptest.NewRequest(http.MethodGet, "/http/"+conn.Token+"/memory_write", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET memory_write status = %d, want 405", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/http/"+conn.Token+"/memory_write", strings.NewReader(`{"directory_id":"x","path":"a.md","content":"# A"}`))
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST read-only memory_write status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "read-only") {
		t.Fatalf("read-only error missing, body=%s", rec.Body.String())
	}
}

func testHTTPServer(t *testing.T, write bool) (*http.ServeMux, config.Connector) {
	t.Helper()
	dir := t.TempDir()
	reg := registry.NewEphemeral()
	dirID, err := reg.AddDirectory(config.Directory{Name: "test", Description: "test memory", Backend: "local", LocalPath: dir})
	if err != nil {
		t.Fatalf("AddDirectory: %v", err)
	}
	conn, err := reg.AddConnector(config.Connector{Name: "web", Kind: config.ConnectorKindHTTP, DirectoryIDs: []string{dirID}, Write: write})
	if err != nil {
		t.Fatalf("AddConnector: %v", err)
	}
	t.Cleanup(func() { _ = reg.Close() })

	server := New(reg, "doctrine", "memd", "test")
	mux := http.NewServeMux()
	server.MountHTTP(mux, "/http/")
	return mux, conn
}
