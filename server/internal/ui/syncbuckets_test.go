package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/debkosh/memd/server/internal/account"
	"github.com/debkosh/synckosh"
	"github.com/debkosh/synckosh/server"
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
	if len(list.Buckets) != 1 || list.Buckets[0].Token != made.Bucket.Token || list.Buckets[0].TokenURL != made.Bucket.TokenURL ||
		list.Buckets[0].Stats != nil {
		t.Fatalf("list: %+v", list.Buckets)
	}

	// Once a device has synced, the list says what the server holds.
	err := accounts.SyncStore().Update(made.Bucket.ID, func(tx server.Tx) error {
		return tx.Put([]synckosh.Item{
			{Collection: "bookmarks", Datatype: "b", GUID: "1", Timestamp: 42, Sequence: 1},
			{Collection: "_synckosh", Datatype: synckosh.SealedKeyDatatype, GUID: "c1", Timestamp: 2, Sequence: 2,
				Public: []byte(`{"label":"Backup, 22 Nov","code":true,"created":1795300000000,"key":"k1","kdf":"argon2id","salt":"SALT","box":"BOX"}`)},
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	rec = call(&ada, http.MethodGet, "/api/sync/buckets", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list.Buckets) != 1 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	if st := list.Buckets[0].Stats; st == nil || st.Newest != 42 || st.Bytes == 0 ||
		len(st.Collections) != 1 || st.Collections[0] != (syncCollectionView{Name: "bookmarks", Items: 1}) {
		t.Fatalf("stats: %s", rec.Body.String())
	}
	if k := list.Buckets[0].Stats.Keys; len(k) != 1 || k[0] != (syncKeyView{ID: "c1", Label: "Backup, 22 Nov", KeyID: "k1", BackupCode: true, Created: "2026-11-21T22:26:40Z"}) {
		t.Fatalf("keys: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "SALT") || strings.Contains(rec.Body.String(), "BOX") {
		t.Fatalf("the list shows a sealed copy's salt or box: %s", rec.Body.String())
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

// An export is a manifest line and then each item, still sealed, without the
// token; exports are limited per account and per IP, with Retry-After, and a
// bucket is exported one at a time.
func TestSyncBucketExport(t *testing.T) {
	accounts := openTestAccountStore(t)
	ctx := context.Background()
	ada, _ := accounts.CreateLocalUser(ctx, account.CreateUserInput{Username: "ada", Password: "correct horse battery staple"})
	bob, _ := accounts.CreateLocalUser(ctx, account.CreateUserInput{Username: "bob", Password: "correct horse battery staple"})
	mux, handler := newTestUI(t, accounts)
	b, _ := accounts.CreateSyncBucket(ctx, ada.ID, "My phone")
	err := accounts.SyncStore().Update(b.ID, func(tx server.Tx) error {
		return tx.Put([]synckosh.Item{
			{Collection: "bookmarks", Datatype: "b", GUID: "1", Timestamp: 7, Sequence: 1},
			{Collection: "_synckosh", Datatype: "key", GUID: "k", Timestamp: 9, Sequence: 2},
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	export := func(user account.User, ip, id string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/sync/buckets/"+id+"/export", nil)
		req.RemoteAddr = ip + ":1234"
		addSession(t, handler, req, user)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	rec := export(ada, "10.0.0.1", b.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("export: %d %s", rec.Code, rec.Body.String())
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, `filename="My-phone-`) || !strings.HasSuffix(cd, `.synckosh.jsonl"`) {
		t.Fatalf("Content-Disposition %q", cd)
	}
	lines := strings.Split(strings.TrimSuffix(rec.Body.String(), "\n"), "\n")
	var m syncExportManifest
	if len(lines) != 3 || json.Unmarshal([]byte(lines[0]), &m) != nil {
		t.Fatalf("export body: %q", rec.Body.String())
	}
	if m.Format != "synckosh-export" || m.Protocol != 1 || m.Bucket != "My phone" || m.Items != 2 || m.Newest != 9 ||
		m.Bytes != int64(len(lines[1])+len(lines[2])) {
		t.Fatalf("manifest: %+v", m)
	}
	var it synckosh.Item
	if json.Unmarshal([]byte(lines[1]), &it) != nil || it.GUID != "1" || json.Unmarshal([]byte(lines[2]), &it) != nil || it.GUID != "k" {
		t.Fatalf("items: %q", lines[1:])
	}
	if strings.Contains(rec.Body.String(), b.Token) {
		t.Fatal("the export carries the bucket's token")
	}

	// bob can't export ada's bucket, and that counts toward his own limit.
	if rec := export(bob, "10.0.0.9", b.ID); rec.Code != http.StatusNotFound {
		t.Fatalf("bob exporting ada's bucket: %d", rec.Code)
	}

	// Three an hour per account, whatever the IP.
	export(ada, "10.0.0.2", b.ID)
	export(ada, "10.0.0.3", b.ID)
	rec = export(ada, "10.0.0.4", b.ID)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("fourth export by ada: %d %q", rec.Code, rec.Header().Get("Retry-After"))
	}
	// Three an hour per IP, whatever the account: 10.0.0.1 has ada's first.
	carol, _ := accounts.CreateLocalUser(ctx, account.CreateUserInput{Username: "carol", Password: "correct horse battery staple"})
	cb, _ := accounts.CreateSyncBucket(ctx, carol.ID, "Carol's")
	export(carol, "10.0.0.1", cb.ID)
	export(carol, "10.0.0.1", cb.ID)
	if rec := export(carol, "10.0.0.1", cb.ID); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("fourth export from 10.0.0.1: %d", rec.Code)
	}

	// One at a time per bucket.
	dave, _ := accounts.CreateLocalUser(ctx, account.CreateUserInput{Username: "dave", Password: "correct horse battery staple"})
	db, _ := accounts.CreateSyncBucket(ctx, dave.ID, "Dave's")
	handler.exporting.Store(db.ID, true)
	if rec := export(dave, "10.0.0.5", db.ID); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("export while one runs: %d", rec.Code)
	}
	handler.exporting.Delete(db.ID)
	if rec := export(dave, "10.0.0.6", db.ID); rec.Code != http.StatusOK {
		t.Fatalf("export after: %d", rec.Code)
	}
}
