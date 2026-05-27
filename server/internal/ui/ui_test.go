package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sudiptadeb/memd/server/internal/logs"
	"github.com/sudiptadeb/memd/server/internal/registry"
)

func TestLogsAPIIsNotCached(t *testing.T) {
	reg := registry.NewEphemeral()
	t.Cleanup(func() { _ = reg.Close() })
	mux := http.NewServeMux()
	New(reg, "http://127.0.0.1:7878").Mount(mux)

	logs.Info("activity polling regression marker")
	req := httptest.NewRequest(http.MethodGet, "/api/logs?since=-1", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if got := rec.Header().Get("Pragma"); got != "no-cache" {
		t.Fatalf("Pragma = %q, want no-cache", got)
	}
	if !strings.Contains(rec.Body.String(), "activity polling regression marker") {
		t.Fatalf("logs response missing marker: %s", rec.Body.String())
	}
}
