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
	type answer struct {
		Bucket syncBucketView `json:"bucket"`
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
	var made answer
	if err := json.Unmarshal(rec.Body.Bytes(), &made); err != nil {
		t.Fatal(err)
	}
	if made.Bucket.Name != "Browser" || made.Bucket.URL != "http://127.0.0.1:7878/sync" ||
		made.Bucket.Token == "" || made.Bucket.TokenURL != "http://127.0.0.1:7878/sync/"+made.Bucket.Token {
		t.Fatalf("create answer: %+v", made)
	}
	if b, err := accounts.SyncBucketByToken(ctx, made.Bucket.Token); err != nil || b.ID != made.Bucket.ID {
		t.Fatalf("the token is not the bucket's: %+v %v", b, err)
	}

	// The owner's list carries the token, so another device can be set up
	// later; another user's list has neither the bucket nor its token.
	rec = call(&ada, http.MethodGet, "/api/sync/buckets", "")
	var list struct {
		Buckets []syncBucketView `json:"buckets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	if len(list.Buckets) != 1 || list.Buckets[0].Token != made.Bucket.Token || list.Buckets[0].TokenURL != made.Bucket.TokenURL {
		t.Fatalf("list: %+v", list.Buckets)
	}
	if rec := call(&bob, http.MethodGet, "/api/sync/buckets", ""); strings.Contains(rec.Body.String(), made.Bucket.ID) ||
		strings.Contains(rec.Body.String(), made.Bucket.Token) {
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
	var rotated answer
	if err := json.Unmarshal(rec.Body.Bytes(), &rotated); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("rotate: %d %s", rec.Code, rec.Body.String())
	}
	if rotated.Bucket.Token == made.Bucket.Token || rotated.Bucket.ID != made.Bucket.ID {
		t.Fatalf("rotate answer: %+v", rotated)
	}
	if _, err := accounts.SyncBucketByToken(ctx, made.Bucket.Token); err == nil {
		t.Fatal("the old token still works")
	}

	if rec := call(&bob, http.MethodPost, path+"/reset", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("bob resetting ada's bucket: %d", rec.Code)
	}
	if rec := call(&ada, http.MethodPost, path+"/reset", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("reset: %d %s", rec.Code, rec.Body.String())
	}

	if rec := call(&ada, http.MethodDelete, path, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", rec.Code)
	}
	if _, err := accounts.SyncBucketByToken(ctx, rotated.Bucket.Token); err == nil {
		t.Fatal("a deleted bucket's token still works")
	}
}
