package tunnel

// End-to-end test of the rendezvous: a real HTTP server running the tunnel
// handler, a fake agent (WebSocket + smux.Server, spliced to a local backend
// exactly like termulaa -rc), and a viewer driving both a plain HTTP request
// and a WebSocket upgrade through the proxy. The backend asserts it received
// LOOPBACK Host/Origin values — the property that lets an unmodified termulaa
// accept proxied traffic.

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/xtaci/smux"
)

const (
	testViewHost = "term.test"
	testUserID   = "user-1"
)

type rig struct {
	t       *testing.T
	handler *Handler
	server  *httptest.Server
	key     []byte
	authed  atomic.Bool
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{t: t, key: TokenKey("e2e-secret", "")}
	r.authed.Store(true)
	auth := func(w http.ResponseWriter, req *http.Request) (User, bool) {
		if !r.authed.Load() {
			return User{}, false
		}
		return User{ID: testUserID, Name: "alice"}, true
	}
	r.handler = New(r.key, testViewHost, auth)
	mux := http.NewServeMux()
	r.handler.Mount(mux)
	// Stand-in for memd's existing routes: they must stay reachable on the
	// main host and must NOT be reachable on the view host.
	mux.HandleFunc("/api/tabs", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("memd-own-api"))
	})
	r.server = httptest.NewServer(r.handler.SplitByHost(mux))
	t.Cleanup(r.server.Close)
	return r
}

func (r *rig) mintToken() string {
	r.t.Helper()
	token, _, err := MintToken(r.key, testUserID, "e2e", time.Hour)
	if err != nil {
		r.t.Fatalf("mint: %v", err)
	}
	return token
}

// viewerRequest sends an HTTP request addressed to the view host (or any
// other host) through the test server, without following redirects.
func (r *rig) request(host, path string, mutate func(*http.Request)) *http.Response {
	r.t.Helper()
	req, err := http.NewRequest(http.MethodGet, r.server.URL+path, nil)
	if err != nil {
		r.t.Fatalf("new request: %v", err)
	}
	req.Host = host
	if mutate != nil {
		mutate(req)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	if err != nil {
		r.t.Fatalf("request %s%s: %v", host, path, err)
	}
	return resp
}

func body(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

// --- fake termulaa backend ----------------------------------------------

// backend is the loopback HTTP+WebSocket server standing in for termulaa. It
// enforces termulaa's actual guard: loopback-only Host and Origin.
type backend struct {
	t      *testing.T
	server *httptest.Server
	port   string
	addr   string
}

func startBackend(t *testing.T) *backend {
	t.Helper()
	b := &backend{t: t}
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		u, err := url.Parse(origin)
		return err == nil && u.Hostname() == "localhost"
	}}
	mux := http.NewServeMux()
	guard := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			// termulaa's DNS-rebinding stance: reject any non-loopback Host.
			if r.Host != "localhost:"+b.port {
				t.Errorf("backend saw non-loopback Host %q", r.Host)
				http.Error(w, "misdirected", http.StatusMisdirectedRequest)
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" && origin != "http://localhost:"+b.port {
				t.Errorf("backend saw non-loopback Origin %q", origin)
				http.Error(w, "forbidden origin", http.StatusForbidden)
				return
			}
			if got := r.Header.Get("X-Forwarded-Host"); got != "" {
				t.Errorf("X-Forwarded-Host leaked to backend: %q", got)
			}
			if got := r.Header.Get("X-Forwarded-Proto"); got != "" {
				t.Errorf("X-Forwarded-Proto leaked to backend: %q", got)
			}
			if _, err := r.Cookie(viewerCookieName); err == nil {
				t.Error("pairing cookie leaked to backend")
			}
			next(w, r)
		}
	}
	mux.HandleFunc("/hello", guard(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "hello from termulaa via %s", r.Host)
	}))
	mux.HandleFunc("/api/tabs", guard(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("termulaa-tabs"))
	}))
	mux.HandleFunc("/ws/echo", guard(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("backend upgrade: %v", err)
			return
		}
		defer ws.Close()
		for {
			mt, msg, err := ws.ReadMessage()
			if err != nil {
				return
			}
			if err := ws.WriteMessage(mt, msg); err != nil {
				return
			}
		}
	}))
	b.server = httptest.NewServer(mux)
	t.Cleanup(b.server.Close)
	b.addr = strings.TrimPrefix(b.server.URL, "http://")
	_, b.port, _ = net.SplitHostPort(b.addr)
	return b
}

