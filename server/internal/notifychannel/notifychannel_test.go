package notifychannel

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/debkosh/memd/server/internal/account"
)

type env struct {
	t        *testing.T
	accounts *account.Store
	user     account.User
	h        *Handler
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
	h := New(accounts)
	mux := http.NewServeMux()
	h.Mount(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return &env{t: t, accounts: accounts, user: user, h: h, url: ts.URL}
}

func (e *env) channel() account.NotifyChannel {
	e.t.Helper()
	c, err := e.accounts.CreateNotifyChannel(context.Background(), e.user.ID, "Phone")
	if err != nil {
		e.t.Fatal(err)
	}
	return c
}

func (e *env) publish(tok, body string) (int, event) {
	e.t.Helper()
	resp, err := http.Post(e.url+"/notify/"+tok, "text/plain", strings.NewReader(body))
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var ev event
	_ = json.NewDecoder(resp.Body).Decode(&ev)
	return resp.StatusCode, ev
}

func (e *env) poll(tok, since string) []event {
	e.t.Helper()
	resp, err := http.Get(e.url + "/notify/" + tok + "/json?poll=1&since=" + since)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("poll: %d", resp.StatusCode)
	}
	var out []event
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		var ev event
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, ev)
	}
	return out
}

func TestPublishAndPoll(t *testing.T) {
	e := newEnv(t)
	c := e.channel()
	status, first := e.publish(c.Token, "sealed one")
	if status != http.StatusOK || first.Event != "message" || first.ID == "" || first.Message != "sealed one" {
		t.Fatalf("publish: %d %+v", status, first)
	}
	e.publish(c.Token, "sealed two")

	if got := e.poll(c.Token, "all"); len(got) != 2 || got[0].Message != "sealed one" || got[1].Message != "sealed two" {
		t.Fatalf("poll all: %+v", got)
	}
	if got := e.poll(c.Token, first.ID); len(got) != 1 || got[0].Message != "sealed two" {
		t.Fatalf("poll since the first: %+v", got)
	}
	// An id this server never gave (say, from ntfy.sh) gives everything kept.
	if got := e.poll(c.Token, "fromelsewhere"); len(got) != 2 {
		t.Fatalf("poll since an unknown id: %+v", got)
	}
}

func TestPublishRefusals(t *testing.T) {
	e := newEnv(t)
	c := e.channel()
	if status, _ := e.publish("nosuchtoken", "x"); status != http.StatusNotFound {
		t.Fatalf("unknown token: %d", status)
	}
	if status, _ := e.publish(c.Token, strings.Repeat("a", MaxMessage+1)); status != http.StatusRequestEntityTooLarge {
		t.Fatalf("too long: %d", status)
	}
	if status, _ := e.publish(c.Token, strings.Repeat("a", MaxMessage)); status != http.StatusOK {
		t.Fatalf("at the limit: %d", status)
	}
	if status, _ := e.publish(c.Token, "  "); status != http.StatusBadRequest {
		t.Fatalf("empty: %d", status)
	}
	// A new token ends the old one, for senders and pollers.
	if _, err := e.accounts.RotateNotifyChannelToken(context.Background(), e.user.ID, c.ID); err != nil {
		t.Fatal(err)
	}
	if status, _ := e.publish(c.Token, "x"); status != http.StatusNotFound {
		t.Fatalf("old token: %d", status)
	}
}

func dial(t *testing.T, e *env, tok, since string) *websocket.Conn {
	t.Helper()
	u := "ws" + strings.TrimPrefix(e.url, "http") + "/notify/" + tok + "/ws"
	if since != "" {
		u += "?since=" + since
	}
	conn, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func next(t *testing.T, conn *websocket.Conn) event {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var ev event
	if err := conn.ReadJSON(&ev); err != nil {
		t.Fatalf("read: %v", err)
	}
	return ev
}

func TestWebSocketBacklogThenLive(t *testing.T) {
	e := newEnv(t)
	c := e.channel()
	_, first := e.publish(c.Token, "before one")
	e.publish(c.Token, "before two")

	conn := dial(t, e, c.Token, first.ID)
	if ev := next(t, conn); ev.Event != "open" {
		t.Fatalf("first event: %+v", ev)
	}
	if ev := next(t, conn); ev.Event != "message" || ev.Message != "before two" {
		t.Fatalf("backlog: %+v", ev)
	}
	e.publish(c.Token, "live")
	if ev := next(t, conn); ev.Event != "message" || ev.Message != "live" {
		t.Fatalf("live: %+v", ev)
	}

	// Without since, only what comes next.
	fresh := dial(t, e, c.Token, "")
	if ev := next(t, fresh); ev.Event != "open" {
		t.Fatalf("open: %+v", ev)
	}
	e.publish(c.Token, "after")
	if ev := next(t, fresh); ev.Message != "after" {
		t.Fatalf("fresh stream: %+v", ev)
	}
}

func TestStreamEndsWhenTheTokenIsReplaced(t *testing.T) {
	e := newEnv(t)
	e.h.keepalive = 50 * time.Millisecond
	c := e.channel()
	conn := dial(t, e, c.Token, "")
	if ev := next(t, conn); ev.Event != "open" {
		t.Fatalf("open: %+v", ev)
	}
	if ev := next(t, conn); ev.Event != "keepalive" {
		t.Fatalf("keepalive: %+v", ev)
	}
	if _, err := e.accounts.RotateNotifyChannelToken(context.Background(), e.user.ID, c.ID); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		var ev event
		if err := conn.ReadJSON(&ev); err != nil {
			return // closed, as it should be
		}
		if ev.Event != "keepalive" {
			t.Fatalf("unexpected event after the new token: %+v", ev)
		}
	}
}

func TestJSONStream(t *testing.T) {
	e := newEnv(t)
	c := e.channel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, e.url+"/notify/"+c.Token+"/json", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	read := func() event {
		if !sc.Scan() {
			t.Fatalf("stream ended: %v", sc.Err())
		}
		var ev event
		_ = json.Unmarshal(sc.Bytes(), &ev)
		return ev
	}
	if ev := read(); ev.Event != "open" {
		t.Fatalf("open: %+v", ev)
	}
	e.publish(c.Token, "streamed")
	if ev := read(); ev.Message != "streamed" {
		t.Fatalf("message: %+v", ev)
	}
}

func TestKeepsAtMostKeepCountPerChannel(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.channel()
	b := e.channel()
	for i := 0; i < account.NotifyKeepCount+5; i++ {
		if _, err := e.accounts.PublishNotify(ctx, a.ID, "a"); err != nil {
			t.Fatal(err)
		}
		if i < 3 {
			if _, err := e.accounts.PublishNotify(ctx, b.ID, "b"); err != nil {
				t.Fatal(err)
			}
		}
	}
	counts, err := e.accounts.NotifyCounts(ctx, e.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if counts[a.ID] != account.NotifyKeepCount || counts[b.ID] != 3 {
		t.Fatalf("counts: %v", counts)
	}
	if err := e.accounts.DeleteNotifyChannel(ctx, e.user.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	if counts, _ := e.accounts.NotifyCounts(ctx, e.user.ID); counts[a.ID] != 0 || counts[b.ID] != 3 {
		t.Fatalf("after delete: %v", counts)
	}
}
