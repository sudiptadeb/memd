package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sudiptadeb/memd/server/internal/account"
	"github.com/sudiptadeb/memd/server/internal/config"
	"github.com/sudiptadeb/memd/server/internal/oidc"
	"github.com/sudiptadeb/memd/server/internal/registry"
)

// newFilesFixture builds a UI handler with one local directory containing a
// few files and returns the mux, handler, owning user, and directory ID.
func newFilesFixture(t *testing.T) (*http.ServeMux, *Handler, account.User, string) {
	t.Helper()
	accounts := openTestAccountStore(t)
	user, err := accounts.CreateLocalUser(context.Background(), account.CreateUserInput{
		Username: "ada",
		Password: "correct horse battery staple",
	})
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}

	root := t.TempDir()
	mustWrite := func(rel, content string) {
		t.Helper()
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	mustWrite("MEMORY.md", "# memory index\n")
	mustWrite("notes/idea.md", "an idea\n")
	mustWrite("evil.html", "<script>alert(1)</script>")
	mustWrite(".hidden.md", "secret\n")

	reg := registry.NewEphemeral()
	t.Cleanup(func() { _ = reg.Close() })
	dirID, err := reg.AddDirectoryForUser(user.ID, config.Directory{
		Name:      "work",
		Backend:   "local",
		LocalPath: root,
	})
	if err != nil {
		t.Fatalf("AddDirectoryForUser: %v", err)
	}

	mux := http.NewServeMux()
	handler := New(reg, accounts, "http://127.0.0.1:7878", newTestSessions(t), oidc.NewManager(), nil)
	handler.Mount(mux)
	return mux, handler, user, dirID
}

func filesGet(t *testing.T, mux *http.ServeMux, handler *Handler, user account.User, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	addSession(t, handler, req, user)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestDirectoryFilesAPIListsRootAndSubdir(t *testing.T) {
	mux, handler, user, dirID := newFilesFixture(t)

	rec := filesGet(t, mux, handler, user, "/api/directories/"+dirID+"/files")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var listing struct {
		Path    string `json:"path"`
		Entries []struct {
			Name  string `json:"name"`
			Path  string `json:"path"`
			IsDir bool   `json:"is_dir"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listing); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	names := map[string]bool{}
	for _, e := range listing.Entries {
		names[e.Name] = e.IsDir
	}
	if isDir, ok := names["notes"]; !ok || !isDir {
		t.Fatalf("notes folder missing or not a dir: %+v", listing.Entries)
	}
	if isDir, ok := names["MEMORY.md"]; !ok || isDir {
		t.Fatalf("MEMORY.md missing or marked dir: %+v", listing.Entries)
	}
	if _, ok := names[".hidden.md"]; ok {
		t.Fatalf("hidden file leaked into listing: %+v", listing.Entries)
	}
	if len(listing.Entries) > 0 && !listing.Entries[0].IsDir {
		t.Fatalf("folders should sort first: %+v", listing.Entries)
	}

	rec = filesGet(t, mux, handler, user, "/api/directories/"+dirID+"/files?path=notes")
	if rec.Code != http.StatusOK {
		t.Fatalf("subdir status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "idea.md") {
		t.Fatalf("subdir listing missing idea.md: %s", rec.Body.String())
	}
}

func TestDirectoryRawAPIServesPlainTextOnly(t *testing.T) {
	mux, handler, user, dirID := newFilesFixture(t)

	rec := filesGet(t, mux, handler, user, "/api/directories/"+dirID+"/raw?path=MEMORY.md")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
	if got := rec.Header().Get("Content-Security-Policy"); !strings.Contains(got, "sandbox") {
		t.Fatalf("Content-Security-Policy = %q, want sandbox", got)
	}
	if rec.Body.String() != "# memory index\n" {
		t.Fatalf("body = %q", rec.Body.String())
	}

	// Stored HTML is untrusted agent content: it must never come back with an
	// executable content type on the UI origin.
	rec = filesGet(t, mux, handler, user, "/api/directories/"+dirID+"/raw?path=evil.html")
	if rec.Code != http.StatusOK {
		t.Fatalf("html status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("html Content-Type = %q, must stay text/plain", got)
	}
}

func TestDirectoryRawAPIRenderAndDownloadModes(t *testing.T) {
	mux, handler, user, dirID := newFilesFixture(t)

	// Rendered HTML must come back sandboxed: opaque origin, no scripts, no
	// remote subresources. allow-same-origin or allow-scripts here would be a
	// stored XSS, so pin the exact policy.
	rec := filesGet(t, mux, handler, user, "/api/directories/"+dirID+"/raw?path=evil.html&render=1")
	if rec.Code != http.StatusOK {
		t.Fatalf("render status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("render Content-Type = %q", got)
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "sandbox;") && !strings.HasPrefix(csp, "sandbox") {
		t.Fatalf("render CSP missing sandbox: %q", csp)
	}
	if strings.Contains(csp, "allow-same-origin") || strings.Contains(csp, "allow-scripts") {
		t.Fatalf("render CSP must not relax the sandbox: %q", csp)
	}
	if !strings.Contains(csp, "default-src 'none'") {
		t.Fatalf("render CSP missing default-src 'none': %q", csp)
	}

	// render=1 is only honored for renderable markup; everything else stays
	// plain text.
	rec = filesGet(t, mux, handler, user, "/api/directories/"+dirID+"/raw?path=MEMORY.md&render=1")
	if got := rec.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("render on .md Content-Type = %q, want text/plain", got)
	}

	// download=1 forces an attachment and never renders, even for HTML.
	rec = filesGet(t, mux, handler, user, "/api/directories/"+dirID+"/raw?path=evil.html&download=1&render=1")
	if got := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "attachment") {
		t.Fatalf("download Content-Disposition = %q", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("download Content-Type = %q, want text/plain", got)
	}
}

func TestDirectoryFilesAPIRejectsTraversalAndStrangers(t *testing.T) {
	mux, handler, user, dirID := newFilesFixture(t)

	for _, target := range []string{
		"/api/directories/" + dirID + "/raw?path=../outside.txt",
		"/api/directories/" + dirID + "/raw?path=..%2F..%2Fetc%2Fpasswd",
		"/api/directories/" + dirID + "/files?path=../",
	} {
		rec := filesGet(t, mux, handler, user, target)
		if rec.Code == http.StatusOK {
			t.Fatalf("traversal %s returned 200: %s", target, rec.Body.String())
		}
	}

	stranger, err := handler.accounts.CreateLocalUser(context.Background(), account.CreateUserInput{
		Username: "mallory",
		Password: "correct horse battery staple",
	})
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	rec := filesGet(t, mux, handler, stranger, "/api/directories/"+dirID+"/files")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("stranger files status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	rec = filesGet(t, mux, handler, stranger, "/api/directories/"+dirID+"/raw?path=MEMORY.md")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("stranger raw status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}
