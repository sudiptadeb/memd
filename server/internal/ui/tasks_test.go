package ui

import (
	"bytes"
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

// newTasksFixture builds a UI handler with one local directory that has the
// tasks feature enabled and an inbox list seeded on disk.
func newTasksFixture(t *testing.T) (*http.ServeMux, *Handler, account.User, string, string) {
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
	if err := os.WriteFile(filepath.Join(root, "MEMORY.md"), []byte("# memory\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tasks", "inbox.md"),
		[]byte("- [ ] buy milk due:2099-01-01\n- [x] call dentist\n"), 0o644); err != nil {
		t.Fatal(err)
	}

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
	if _, err := reg.SetDirectoryFeatureForActor(user.ID, dirID, "tasks", true); err != nil {
		t.Fatalf("enable tasks: %v", err)
	}

	mux := http.NewServeMux()
	handler := New(reg, accounts, "http://127.0.0.1:7878", newTestSessions(t), oidc.NewManager(), nil)
	handler.Mount(mux)
	return mux, handler, user, dirID, root
}

func tasksReq(t *testing.T, mux *http.ServeMux, handler *Handler, user account.User, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, bytes.NewBufferString(body))
		r.Header.Set("Content-Type", "application/json")
	}
	addSession(t, handler, r, user)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	return rec
}

func TestTasksGetReturnsListsAndBoard(t *testing.T) {
	mux, handler, user, dirID, _ := newTasksFixture(t)
	rec := tasksReq(t, mux, handler, user, http.MethodGet, "/api/directories/"+dirID+"/tasks", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Lists []struct {
			File  string `json:"file"`
			Name  string `json:"name"`
			Open  int    `json:"open"`
			Total int    `json:"total"`
			Tasks []struct {
				Title string `json:"title"`
				Done  bool   `json:"done"`
				Line  int    `json:"line"`
			} `json:"tasks"`
		} `json:"lists"`
		Board struct {
			Later []struct {
				Title string `json:"title"`
			} `json:"later"`
		} `json:"board"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Lists) != 1 || resp.Lists[0].Name != "inbox" {
		t.Fatalf("lists = %+v", resp.Lists)
	}
	if resp.Lists[0].Open != 1 || resp.Lists[0].Total != 2 {
		t.Errorf("counts open=%d total=%d", resp.Lists[0].Open, resp.Lists[0].Total)
	}
	if len(resp.Board.Later) != 1 || resp.Board.Later[0].Title != "buy milk" {
		t.Errorf("board.later = %+v", resp.Board.Later)
	}
}

func TestTasksToggle(t *testing.T) {
	mux, handler, user, dirID, root := newTasksFixture(t)
	body := `{"action":"toggle","file":"tasks/inbox.md","line":1,"expect":"- [ ] buy milk due:2099-01-01"}`
	rec := tasksReq(t, mux, handler, user, http.MethodPost, "/api/directories/"+dirID+"/tasks", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	got, err := os.ReadFile(filepath.Join(root, "tasks", "inbox.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "- [x] buy milk due:2099-01-01") {
		t.Errorf("toggle not applied: %s", got)
	}
}

func TestTasksToggleStaleRejected(t *testing.T) {
	mux, handler, user, dirID, _ := newTasksFixture(t)
	body := `{"action":"toggle","file":"tasks/inbox.md","line":1,"expect":"- [ ] something stale"}`
	rec := tasksReq(t, mux, handler, user, http.MethodPost, "/api/directories/"+dirID+"/tasks", body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
}

func TestTasksAddToNewList(t *testing.T) {
	mux, handler, user, dirID, root := newTasksFixture(t)
	body := `{"action":"add","list_name":"Home Renovation","title":"paint the bedroom"}`
	rec := tasksReq(t, mux, handler, user, http.MethodPost, "/api/directories/"+dirID+"/tasks", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	got, err := os.ReadFile(filepath.Join(root, "tasks", "home-renovation.md"))
	if err != nil {
		t.Fatalf("new list not created: %v", err)
	}
	if !strings.Contains(string(got), "- [ ] paint the bedroom") {
		t.Errorf("task not appended: %s", got)
	}
}

func TestTasksRejectsPathEscape(t *testing.T) {
	mux, handler, user, dirID, _ := newTasksFixture(t)
	body := `{"action":"toggle","file":"../MEMORY.md","line":1}`
	rec := tasksReq(t, mux, handler, user, http.MethodPost, "/api/directories/"+dirID+"/tasks", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestTasksDisabledRejected(t *testing.T) {
	mux, handler, user, dirID, _ := newTasksFixture(t)
	if _, err := handler.reg.SetDirectoryFeatureForActor(user.ID, dirID, "tasks", false); err != nil {
		t.Fatal(err)
	}
	rec := tasksReq(t, mux, handler, user, http.MethodGet, "/api/directories/"+dirID+"/tasks", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}
