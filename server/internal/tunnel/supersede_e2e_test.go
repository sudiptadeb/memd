package tunnel

// End-to-end tests of the instance-aware registration rules (rc protocol §8):
// a second agent process holding the same token is refused while the first is
// alive, an explicit takeover displaces the whole incumbent pool with the
// SUPERSEDED close signal and the system settles (no mutual-eviction
// livelock), and a dead incumbent never blocks its own restart.

import (
	"encoding/json"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/xtaci/smux"
)

// rcAgent models termulaa -rc's actual control flow: every tunnel reconnects
// on generic failure with a short backoff, a 409 conflict is terminal, and a
// SUPERSEDED close frame stops the whole agent without retrying.
type rcAgent struct {
	t        *testing.T
	r        *rig
	token    string
	backend  string
	port     string
	instance string
	takeover bool

	stopped  chan struct{}
	stopOnce sync.Once

	mu         sync.Mutex
	conns      []*websocket.Conn
	dials      int
	superseded bool
	refused    bool
}

func startRCAgent(t *testing.T, r *rig, token, backendAddr, backendPort string, tunnels int, takeover bool) *rcAgent {
	t.Helper()
	a := &rcAgent{
		t: t, r: r, token: token, backend: backendAddr, port: backendPort,
		instance: newInstance(t), takeover: takeover,
		stopped: make(chan struct{}),
	}
	for i := 0; i < tunnels; i++ {
		go a.runTunnel(i)
	}
	t.Cleanup(a.stop)
	return a
}

