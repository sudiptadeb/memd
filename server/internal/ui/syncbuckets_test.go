package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sudiptadeb/memd/server/internal/account"
)

func TestSyncBucketsAPI(t *testing.T) {
	accounts := openTestAccountStore(t)
	ctx := context.Background()
	ada, _ := accounts.CreateLocalUser(ctx, account.CreateUserInput{Username: "ada", Password: "correct horse battery staple"})
	bob, _ := accounts.CreateLocalUser(ctx, account.CreateUserInput{Username: "bob", Password: "correct horse battery staple"})
	mux, handler := newTestUI(t, accounts)

	call := func(user *account.User, method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		if user != nil {
			addSession(t, handler, req, *user)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	type secret struct {
		Bucket   syncBucketView `json:"bucket"`
		Token    string         `json:"token"`
		TokenURL string         `json:"token_url"`
	}

	if rec := call(nil, http.MethodGet, "/api/sync/buckets", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous list: %d", rec.Code)
	}
	if rec := call(&ada, http.MethodPost, "/api/sync/buckets", `{"name":""}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("no name: %d", rec.Code)
	}

	rec := call(&ada, http.MethodPost, "/api/sync/buckets", `{"name":"Browser"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var made secret
	if err := json.Unmarshal(rec.Body.Bytes(), &made); err != nil {
		t.Fatal(err)
	}
	if made.Bucket.Name != "Browser" || made.Bucket.URL != "http://127.0.0.1:7878/sync" ||
		made.Token == "" || made.TokenURL != "http://127.0.0.1:7878/sync/"+made.Token {
		t.Fatalf("create answer: %+v", made)
	}
	if b, err := accounts.SyncBucketByToken(ctx, made.Token); err != nil || b.ID != made.Bucket.ID {
		t.Fatalf("the token is not the bucket's: %+v %v", b, err)
	}

	rec = call(&ada, http.MethodGet, "/api/sync/buckets", "")
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), made.Token) || !strings.Contains(rec.Body.String(), made.Bucket.ID) {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(&bob, http.MethodGet, "/api/sync/buckets", ""); strings.Contains(rec.Body.String(), made.Bucket.ID) {
		t.Fatalf("bob's list has ada's bucket: %s", rec.Body.String())
	}

	path := "/api/sync/buckets/" + made.Bucket.ID
	if rec := call(&bob, http.MethodPost, path+"/rotate", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("bob rotating ada's bucket: %d", rec.Code)
	}
	if rec := call(&bob, http.MethodDelete, path, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("bob deleting ada's bucket: %d", rec.Code)
	}

	rec = call(&ada, http.MethodPost, path+"/rotate", "")
	var rotated secret
	if err := json.Unmarshal(rec.Body.Bytes(), &rotated); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("rotate: %d %s", rec.Code, rec.Body.String())
	}
	if rotated.Token == made.Token || rotated.Bucket.ID != made.Bucket.ID {
		t.Fatalf("rotate answer: %+v", rotated)
	}
	if _, err := accounts.SyncBucketByToken(ctx, made.Token); err == nil {
		t.Fatal("the old token still works")
	}

	if rec := call(&ada, http.MethodDelete, path, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", rec.Code)
	}
	if _, err := accounts.SyncBucketByToken(ctx, rotated.Token); err == nil {
		t.Fatal("a deleted bucket's token still works")
	}
}
