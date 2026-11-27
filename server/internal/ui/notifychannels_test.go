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
)

func TestNotifyChannelsAPI(t *testing.T) {
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
		Channel notifyChannelView `json:"channel"`
	}
	type list struct {
		Channels []notifyChannelView `json:"channels"`
	}

	if rec := call(nil, http.MethodGet, "/api/notify/channels", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous list: %d", rec.Code)
	}
	if rec := call(&ada, http.MethodPost, "/api/notify/channels", `{"name":""}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("no name: %d", rec.Code)
	}
	rec := call(&ada, http.MethodPost, "/api/notify/channels", `{"name":"Phone"}`)
	var made answer
	if err := json.Unmarshal(rec.Body.Bytes(), &made); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	tok := strings.TrimPrefix(made.Channel.URL, "http://127.0.0.1:7878/notify/")
	if made.Channel.Name != "Phone" || tok == made.Channel.URL || len(tok) < 16 {
		t.Fatalf("create answer: %+v", made)
	}
	if c, err := accounts.NotifyChannelByToken(ctx, tok); err != nil || c.ID != made.Channel.ID {
		t.Fatalf("the address is not the channel's: %+v %v", c, err)
	}
	if _, err := accounts.PublishNotify(ctx, made.Channel.ID, "sealed"); err != nil {
		t.Fatal(err)
	}

	var l list
	rec = call(&ada, http.MethodGet, "/api/notify/channels", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil || len(l.Channels) != 1 ||
		l.Channels[0].URL != made.Channel.URL || l.Channels[0].Messages != 1 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	rec = call(&bob, http.MethodGet, "/api/notify/channels", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil || len(l.Channels) != 0 {
		t.Fatalf("bob sees ada's channel: %s", rec.Body.String())
	}

	// Only the owner can replace the token or delete the channel.
	path := "/api/notify/channels/" + made.Channel.ID
	if rec := call(&bob, http.MethodPost, path+"/rotate", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("bob rotate: %d", rec.Code)
	}
	if rec := call(&bob, http.MethodDelete, path, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("bob delete: %d", rec.Code)
	}
	rec = call(&ada, http.MethodPost, path+"/rotate", "")
	var rotated answer
	if err := json.Unmarshal(rec.Body.Bytes(), &rotated); err != nil || rotated.Channel.URL == made.Channel.URL {
		t.Fatalf("rotate: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := accounts.NotifyChannelByToken(ctx, tok); err == nil {
		t.Fatal("the old token still works")
	}
	if rec := call(&ada, http.MethodDelete, path, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", rec.Code)
	}
	rec = call(&ada, http.MethodGet, "/api/notify/channels", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil || len(l.Channels) != 0 {
		t.Fatalf("after delete: %s", rec.Body.String())
	}
}