// --- fake agent ----------------------------------------------------------

// agent mimics termulaa -rc: a pool of outbound WebSocket tunnels, each an
// smux.Server whose accepted streams are spliced byte-for-byte to the local
// backend. It parses no HTTP.
type agent struct {
	t     *testing.T
	mu    sync.Mutex
	conns []*websocket.Conn
}

func startAgent(t *testing.T, r *rig, token string, backendAddr, backendPort string, tunnels int) *agent {
	t.Helper()
	a := &agent{t: t}
	wsBase := "ws" + strings.TrimPrefix(r.server.URL, "http")
	for i := 0; i < tunnels; i++ {
		u := fmt.Sprintf("%s/rc/tunnel?agent=%s&session=%d&port=%s",
			wsBase, url.QueryEscape("e2e-box"), i, backendPort)
		header := http.Header{"Authorization": {"Bearer " + token}}
		ws, resp, err := websocket.DefaultDialer.Dial(u, header)
		if err != nil {
			t.Fatalf("agent dial tunnel %d: %v (resp=%v)", i, err, resp)
		}
		a.mu.Lock()
		a.conns = append(a.conns, ws)
		a.mu.Unlock()
		sess, err := smux.Server(newWSConn(ws), muxConfig())
		if err != nil {
			t.Fatalf("agent smux %d: %v", i, err)
		}
		go func() {
			for {
				stream, err := sess.AcceptStream()
				if err != nil {
					return
				}
				go splice(stream, backendAddr)
			}
		}()
	}
	t.Cleanup(a.close)
	// The handler registers the tunnel after the upgrade returns; wait for
	// the pool to be fully visible.
	waitFor(t, func() bool { return r.handler.hub.Tunnels(TokenAgentID(token)) == tunnels },
		"agent pool registration")
	return a
}

// splice is the agent's whole data plane: dial loopback, pump bytes.
func splice(stream net.Conn, backendAddr string) {
	local, err := net.Dial("tcp", backendAddr)
	if err != nil {
		_ = stream.Close()
		return
	}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(local, stream); done <- struct{}{} }()
	go func() { _, _ = io.Copy(stream, local); done <- struct{}{} }()
	<-done
	_ = stream.Close()
	_ = local.Close()
}

// killTunnel severs one pooled connection abruptly, as a network drop would.
func (a *agent) killTunnel(i int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	_ = a.conns[i].UnderlyingConn().Close()
}

func (a *agent) close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, ws := range a.conns {
		_ = ws.Close()
	}
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// --- the tests -----------------------------------------------------------