func (a *rcAgent) runTunnel(idx int) {
	for {
		select {
		case <-a.stopped:
			return
		default:
		}
		ws, resp, err := dialTunnel(a.r, a.token, tunnelQuery("rc-agent", a.instance, idx, a.port, a.takeover))
		a.mu.Lock()
		a.dials++
		a.mu.Unlock()
		if err != nil {
			if resp != nil && resp.StatusCode == http.StatusConflict {
				a.mu.Lock()
				a.refused = true
				a.mu.Unlock()
				a.stop()
				return
			}
			select {
			case <-a.stopped:
				return
			case <-time.After(20 * time.Millisecond):
			}
			continue
		}
		var superseded bool
		var supersededMu sync.Mutex
		prev := ws.CloseHandler()
		ws.SetCloseHandler(func(code int, text string) error {
			if code == websocket.ClosePolicyViolation && text == supersededCloseReason {
				supersededMu.Lock()
				superseded = true
				supersededMu.Unlock()
			}
			return prev(code, text)
		})
		a.mu.Lock()
		a.conns = append(a.conns, ws)
		a.mu.Unlock()
		sess, err := smux.Server(newWSConn(ws), muxConfig())
		if err != nil {
			_ = ws.Close()
			continue
		}
		for {
			stream, err := sess.AcceptStream()
			if err != nil {
				break
			}
			go splice(stream, a.backend)
		}
		_ = ws.Close()
		supersededMu.Lock()
		wasSuperseded := superseded
		supersededMu.Unlock()
		if wasSuperseded {
			a.mu.Lock()
			a.superseded = true
			a.mu.Unlock()
			a.stop()
			return
		}
		select {
		case <-a.stopped:
			return
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func (a *rcAgent) stop() {
	a.stopOnce.Do(func() {
		close(a.stopped)
		a.mu.Lock()
		conns := append([]*websocket.Conn(nil), a.conns...)
		a.mu.Unlock()
		for _, ws := range conns {
			_ = ws.UnderlyingConn().Close()
		}
	})
}

func (a *rcAgent) isStopped() bool {
	select {
	case <-a.stopped:
		return true
	default:
		return false
	}
}

func (a *rcAgent) snapshot() (dials int, superseded, refused bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.dials, a.superseded, a.refused
}

// pairViewer runs the view-host pairing flow and returns the viewer cookie.
func pairViewer(t *testing.T, r *rig, token string) *http.Cookie {
	t.Helper()
	resp := r.request(testViewHost, "/?t="+url.QueryEscape(token), nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("pairing status = %d, want 303", resp.StatusCode)
	}
	for _, c := range resp.Cookies() {
		if c.Name == viewerCookieName {
			return c
		}
	}
	t.Fatal("pairing set no cookie")
	return nil
}

// TestTunnelConflictRefusal: while a live agent holds the token, a second
// process (same token, fresh instance, no takeover) is refused with 409 and a
// body naming the incumbent — and the incumbent is not disturbed at all.
func TestTunnelConflictRefusal(t *testing.T) {
	r := newRig(t, testViewHost)
	b := startBackend(t)
	token := r.mintToken()
	startAgent(t, r, token, b.addr, b.port, 2)
	id := TokenAgentID(token)

	ws, resp, err := dialTunnel(r, token, tunnelQuery("second-box", newInstance(t), 0, b.port, false))
	if err == nil {
		ws.Close()
		t.Fatal("second instance's handshake succeeded, want 409 refusal")
	}
	if resp == nil || resp.StatusCode != http.StatusConflict {
		t.Fatalf("second instance refusal = %v, want 409", resp)
	}
	var conflict struct {
		Error         string `json:"error"`
		Label         string `json:"label"`
		Tunnels       int    `json:"tunnels"`
		ConnectedAt   string `json:"connected_at"`
		ConnectedSecs *int64 `json:"connected_secs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&conflict); err != nil {
		t.Fatalf("conflict body decode: %v", err)
	}
	if conflict.Error != "conflict" || conflict.Label != "e2e-box" || conflict.Tunnels != 2 {
		t.Errorf("conflict body = %+v, want error=conflict label=e2e-box tunnels=2", conflict)
	}
	if _, err := time.Parse(time.RFC3339, conflict.ConnectedAt); err != nil {
		t.Errorf("conflict connected_at %q: %v", conflict.ConnectedAt, err)
	}
	if conflict.ConnectedSecs == nil || *conflict.ConnectedSecs < 0 {
		t.Errorf("conflict connected_secs = %v, want >= 0", conflict.ConnectedSecs)
	}

	// The incumbent kept its full pool and still serves traffic.
	if got := r.handler.hub.Tunnels(id); got != 2 {
		t.Errorf("incumbent Tunnels = %d after refusal, want 2 untouched", got)
	}
	cookie := pairViewer(t, r, token)
	hello := r.request(testViewHost, "/hello", func(req *http.Request) { req.AddCookie(cookie) })
	if hello.StatusCode != http.StatusOK {
		t.Errorf("incumbent traffic after refusal = %d, want 200", hello.StatusCode)
	}
	if got := body(t, hello); got != "hello from termulaa via localhost:"+b.port {
		t.Errorf("incumbent proxied body = %q", got)
	}
}

// TestTunnelTakeover: agent B (same token, new instance, explicit takeover)
// displaces agent A entirely. Both model the real reconnect loop, so this is
// the anti-livelock proof: the system must settle at B serving with its full
// pool and A stopped by the SUPERSEDED close — no oscillation, no thrash.
func TestTunnelTakeover(t *testing.T) {
	r := newRig(t, testViewHost)
	b := startBackend(t)
	token := r.mintToken()
	id := TokenAgentID(token)

	agentA := startRCAgent(t, r, token, b.addr, b.port, 3, false)
	waitFor(t, func() bool { return r.handler.hub.Tunnels(id) == 3 }, "agent A pool up")

	agentB := startRCAgent(t, r, token, b.addr, b.port, 2, true)
	waitFor(t, agentA.isStopped, "agent A stopped after takeover")
	waitFor(t, func() bool { return r.handler.hub.Tunnels(id) == 2 }, "agent B pool up alone")

	_, aSuperseded, aRefused := agentA.snapshot()
	if !aSuperseded && !aRefused {
		t.Error("agent A stopped without a terminal signal (superseded/refused)")
	}
	if !aSuperseded {
		t.Error("agent A never saw the SUPERSEDED close frame")
	}

	// Stability: nothing may oscillate once B holds the pool.
	aDials, _, _ := agentA.snapshot()
	bDials, _, _ := agentB.snapshot()
	time.Sleep(500 * time.Millisecond)
	if got := r.handler.hub.Tunnels(id); got != 2 {
		t.Errorf("Tunnels = %d after settling, want a stable 2", got)
	}
	if agentB.isStopped() {
		t.Error("agent B stopped; the takeover winner must stay up")
	}
	aDials2, _, _ := agentA.snapshot()
	bDials2, _, _ := agentB.snapshot()
	if aDials2 != aDials {
		t.Errorf("agent A kept dialing after being superseded (%d -> %d)", aDials, aDials2)
	}
	if bDials2 != bDials {
		t.Errorf("agent B is thrashing: dials %d -> %d during quiet period", bDials, bDials2)
	}

	// Traffic flows through B afterwards.
	cookie := pairViewer(t, r, token)
	hello := r.request(testViewHost, "/hello", func(req *http.Request) { req.AddCookie(cookie) })
	if hello.StatusCode != http.StatusOK {
		t.Errorf("traffic through B = %d, want 200", hello.StatusCode)
	}
	if got := body(t, hello); got != "hello from termulaa via localhost:"+b.port {
		t.Errorf("proxied body through B = %q", got)
	}
}

// TestTunnelDoubleRunRefusedStable: the reported accident — a second
// `termulaa -rc` process started with the same token (no takeover flag) while
// the first is healthy. The second must stop terminally on the 409; the first
// must be completely undisturbed. No mutual eviction, no flapping.
func TestTunnelDoubleRunRefusedStable(t *testing.T) {
	r := newRig(t, testViewHost)
	b := startBackend(t)
	token := r.mintToken()
	id := TokenAgentID(token)

	agentA := startRCAgent(t, r, token, b.addr, b.port, 4, false)
	waitFor(t, func() bool { return r.handler.hub.Tunnels(id) == 4 }, "agent A pool up")
	aDials, _, _ := agentA.snapshot()

	agentB := startRCAgent(t, r, token, b.addr, b.port, 4, false)
	waitFor(t, agentB.isStopped, "agent B stopped after refusal")
	_, bSuperseded, bRefused := agentB.snapshot()
	if !bRefused {
		t.Error("agent B did not observe the 409 conflict")
	}
	if bSuperseded {
		t.Error("agent B was superseded; a plain double-run must be refused, not displaced")
	}

	time.Sleep(300 * time.Millisecond)
	if got := r.handler.hub.Tunnels(id); got != 4 {
		t.Errorf("incumbent Tunnels = %d after double-run, want 4 untouched", got)
	}
	if agentA.isStopped() {
		t.Error("incumbent agent A stopped; a refusal must not disturb it")
	}
	if aDials2, _, _ := agentA.snapshot(); aDials2 != aDials {
		t.Errorf("incumbent agent A reconnected during refusal (%d -> %d dials): eviction livelock", aDials, aDials2)
	}
}

// TestTunnelRestartAfterDeath: kill-and-restart with the same token must
// always work. When the old process's sockets die (SIGKILL, crash, clean
// exit), the server reaps the corpse tunnels and a fresh instance registers
// with no refusal — and near-instantly, not after a keepalive timeout.
func TestTunnelRestartAfterDeath(t *testing.T) {
	r := newRig(t, testViewHost)
	b := startBackend(t)
	token := r.mintToken()
	id := TokenAgentID(token)

	a := startAgent(t, r, token, b.addr, b.port, 2)
	// Hard-kill the process: the OS closes its sockets.
	killed := time.Now()
	a.killTunnel(0)
	a.killTunnel(1)

	// Restart with a fresh instance immediately; retry on 409 to measure how
	// long the server takes to notice the corpse.
	instance := newInstance(t)
	var elapsed time.Duration
	deadline := time.Now().Add(10 * time.Second)
	for {
		ws, resp, err := dialTunnel(r, token, tunnelQuery("restarted-box", instance, 0, b.port, false))
		if err == nil {
			elapsed = time.Since(killed)
			sess, err := smux.Server(newWSConn(ws), muxConfig())
			if err != nil {
				t.Fatalf("restarted agent smux: %v", err)
			}
			go func() {
				for {
					if _, err := sess.AcceptStream(); err != nil {
						return
					}
				}
			}()
			t.Cleanup(func() { _ = ws.Close() })
			break
		}
		if resp == nil || resp.StatusCode != http.StatusConflict {
			t.Fatalf("restart dial failed: %v (resp=%v)", err, resp)
		}
		if time.Now().After(deadline) {
			t.Fatal("restart still refused 10s after the old process died")
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Logf("restart accepted %v after hard kill", elapsed)
	if elapsed > 3*time.Second {
		t.Errorf("restart took %v after hard kill; corpse reaping is too slow (keepalive-bound?)", elapsed)
	}
	waitFor(t, func() bool { return r.handler.hub.Tunnels(id) == 1 }, "restarted pool registered")
}

// TestTunnelLegacyAgentCompat: an agent that predates the instance parameter
// sends none; the server treats "no instance" as its own legacy identity.
// Legacy same-slot re-registration keeps today's replace behavior, and the
// conflict rule applies between the legacy identity and any real instance.
func TestTunnelLegacyAgentCompat(t *testing.T) {
	r := newRig(t, testViewHost)
	b := startBackend(t)
	token := r.mintToken()
	id := TokenAgentID(token)

	serveSmux := func(ws *websocket.Conn) {
		sess, err := smux.Server(newWSConn(ws), muxConfig())
		if err != nil {
			t.Fatalf("smux: %v", err)
		}
		go func() {
			for {
				if _, err := sess.AcceptStream(); err != nil {
					return
				}
			}
		}()
	}

	// A legacy agent registers fine.
	legacy1, resp, err := dialTunnel(r, token, tunnelQuery("legacy-box", "", 0, b.port, false))
	if err != nil {
		t.Fatalf("legacy dial: %v (resp=%v)", err, resp)
	}
	t.Cleanup(func() { _ = legacy1.Close() })
	serveSmux(legacy1)
	waitFor(t, func() bool { return r.handler.hub.Tunnels(id) == 1 }, "legacy registration")

	// A second legacy registration of the same slot replaces it, exactly as
	// before the instance parameter existed.
	legacy2, resp, err := dialTunnel(r, token, tunnelQuery("legacy-box", "", 0, b.port, false))
	if err != nil {
		t.Fatalf("legacy re-dial: %v (resp=%v)", err, resp)
	}
	t.Cleanup(func() { _ = legacy2.Close() })
	serveSmux(legacy2)
	waitFor(t, func() bool { return r.handler.hub.Tunnels(id) == 1 }, "legacy same-slot replacement")

	// A new-style instance against the live legacy pool is a conflict.
	if ws, resp, err := dialTunnel(r, token, tunnelQuery("new-box", newInstance(t), 0, b.port, false)); err == nil {
		ws.Close()
		t.Fatal("new instance registered over a live legacy pool, want 409")
	} else if resp == nil || resp.StatusCode != http.StatusConflict {
		t.Fatalf("new instance vs legacy pool = %v, want 409", resp)
	}

	// Once the legacy agent is dead, the new instance registers freely...
	_ = legacy2.UnderlyingConn().Close()
	instance := newInstance(t)
	waitFor(t, func() bool {
		ws, resp, err := dialTunnel(r, token, tunnelQuery("new-box", instance, 0, b.port, false))
		if err != nil {
			if resp == nil || resp.StatusCode != http.StatusConflict {
				t.Fatalf("new instance dial after legacy death: %v (resp=%v)", err, resp)
			}
			return false
		}
		t.Cleanup(func() { _ = ws.Close() })
		serveSmux(ws)
		return true
	}, "new instance registered after legacy death")

	// ...and a legacy newcomer against the live new-style pool is refused,
	// leaving the pool untouched (a retrying old agent backs off; it can
	// never evict the incumbent).
	if ws, resp, err := dialTunnel(r, token, tunnelQuery("legacy-box", "", 0, b.port, false)); err == nil {
		ws.Close()
		t.Fatal("legacy agent registered over a live new-style pool, want 409")
	} else if resp == nil || resp.StatusCode != http.StatusConflict {
		t.Fatalf("legacy vs new-style pool = %v, want 409", resp)
	}
	if got := r.handler.hub.Tunnels(id); got != 1 {
		t.Errorf("Tunnels = %d after legacy refusal, want 1 untouched", got)
	}
}
