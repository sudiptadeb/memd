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

	// testMemdCSP stands in for memd's global CSP (no 'unsafe-inline' — it
	// would break termulaa's UI if it reached proxied responses).
	testMemdCSP = "default-src 'self'; script-src 'self' 'unsafe-eval'"

	// testTermulaaCSP is the terminal's own policy as termulaa sends it on
	// HTML pages; the proxy must deliver it untouched.
	testTermulaaCSP = "default-src 'self'; script-src 'self' 'unsafe-eval' 'unsafe-inline'"

	// testUserHeader lets a request impersonate a different logged-in memd
	// user in the rig's auth stub.
	testUserHeader = "X-Test-User"
)

type rig struct {
	t       *testing.T
	handler *Handler
	server  *httptest.Server
	key     []byte
	authed  atomic.Bool
}

// newRig assembles the tunnel handler exactly as serve.go does: the viewer
// split sits OUTSIDE the security-header middleware, which stamps memd's CSP
// on everything that flows through the normal mux.
func newRig(t *testing.T, viewHost string) *rig {
	t.Helper()
	r := &rig{t: t, key: TokenKey("e2e-secret", "")}
	r.authed.Store(true)
	auth := func(w http.ResponseWriter, req *http.Request) (User, bool) {
		if !r.authed.Load() {
			return User{}, false
		}
		if uid := req.Header.Get(testUserHeader); uid != "" {
			return User{ID: uid, Name: "someone-else"}, true
		}
		return User{ID: testUserID, Name: "alice"}, true
	}
	r.handler = New(r.key, viewHost, auth)
	mux := http.NewServeMux()
	r.handler.Mount(mux)
	// Stand-in for memd's existing routes: they must stay reachable on the
	// main host and must NOT be reachable through the viewer surface.
	mux.HandleFunc("/api/tabs", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("memd-own-api"))
	})
	withHeaders := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Security-Policy", testMemdCSP)
		w.Header().Set("X-Frame-Options", "DENY")
		mux.ServeHTTP(w, req)
	})
	r.server = httptest.NewServer(r.handler.SplitViewer(withHeaders))
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
		// termulaa sends its own CSP on HTML pages; the proxy must deliver
		// it to the viewer untouched.
		w.Header().Set("Content-Security-Policy", testTermulaaCSP)
		_, _ = fmt.Fprintf(w, "hello from termulaa via %s", r.Host)
	}))
	mux.HandleFunc("/api/tabs", guard(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("termulaa-tabs"))
	}))
	mux.HandleFunc("/echo-req", guard(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"path":    r.URL.Path,
			"prefix":  r.Header.Get("X-Forwarded-Prefix"),
			"cookies": len(r.Cookies()),
		})
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
	r := newRig(t, testViewHost)
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
	if got := resp.Header.Values("Content-Security-Policy"); len(got) != 1 || got[0] != testTermulaaCSP {
		t.Errorf("proxied CSP = %q, want only termulaa's own", got)
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
	r := newRig(t, testViewHost)
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
	r := newRig(t, testViewHost)
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
	r := newRig(t, testViewHost)
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

// TestManagementPage: the management UI lives in the dashboard SPA; /rc stays
// a stable entry point (the termulaa CLI prints `<server>/rc` on token expiry,
// and the rc protocol spec says a rendezvous SHOULD serve a pairing page
// there) and redirects into the SPA's termulaa section.
func TestManagementPage(t *testing.T) {
	r := newRig(t, testViewHost)
	mainHost := r.server.Listener.Addr().String()

	resp := r.request(mainHost, "/rc", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("/rc status = %d, want 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != dashboardRCPath {
		t.Errorf("/rc Location = %q, want %q", loc, dashboardRCPath)
	}

	// The redirect itself needs no login — the SPA handles sign-in.
	r.authed.Store(false)
	resp = r.request(mainHost, "/rc", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Errorf("unauthenticated /rc status = %d, want 302", resp.StatusCode)
	}

	// The JSON APIs behind the SPA page stay authenticated.
	resp = r.request(mainHost, "/rc/api/agents", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("unauthenticated agents api = %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// --- path mode -----------------------------------------------------------

// TestEndToEndPathMode drives the same-host viewer: the terminal lives under
// /rc/t/<agentID>/ on memd's own host, the viewer's credential is memd's
// login session, and only the agent's owner gets through.
func TestEndToEndPathMode(t *testing.T) {
	r := newRig(t, "")
	b := startBackend(t)
	token := r.mintToken()
	startAgent(t, r, token, b.addr, b.port, 2)

	mainHost := r.server.Listener.Addr().String()
	id := string(TokenAgentID(token))
	base := viewerPathPrefix + id

	// Missing trailing slash: canonical redirect so the page's <base>
	// resolves correctly.
	resp := r.request(mainHost, base, nil)
	if resp.StatusCode != http.StatusMovedPermanently || resp.Header.Get("Location") != base+"/" {
		t.Errorf("no-slash redirect = %d %q, want 301 to %s/", resp.StatusCode, resp.Header.Get("Location"), base)
	}
	resp.Body.Close()

	// Proxied HTTP: stripped path, loopback Host, and termulaa's own CSP —
	// not memd's, which would break the terminal UI.
	resp = r.request(mainHost, base+"/hello", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("proxied GET status = %d", resp.StatusCode)
	}
	if got := resp.Header.Values("Content-Security-Policy"); len(got) != 1 || got[0] != testTermulaaCSP {
		t.Errorf("proxied CSP = %q, want only termulaa's own", got)
	}
	if got := body(t, resp); got != "hello from termulaa via localhost:"+b.port {
		t.Errorf("proxied GET body = %q", got)
	}

	// The backend sees the STRIPPED path, the X-Forwarded-Prefix for that
	// agent, and none of memd's cookies — the login session must never reach
	// the agent's machine. The hostile same-name Origin is rewritten to
	// loopback by the Director (asserted inside the backend's guard).
	resp = r.request(mainHost, base+"/echo-req", func(req *http.Request) {
		req.AddCookie(&http.Cookie{Name: "memd_session", Value: "secret-login"})
		req.Header.Set("Origin", "http://"+mainHost)
		req.Header.Set("X-Forwarded-Prefix", "/attacker/injected")
	})
	var echo struct {
		Path    string `json:"path"`
		Prefix  string `json:"prefix"`
		Cookies int    `json:"cookies"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&echo); err != nil {
		t.Fatalf("echo-req decode: %v", err)
	}
	resp.Body.Close()
	if echo.Path != "/echo-req" {
		t.Errorf("backend path = %q, want the prefix stripped", echo.Path)
	}
	if echo.Prefix != base {
		t.Errorf("backend X-Forwarded-Prefix = %q, want %q", echo.Prefix, base)
	}
	if echo.Cookies != 0 {
		t.Errorf("backend saw %d cookies, want 0 (memd session must not leak)", echo.Cookies)
	}

	// WebSocket upgrade through the path proxy, with the browser's real
	// same-origin Origin header.
	dialer := websocket.Dialer{NetDial: func(network, addr string) (net.Conn, error) {
		return net.Dial("tcp", r.server.Listener.Addr().String())
	}}
	header := http.Header{"Origin": {"http://" + mainHost}}
	ws, resp2, err := dialer.Dial("ws://"+mainHost+base+"/ws/echo", header)
	if err != nil {
		t.Fatalf("viewer websocket dial: %v (resp=%v)", err, resp2)
	}
	if err := ws.WriteMessage(websocket.TextMessage, []byte("ping over path tunnel")); err != nil {
		t.Fatalf("ws write: %v", err)
	}
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("ws read: %v", err)
	}
	if string(msg) != "ping over path tunnel" {
		t.Errorf("echo = %q", msg)
	}
	ws.Close()

	// Cross-origin browser requests must never reach the shell: termulaa's
	// own Origin check is neutralized by the loopback rewrite, so memd
	// enforces same-origin before proxying.
	resp = r.request(mainHost, base+"/hello", func(req *http.Request) {
		req.Header.Set("Origin", "https://evil.example")
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-origin GET = %d, want 403", resp.StatusCode)
	}
	resp.Body.Close()

	// A different logged-in user does not reach this agent — and cannot even
	// confirm it exists.
	resp = r.request(mainHost, base+"/hello", func(req *http.Request) {
		req.Header.Set(testUserHeader, "user-2")
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("other user's GET = %d, want 404", resp.StatusCode)
	}
	resp.Body.Close()

	// No memd login, no terminal.
	r.authed.Store(false)
	resp = r.request(mainHost, base+"/hello", nil)
	got := body(t, resp)
	if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(got, "Sign in") {
		t.Errorf("unauthenticated GET = %d %q, want the 401 sign-in page", resp.StatusCode, got)
	}
	r.authed.Store(true)

	// Malformed agent ids never reach the proxy.
	for _, p := range []string{"/rc/t/zzzz/", "/rc/t/" + strings.ToUpper(id) + "/", "/rc/t//hello"} {
		resp = r.request(mainHost, p, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", p, resp.StatusCode)
		}
		resp.Body.Close()
	}

	// A valid id whose agent never connected: honest offline page.
	otherID := string(TokenAgentID(r.mintToken()))
	resp = r.request(mainHost, viewerPathPrefix+otherID+"/", nil)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("offline agent = %d, want 503", resp.StatusCode)
	}
	resp.Body.Close()

	// memd's own routes are untouched and still carry memd's headers.
	resp = r.request(mainHost, "/api/tabs", nil)
	if got := resp.Header.Get("Content-Security-Policy"); got != testMemdCSP {
		t.Errorf("memd route CSP = %q, want memd's own", got)
	}
	if got := body(t, resp); got != "memd-own-api" {
		t.Errorf("main-host /api/tabs = %q, want memd's own handler", got)
	}

	// The agents API links each agent straight to its terminal.
	resp = r.request(mainHost, "/rc/api/agents", nil)
	var status struct {
		Agents []struct {
			URL string `json:"url"`
		} `json:"agents"`
		ViewHost string `json:"view_host"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatalf("agents api decode: %v", err)
	}
	resp.Body.Close()
	if len(status.Agents) != 1 || status.Agents[0].URL != base+"/" {
		t.Errorf("agents api = %+v, want url %s/", status, base)
	}
	if status.ViewHost != "" {
		t.Errorf("view_host = %q, want empty in path mode", status.ViewHost)
	}
}

// TestMintOpenURLPathMode: in path mode the mint response carries the
// terminal's future URL, derived from the token.
func TestMintOpenURLPathMode(t *testing.T) {
	r := newRig(t, "")
	req, err := http.NewRequest(http.MethodPost, r.server.URL+"/rc/api/tokens",
		strings.NewReader(`{"label":"x","ttl":1}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("mint request: %v", err)
	}
	var minted struct {
		Token   string `json:"token"`
		OpenURL string `json:"open_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&minted); err != nil {
		t.Fatalf("mint decode: %v", err)
	}
	resp.Body.Close()
	if want := viewerPathPrefix + string(TokenAgentID(minted.Token)) + "/"; minted.OpenURL != want {
		t.Errorf("open_url = %q, want %q", minted.OpenURL, want)
	}
}

// TestPathViewerDisabledInHostMode: with a view host configured, /rc/t/ paths
// on the main host fall through to memd's normal stack — the same-origin
// viewer must not undermine host mode's origin isolation.
func TestPathViewerDisabledInHostMode(t *testing.T) {
	r := newRig(t, testViewHost)
	mainHost := r.server.Listener.Addr().String()
	resp := r.request(mainHost, viewerPathPrefix+strings.Repeat("ab", 32)+"/", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("host-mode /rc/t/ = %d, want memd's 404", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Security-Policy"); got != testMemdCSP {
		t.Errorf("host-mode /rc/t/ CSP = %q, want memd's — proof it went through the normal stack", got)
	}
	resp.Body.Close()
}

// TestFromEnv: the feature is on by default with zero configuration (a token
// key is derivable from MEMD_SESSION_SECRET), MEMD_RC=0 is an emergency kill
// switch that forces it off, and without any token secret the feature is
// honestly unavailable. The view host selects host mode.
func TestFromEnv(t *testing.T) {
	auth := func(http.ResponseWriter, *http.Request) (User, bool) { return User{}, false }
	setenv := func(t *testing.T, rc, viewHost, secret string) {
		t.Setenv("MEMD_RC", rc)
		t.Setenv("MEMD_RC_VIEW_HOST", viewHost)
		t.Setenv("MEMD_RC_TOKEN_SECRET", "")
		t.Setenv("MEMD_SESSION_SECRET", secret)
	}

	setenv(t, "", "", "s3cret")
	if h := FromEnv(auth); h == nil || h.ViewHost() != "" || !h.Enabled() {
		t.Errorf("zero config: want enabled path-mode handler (default on), got %v", h)
	}
	if KillSwitchActive() {
		t.Error("MEMD_RC unset: kill switch must be inactive")
	}
	for _, off := range []string{"0", "false", "off", "no", " OFF "} {
		setenv(t, off, "", "s3cret")
		if FromEnv(auth) != nil {
			t.Errorf("MEMD_RC=%q: want nil (kill switch)", off)
		}
		if !KillSwitchActive() {
			t.Errorf("MEMD_RC=%q: want KillSwitchActive", off)
		}
	}
	// The kill switch wins over everything, host mode included.
	setenv(t, "0", "term.example", "s3cret")
	if FromEnv(auth) != nil {
		t.Error("kill switch + view host: want nil (kill switch wins)")
	}
	// Legacy MEMD_RC=1 is harmless: the feature is on either way.
	setenv(t, "1", "", "s3cret")
	if h := FromEnv(auth); h == nil || h.ViewHost() != "" {
		t.Errorf("MEMD_RC=1: want path-mode handler, got %v", h)
	}
	setenv(t, "", "term.example", "s3cret")
	if h := FromEnv(auth); h == nil || h.ViewHost() != "term.example" {
		t.Errorf("view host set: want host-mode handler, got %v", h)
	}
	setenv(t, "", "", "")
	if FromEnv(auth) != nil {
		t.Error("no token secret at all: want nil (feature unavailable)")
	}
	if KeyAvailable() {
		t.Error("no secrets: KeyAvailable must be false")
	}
	t.Setenv("MEMD_RC_TOKEN_SECRET", "dedicated")
	if h := FromEnv(auth); h == nil {
		t.Error("dedicated token secret without session secret: want handler")
	}
	if !KeyAvailable() {
		t.Error("dedicated token secret: KeyAvailable must be true")
	}
}

// TestRuntimeDisablePathMode: flipping the super-admin toggle off must take
// effect immediately — live tunnels are closed, /rc* stops serving, and the
// viewer path falls through to memd's normal stack — and flipping it back on
// serves again with no restart: a retrying agent simply reconnects.
func TestRuntimeDisablePathMode(t *testing.T) {
	r := newRig(t, "")
	b := startBackend(t)
	token := r.mintToken()
	startAgent(t, r, token, b.addr, b.port, 2)

	mainHost := r.server.Listener.Addr().String()
	id := TokenAgentID(token)
	base := viewerPathPrefix + string(id)

	// Sanity: live before the flip.
	resp := r.request(mainHost, base+"/hello", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pre-disable proxied GET = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()

	r.handler.SetEnabled(false)

	// The registered tunnels are dropped from the hub at once (CloseAll), and
	// the agent-side goroutines observe the close and release themselves.
	waitFor(t, func() bool { return r.handler.hub.Tunnels(id) == 0 }, "tunnels closed on disable")

	// The viewer path no longer proxies: it falls through to memd's normal
	// stack — proven by memd's CSP on the 404 — never to the shell.
	resp = r.request(mainHost, base+"/hello", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("disabled viewer GET = %d, want 404", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Security-Policy"); got != testMemdCSP {
		t.Errorf("disabled viewer CSP = %q, want memd's (normal stack)", got)
	}
	resp.Body.Close()

	// The management surface and pairing entry point stop serving too.
	for _, path := range []string{"/rc", "/rc/api/agents"} {
		resp = r.request(mainHost, path, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("disabled GET %s = %d, want 404", path, resp.StatusCode)
		}
		resp.Body.Close()
	}
	resp = r.request(mainHost, "/rc/api/tokens", func(req *http.Request) { req.Method = http.MethodPost })
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("disabled POST /rc/api/tokens = %d, want 404", resp.StatusCode)
	}
	resp.Body.Close()

	// A reconnecting agent is refused at the handshake.
	wsBase := "ws" + strings.TrimPrefix(r.server.URL, "http")
	header := http.Header{"Authorization": {"Bearer " + token}}
	if _, dialResp, err := websocket.DefaultDialer.Dial(wsBase+"/rc/tunnel?session=0&port="+b.port, header); err == nil {
		t.Error("disabled agent dial succeeded, want refusal")
	} else if dialResp == nil || dialResp.StatusCode != http.StatusNotFound {
		t.Errorf("disabled agent dial resp = %v, want 404", dialResp)
	}

	// Re-enable: no restart needed in either direction. The agent's retry
	// loop reconnects (modeled by a fresh startAgent) and the viewer serves.
	r.handler.SetEnabled(true)
	startAgent(t, r, token, b.addr, b.port, 1)
	resp = r.request(mainHost, base+"/hello", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("re-enabled proxied GET = %d, want 200", resp.StatusCode)
	}
	if got := body(t, resp); got != "hello from termulaa via localhost:"+b.port {
		t.Errorf("re-enabled proxied body = %q", got)
	}
}

// TestRuntimeDisableHostMode: with the runtime switch off, view-host traffic
// is not intercepted — it flows through memd's normal stack like any other
// host — so the viewer surface is gone, not merely erroring.
func TestRuntimeDisableHostMode(t *testing.T) {
	r := newRig(t, testViewHost)
	resp := r.request(testViewHost, "/", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("enabled unpaired view host = %d, want 401 pair page", resp.StatusCode)
	}
	resp.Body.Close()

	r.handler.SetEnabled(false)
	resp = r.request(testViewHost, "/", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("disabled view host = %d, want memd's 404", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Security-Policy"); got != testMemdCSP {
		t.Errorf("disabled view host CSP = %q, want memd's (normal stack)", got)
	}
	resp.Body.Close()

	r.handler.SetEnabled(true)
	resp = r.request(testViewHost, "/", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("re-enabled unpaired view host = %d, want 401 pair page", resp.StatusCode)
	}
	resp.Body.Close()
}