func TestEndToEnd(t *testing.T) {
	r := newRig(t)
	b := startBackend(t)
	token := r.mintToken()
	a := startAgent(t, r, token, b.addr, b.port, 3)

	// Pairing: /?t=<token> on the view host sets the cookie and redirects so
	// the token leaves the URL bar.
	resp := r.request(testViewHost, "/?t="+url.QueryEscape(token), nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("pairing status = %d, want 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Errorf("pairing redirect = %q, want /", loc)
	}
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == viewerCookieName {
			cookie = c
		}
	}
	resp.Body.Close()
	if cookie == nil {
		t.Fatal("pairing set no cookie")
	}
	if !cookie.HttpOnly || cookie.Path != "/" {
		t.Errorf("cookie attributes wrong: %+v", cookie)
	}

	// Plain HTTP through the tunnel, with a hostile public Origin that must be
	// rewritten to loopback before the backend sees it.
	resp = r.request(testViewHost, "/hello", func(req *http.Request) {
		req.AddCookie(cookie)
		req.Header.Set("Origin", "https://"+testViewHost)
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("proxied GET status = %d", resp.StatusCode)
	}
	if got := body(t, resp); got != "hello from termulaa via localhost:"+b.port {
		t.Errorf("proxied GET body = %q", got)
	}

	// The view host serves termulaa's absolute paths, never memd's own API.
	resp = r.request(testViewHost, "/api/tabs", func(req *http.Request) { req.AddCookie(cookie) })
	if got := body(t, resp); got != "termulaa-tabs" {
		t.Errorf("view-host /api/tabs = %q, want the proxied backend, not memd", got)
	}

	// WebSocket upgrade through the tunnel: the 101 path must work end to end,
	// again with a hostile Origin.
	echoOnce := func() {
		dialer := websocket.Dialer{NetDial: func(network, addr string) (net.Conn, error) {
			return net.Dial("tcp", r.server.Listener.Addr().String())
		}}
		header := http.Header{
			"Cookie": {viewerCookieName + "=" + cookie.Value},
			"Origin": {"https://" + testViewHost},
		}
		ws, resp2, err := dialer.Dial("ws://"+testViewHost+"/ws/echo", header)
		if err != nil {
			t.Fatalf("viewer websocket dial: %v (resp=%v)", err, resp2)
		}
		defer ws.Close()
		if err := ws.WriteMessage(websocket.TextMessage, []byte("ping over tunnel")); err != nil {
			t.Fatalf("ws write: %v", err)
		}
		_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, msg, err := ws.ReadMessage()
		if err != nil {
			t.Fatalf("ws read: %v", err)
		}
		if string(msg) != "ping over tunnel" {
			t.Errorf("echo = %q", msg)
		}
	}
	echoOnce()

	// Multi-tunnel pool resilience: kill members one at a time; the pool at
	// reduced strength must keep serving both HTTP and WebSocket traffic.
	id := TokenAgentID(token)
	for _, kill := range []int{0, 1} {
		a.killTunnel(kill)
		waitFor(t, func() bool { return r.handler.hub.Tunnels(id) == 2-kill },
			"dead tunnel reaped")
		resp = r.request(testViewHost, "/hello", func(req *http.Request) { req.AddCookie(cookie) })
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET after killing tunnel %d: status %d", kill, resp.StatusCode)
		}
		resp.Body.Close()
		echoOnce()
	}

	// Live status: the page API reports the surviving pool truthfully.
	resp = r.request(r.server.Listener.Addr().String(), "/rc/api/agents", nil)
	var status struct {
		Agents []struct {
			ID      string `json:"id"`
			Label   string `json:"label"`
			Tunnels int    `json:"tunnels"`
		} `json:"agents"`
		ViewHost string `json:"view_host"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatalf("agents api decode: %v", err)
	}
	resp.Body.Close()
	if len(status.Agents) != 1 || status.Agents[0].Tunnels != 1 ||
		status.Agents[0].Label != "e2e-box" || status.Agents[0].ID != shortID(id) {
		t.Errorf("agents api = %+v, want one agent with 1 tunnel up", status)
	}
	if status.ViewHost != testViewHost {
		t.Errorf("view_host = %q", status.ViewHost)
	}
}

func TestRoutingSeam(t *testing.T) {
	r := newRig(t)
	mainHost := r.server.Listener.Addr().String()

	// Main host: /rc/tunnel is the tunnel handler (401 without a token), not
	// the SPA fallback or the proxy.
	resp := r.request(mainHost, "/rc/tunnel", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("main-host /rc/tunnel status = %d, want 401 from the tunnel handler", resp.StatusCode)
	}
	resp.Body.Close()

	// Main host: memd's own routes are untouched.
	resp = r.request(mainHost, "/api/tabs", nil)
	if got := body(t, resp); got != "memd-own-api" {
		t.Errorf("main-host /api/tabs = %q, want memd's own handler", got)
	}

	// View host, unpaired: every path is the viewer surface — 401 pair page,
	// never memd routes.
	for _, path := range []string{"/", "/api/tabs", "/rc", "/ws/x"} {
		resp = r.request(testViewHost, path, nil)
		got := body(t, resp)
		if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(got, "Pair this browser") {
			t.Errorf("view-host %s = %d %q, want the 401 pair page", path, resp.StatusCode, got)
		}
	}

	// View host with a bogus pairing attempt.
	resp = r.request(testViewHost, "/?t=not-a-token", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("bogus pairing status = %d, want 401", resp.StatusCode)
	}
	resp.Body.Close()

	// Valid cookie but agent never connected: honest offline page, no
	// fabricated liveness.
	token := r.mintToken()
	resp = r.request(testViewHost, "/hello", func(req *http.Request) {
		req.AddCookie(&http.Cookie{Name: viewerCookieName, Value: token})
	})
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("offline agent status = %d, want 503", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestTunnelHandshakeAuth(t *testing.T) {
	r := newRig(t)
	wsBase := "ws" + strings.TrimPrefix(r.server.URL, "http")

	cases := []struct {
		name   string
		url    string
		header http.Header
		want   int
	}{
		{"no token", wsBase + "/rc/tunnel?port=1234", nil, http.StatusUnauthorized},
		{"garbage token", wsBase + "/rc/tunnel?port=1234",
			http.Header{"Authorization": {"Bearer garbage"}}, http.StatusUnauthorized},
		{"missing port", wsBase + "/rc/tunnel",
			http.Header{"Authorization": {"Bearer " + r.mintToken()}}, http.StatusBadRequest},
		{"bad port", wsBase + "/rc/tunnel?port=70000",
			http.Header{"Authorization": {"Bearer " + r.mintToken()}}, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, resp, err := websocket.DefaultDialer.Dial(tc.url, tc.header)
			if err == nil {
				t.Fatal("handshake unexpectedly succeeded")
			}
			if resp == nil || resp.StatusCode != tc.want {
				t.Errorf("status = %v, want %d", resp, tc.want)
			}
		})
	}
}

func TestMintAPI(t *testing.T) {
	r := newRig(t)
	mainHost := r.server.Listener.Addr().String()

	mint := func(bodyJSON string) *http.Response {
		req, err := http.NewRequest(http.MethodPost, r.server.URL+"/rc/api/tokens",
			strings.NewReader(bodyJSON))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Host = mainHost
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("mint request: %v", err)
		}
		return resp
	}

	resp := mint(`{"label":"my laptop","ttl":45}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("mint status = %d", resp.StatusCode)
	}
	var minted struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&minted); err != nil {
		t.Fatalf("mint decode: %v", err)
	}
	resp.Body.Close()
	claims, err := ParseToken(r.key, minted.Token)
	if err != nil {
		t.Fatalf("minted token does not verify: %v", err)
	}
	if claims.UserID != testUserID || claims.Label != "my laptop" {
		t.Errorf("claims = %+v", claims)
	}
	expires, err := time.Parse(time.RFC3339, minted.ExpiresAt)
	if err != nil {
		t.Fatalf("expires_at %q: %v", minted.ExpiresAt, err)
	}
	if got := time.Until(expires); got < 44*24*time.Hour || got > 46*24*time.Hour {
		t.Errorf("ttl 45 days: expiry %v away", got.Round(time.Hour))
	}

	// TTL above the cap is clamped to 90 days.
	resp = mint(`{"ttl":400}`)
	if err := json.NewDecoder(resp.Body).Decode(&minted); err != nil {
		t.Fatalf("clamped mint decode: %v", err)
	}
	resp.Body.Close()
	expires, _ = time.Parse(time.RFC3339, minted.ExpiresAt)
	if got := time.Until(expires); got > 91*24*time.Hour {
		t.Errorf("ttl 400 days not clamped: expiry %v away", got.Round(time.Hour))
	}

	// Unauthenticated requests are refused.
	r.authed.Store(false)
	resp = mint(`{}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("unauthenticated mint status = %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestManagementPage(t *testing.T) {
	r := newRig(t)
	mainHost := r.server.Listener.Addr().String()

	resp := r.request(mainHost, "/rc", nil)
	got := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/rc status = %d", resp.StatusCode)
	}
	if strings.Contains(got, "<script>") {
		t.Error("/rc page contains an inline script, which memd's CSP blocks")
	}
	if !strings.Contains(got, `src="/rc/app.js"`) {
		t.Error("/rc page does not load its same-origin script")
	}

	resp = r.request(mainHost, "/rc/app.js", nil)
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Errorf("app.js content-type = %q", ct)
	}
	resp.Body.Close()

	r.authed.Store(false)
	resp = r.request(mainHost, "/rc", nil)
	got = body(t, resp)
	if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(got, "Sign in") {
		t.Errorf("unauthenticated /rc = %d %q", resp.StatusCode, got)
	}
	resp = r.request(mainHost, "/rc/api/agents", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("unauthenticated agents api = %d", resp.StatusCode)
	}
	resp.Body.Close()
}
