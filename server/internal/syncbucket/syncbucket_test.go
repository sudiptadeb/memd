package syncbucket

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sudiptadeb/memd/server/internal/account"
	"github.com/sudiptadeb/synckosh"
	"github.com/sudiptadeb/synckosh/bolt"
	"github.com/sudiptadeb/synckosh/client"
)

type env struct {
	t        *testing.T
	accounts *account.Store
	user     account.User
	url      string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	cfg, err := account.ParseDatabaseURL(filepath.Join(t.TempDir(), "memd.db"))
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := account.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = accounts.Close() })
	if err := accounts.Init(ctx); err != nil {
		t.Fatal(err)
	}
	user, err := accounts.CreateLocalUser(ctx, account.CreateUserInput{Username: "ada", Password: "correct horse battery staple"})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	New(accounts).Mount(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return &env{t: t, accounts: accounts, user: user, url: ts.URL}
}

// bucket makes a bucket and returns it with the token its owner sees in the
// list.
func (e *env) bucket(name string) (account.SyncBucket, string) {
	e.t.Helper()
	ctx := context.Background()
	made, err := e.accounts.CreateSyncBucket(ctx, e.user.ID, name)
	if err != nil {
		e.t.Fatal(err)
	}
	list, err := e.accounts.ListSyncBuckets(ctx, e.user.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, b := range list {
		if b.ID == made.ID && b.Token != "" {
			return b, b.Token
		}
	}
	e.t.Fatalf("bucket %s shows no token", made.ID)
	return account.SyncBucket{}, ""
}

// bearer is the header form: the bucket's address, and its token apart.
func (e *env) bearer(tok string) *client.Client {
	return &client.Client{URL: e.url + Prefix, Token: tok}
}

// inPath is the path form: one URL that carries the token.
func (e *env) inPath(tok string) *client.Client {
	return &client.Client{URL: e.url + Prefix + "/" + tok}
}

func (e *env) replica(c *client.Client, key *synckosh.DataKey) *client.Replica {
	e.t.Helper()
	local, err := bolt.OpenLocal(filepath.Join(e.t.TempDir(), "device.db"))
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { local.Close() })
	r, err := client.Open(client.Config{Client: c, Local: local, Key: key})
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

func (e *env) status(method, path, auth string) int {
	e.t.Helper()
	var body *strings.Reader
	if method == http.MethodPost {
		body = strings.NewReader(`{"items":[]}`)
	} else {
		body = strings.NewReader("")
	}
	req, err := http.NewRequest(method, e.url+path, body)
	if err != nil {
		e.t.Fatal(err)
	}
	if auth != "" {
		req.Header.Set("Authorization", "Bearer "+auth)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// One device syncs through the header form, a second joins through the path
// form, and each sees what the other wrote.
func TestBothAddressFormsSyncOneBucket(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	_, tok := e.bucket("browser")

	key, err := client.Create(ctx, e.bearer(tok), "a long enough passphrase", "Main passphrase")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	mac := e.replica(e.bearer(tok), key)
	if _, err := mac.Put(synckosh.Item{Collection: "bookmarks", Datatype: "bookmark", GUID: synckosh.NewGUID(),
		Private: json.RawMessage(`{"title":"Go","url":"https://go.dev/"}`)}); err != nil {
		t.Fatal(err)
	}
	if err := mac.Sync(ctx); err != nil {
		t.Fatalf("Sync over the header form: %v", err)
	}

	joined, err := client.Join(ctx, e.inPath(tok), "a long enough passphrase")
	if err != nil {
		t.Fatalf("Join over the path form: %v", err)
	}
	phone := e.replica(e.inPath(tok), joined)
	if err := phone.Sync(ctx); err != nil {
		t.Fatalf("Sync over the path form: %v", err)
	}
	got, err := phone.Items("bookmarks")
	if err != nil || len(got) != 1 || !strings.Contains(string(got[0].Private), "go.dev") {
		t.Fatalf("the second device has %+v %v", got, err)
	}

	if _, err := phone.Put(synckosh.Item{Collection: "bookmarks", Datatype: "bookmark", GUID: synckosh.NewGUID(),
		Private: json.RawMessage(`{"title":"memd"}`)}); err != nil {
		t.Fatal(err)
	}
	if err := phone.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if err := mac.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := mac.Items("bookmarks"); len(got) != 2 {
		t.Fatalf("the first device has %d items, want 2", len(got))
	}
}

func TestWrongMissingAndReplacedTokens(t *testing.T) {
	e := newEnv(t)
	b, tok := e.bucket("browser")
	wrong := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	for _, c := range []struct {
		name, method, path, auth string
		want                     int
	}{
		{"header form", http.MethodGet, "/sync/v1/pull", tok, http.StatusOK},
		{"path form", http.MethodGet, "/sync/" + tok + "/v1/pull", "", http.StatusOK},
		{"path form with its own token as header", http.MethodPost, "/sync/" + tok + "/v1/push", tok, http.StatusOK},
		{"no token", http.MethodGet, "/sync/v1/pull", "", http.StatusUnauthorized},
		{"no token, info", http.MethodGet, "/sync/v1", "", http.StatusUnauthorized},
		{"wrong header token", http.MethodPost, "/sync/v1/push", wrong, http.StatusUnauthorized},
		{"wrong path token", http.MethodGet, "/sync/" + wrong + "/v1/pull", "", http.StatusUnauthorized},
		{"path and header disagree", http.MethodGet, "/sync/" + tok + "/v1/pull", wrong, http.StatusUnauthorized},
		{"bare prefix", http.MethodGet, "/sync/", tok, http.StatusUnauthorized},
	} {
		if got := e.status(c.method, c.path, c.auth); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}

	rotated, err := e.accounts.RotateSyncBucketToken(context.Background(), e.user.ID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	tok2 := rotated.Token
	if got := e.status(http.MethodGet, "/sync/v1/pull", tok); got != http.StatusUnauthorized {
		t.Errorf("replaced token, header form: %d", got)
	}
	if got := e.status(http.MethodGet, "/sync/"+tok+"/v1/pull", ""); got != http.StatusUnauthorized {
		t.Errorf("replaced token, path form: %d", got)
	}
	if got := e.status(http.MethodGet, "/sync/"+tok2+"/v1/pull", ""); got != http.StatusOK {
		t.Errorf("new token: %d", got)
	}

	if err := e.accounts.DeleteSyncBucket(context.Background(), e.user.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if got := e.status(http.MethodGet, "/sync/v1/pull", tok2); got != http.StatusUnauthorized {
		t.Errorf("deleted bucket's token: %d", got)
	}
}

// A bucket's token reaches its own items and never another bucket's, whatever
// the request asks for.
func TestBucketsAreKeptApart(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	_, tokA := e.bucket("a")
	_, tokB := e.bucket("b")

	a := e.replica(e.bearer(tokA), nil)
	if _, err := a.Put(synckosh.Item{Collection: "notes", Datatype: "note", GUID: synckosh.NewGUID(), Private: json.RawMessage(`{"text":"a's"}`)}); err != nil {
		t.Fatal(err)
	}
	if err := a.Sync(ctx); err != nil {
		t.Fatal(err)
	}

	for _, c := range []*client.Client{e.bearer(tokB), e.inPath(tokB)} {
		p, err := c.Pull(ctx, synckosh.Filter{}, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Items) != 0 {
			t.Fatalf("bucket b's token pulled %d of bucket a's items", len(p.Items))
		}
	}
	if p, err := e.inPath(tokA).Pull(ctx, synckosh.Filter{}, 0, 0); err != nil || len(p.Items) != 1 {
		t.Fatalf("bucket a: %d items, %v", len(p.Items), err)
	}
}

// After a reset the server holds nothing, not even a sealed copy of the key: each device's next
// Sync stops with client.ErrBucketReset and pushes nothing, and sync is set up again from the
// start, a new key on the first device and a join on the next.
func TestResetMakesTheDevicesSetUpAgain(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	b, tok := e.bucket("browser")

	key, err := client.Create(ctx, e.bearer(tok), "a long enough passphrase", "Main passphrase")
	if err != nil {
		t.Fatal(err)
	}
	one := e.replica(e.bearer(tok), key)
	two := e.replica(e.inPath(tok), key)
	if _, err := one.Put(synckosh.Item{Collection: "bookmarks", Datatype: "bookmark", GUID: synckosh.NewGUID(), Private: json.RawMessage(`{"title":"one"}`)}); err != nil {
		t.Fatal(err)
	}
	for _, r := range []*client.Replica{one, two} {
		if err := r.Sync(ctx); err != nil {
			t.Fatal(err)
		}
	}
	serverItems := func() int {
		p, err := e.bearer(tok).Pull(ctx, synckosh.Filter{}, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		return len(p.Items)
	}
	if n := serverItems(); n != 2 { // the bookmark and the sealed key copy
		t.Fatalf("before the reset the server has %d items, want 2", n)
	}

	if err := e.accounts.ResetSyncBucket(ctx, e.user.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	for name, r := range map[string]*client.Replica{"one": one, "two": two} {
		if err := r.Sync(ctx); !errors.Is(err, client.ErrBucketReset) {
			t.Fatalf("device %s after the reset: %v, want client.ErrBucketReset", name, err)
		}
	}
	if n := serverItems(); n != 0 {
		t.Fatalf("after the reset the devices pushed %d items", n)
	}

	// Set up again: the first device makes a new key, the next joins with its passphrase.
	fresh, err := client.Create(ctx, e.bearer(tok), "a new long passphrase", "Main passphrase")
	if err != nil {
		t.Fatalf("Create after the reset: %v", err)
	}
	if joined, err := client.Join(ctx, e.inPath(tok), "a new long passphrase"); err != nil || joined.ID != fresh.ID {
		t.Fatalf("Join after the reset: %v", err)
	}
}
