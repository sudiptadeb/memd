package tunnel

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sudiptadeb/memd/server/internal/logs"
	"github.com/xtaci/smux"
)

// viewerCookieName holds the pairing token on the view host.
const viewerCookieName = "termulaa_rc"

// User is a logged-in memd user, as resolved by the host application's own
// session auth.
type User struct {
	ID   string
	Name string
}

// UserAuth resolves the memd login session on a request. The tunnel package
// deliberately has no auth of its own for the /rc page and mint endpoint —
// it reuses memd's existing session handling through this hook.
type UserAuth func(w http.ResponseWriter, r *http.Request) (User, bool)

// Handler is the HTTP surface of the reverse tunnel: the /rc management page
// and its small API, the agent's /rc/tunnel WebSocket endpoint, and the
// viewer proxy. The viewer runs in exactly one of two modes:
//
//   - path mode (viewHost == ""): the terminal is served on memd's own host
//     under /rc/t/<agentID>/, gated by memd's login session plus agent
//     ownership. No extra DNS or TLS, but the terminal shares memd's browser
//     origin.
//   - host mode (viewHost != ""): the terminal is served on a dedicated
//     hostname, gated by the token pairing cookie. Browser-origin isolation
//     between memd and the remote shell, at the cost of a DNS record and a
//     certificate.
type Handler struct {
	hub      *Hub
	key      []byte
	viewHost string
	auth     UserAuth
	proxy    *httputil.ReverseProxy
	upgrader websocket.Upgrader
	// enabled is the runtime switch a super admin flips from the admin
	// console. While off, every mounted /rc* route answers 404, the viewer
	// split passes all traffic through untouched, and the hub holds no
	// tunnels — turning the feature off is indistinguishable from it never
	// having been mounted, minus the 404-vs-SPA-fallthrough detail on /rc*.
	enabled atomic.Bool
}

// New builds a tunnel handler, enabled. key signs tokens, viewHost selects
// host mode when non-empty (path mode otherwise), and auth resolves memd's
// login session for the management surface and the path-mode viewer.
func New(key []byte, viewHost string, auth UserAuth) *Handler {
	hub := NewHub()
	h := &Handler{
		hub:      hub,
		key:      key,
		viewHost: viewHost,
		auth:     auth,
		proxy:    newProxy(hub),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  32 * 1024,
			WriteBufferSize: 32 * 1024,
			// The endpoint is bearer-token authenticated and dialed by a
			// non-browser agent; browser origin checks do not apply.
			CheckOrigin: func(*http.Request) bool { return true },
		},
	}
	h.enabled.Store(true)
	return h
}

// FromEnv builds the handler from the environment. The rc feature is on by
// default: the handler is returned (enabled; the caller may apply a persisted
// super-admin setting via SetEnabled) whenever a token key is available — from
// MEMD_RC_TOKEN_SECRET, or derived from MEMD_SESSION_SECRET. It returns nil
// in exactly two cases: the MEMD_RC=0 kill switch is set (an emergency
// escape hatch that forces the feature off regardless of the stored setting),
// or no token key can be derived, in which case the feature reports itself
// unavailable rather than pretending to work.
func FromEnv(auth UserAuth) *Handler {
	if KillSwitchActive() {
		logs.Info("rc: MEMD_RC kill switch is set; reverse tunnel forced off")
		return nil
	}
	viewHost := strings.TrimSpace(os.Getenv("MEMD_RC_VIEW_HOST"))
	key := TokenKey(os.Getenv("MEMD_RC_TOKEN_SECRET"), os.Getenv("MEMD_SESSION_SECRET"))
	if key == nil {
		logs.Warn("rc: no token secret is available (set MEMD_RC_TOKEN_SECRET or MEMD_SESSION_SECRET); reverse tunnel unavailable")
		return nil
	}
	return New(key, viewHost, auth)
}

// KillSwitchActive reports whether MEMD_RC is explicitly set to an off value
// (0/false/off/no). This is the emergency kill switch: it forces the rc
// feature off no matter what the persisted super-admin setting says. Unset —
// the normal state — means the feature follows that setting (default on).
func KillSwitchActive() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("MEMD_RC"))) {
	case "0", "false", "off", "no":
		return true
	}
	return false
}

// KeyAvailable reports whether a token-signing key can be derived from the
// environment (see TokenKey). Without one the rc feature cannot run at all.
func KeyAvailable() bool {
	return TokenKey(os.Getenv("MEMD_RC_TOKEN_SECRET"), os.Getenv("MEMD_SESSION_SECRET")) != nil
}

// SetEnabled flips the runtime switch. Disabling takes effect immediately:
// every tunnel registered in the hub is closed (agents keep retrying with
// backoff and are refused at the HTTP layer), and the /rc* routes and viewer
// surface stop serving. Re-enabling requires no restart either — retrying
// agents are accepted again on their next attempt.
func (h *Handler) SetEnabled(on bool) {
	was := h.enabled.Swap(on)
	if was && !on {
		h.hub.CloseAll()
	}
}

// Enabled reports the current runtime state.
func (h *Handler) Enabled() bool { return h.enabled.Load() }

// ViewHost is the dedicated view hostname; empty in path mode.
func (h *Handler) ViewHost() string { return h.viewHost }

// Mount registers the management and agent endpoints on memd's main mux
// (i.e. on the normal memd host, not the view host).
func (h *Handler) Mount(mux *http.ServeMux) {
	mux.HandleFunc("/rc", h.ifEnabled(servePageRedirect))
	mux.HandleFunc("/rc/api/tokens", h.ifEnabled(h.mintAPI))
	mux.HandleFunc("/rc/api/agents", h.ifEnabled(h.agentsAPI))
	mux.HandleFunc("/rc/tunnel", h.ifEnabled(h.serveTunnel))
}

// ifEnabled gates a mounted route on the runtime switch: while the feature is
// disabled every /rc* route answers 404, exactly as if it were never mounted.
// An agent whose tunnel dial hits this 404 simply stays in its retry loop and
// reconnects on its own once the feature is re-enabled.
func (h *Handler) ifEnabled(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !h.enabled.Load() {
			http.NotFound(w, r)
			return
		}
		next(w, r)
	}
}

// viewerPathPrefix is where path mode serves proxied terminals:
// /rc/t/<agentID>/... on memd's own host.
const viewerPathPrefix = "/rc/t/"

// SplitViewer routes viewer traffic off memd's normal stack — host mode by
// Host header, path mode by the /rc/t/ path prefix. It sits OUTSIDE memd's
// security-header middleware and body cap on purpose: the proxied terminal
// carries termulaa's own security headers (its UI needs a CSP memd's global
// one forbids), and memd's headers must not be weakened anywhere else.
// Everything that is not viewer traffic falls through to next completely
// unchanged.
func (h *Handler) SplitViewer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.enabled.Load() {
			// Feature disabled at runtime: no viewer surface exists. All
			// traffic — view-host and /rc/t/ alike — flows through memd's
			// normal stack, as it would with the feature never mounted.
			next.ServeHTTP(w, r)
			return
		}
		if h.viewHost != "" {
			if hostMatches(r.Host, h.viewHost) {
				h.serveViewer(w, r)
				return
			}
		} else if strings.HasPrefix(r.URL.Path, viewerPathPrefix) {
			h.servePathViewer(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// hostMatches compares request and configured hosts case-insensitively,
// ignoring any port on either side.
func hostMatches(requestHost, viewHost string) bool {
	return strings.EqualFold(hostOnly(requestHost), hostOnly(viewHost))
}

func hostOnly(hostport string) string {
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		return host
	}
	return hostport
}

// --- Agent endpoint -----------------------------------------------------

// The SUPERSEDED close signal (rc protocol §8): sent to every tunnel of an
// agent instance displaced by an explicit takeover, so the displaced agent can
// tell "my token was taken over — stop" from an ordinary network drop it
// should retry. The reason string is machine-readable and MUST NOT change.
const (
	supersededCloseCode   = websocket.ClosePolicyViolation // 1008
	supersededCloseReason = "superseded"
)

// instancePattern bounds the agent-chosen instance id (rc protocol §2).
// Empty is a legacy agent that predates the parameter.
var instancePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{0,64}$`)

// serveTunnel is the agent's outbound WebSocket endpoint. After the bearer
// token checks out, the connection becomes one smux session — one tunnel in
// the agent's pool — with memd as the smux client (memd initiates work when
// a browser arrives; the agent accepts streams and splices them locally).
func (h *Handler) serveTunnel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	token, ok := bearerToken(r)
	if !ok {
		http.Error(w, "missing bearer token", http.StatusUnauthorized)
		return
	}
	claims, err := ParseToken(h.key, token)
	if err != nil {
		logs.Warn("rc: rejected tunnel connection: invalid token")
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}
	q := r.URL.Query()
	port, err := strconv.Atoi(q.Get("port"))
	if err != nil || port < 1 || port > 65535 {
		http.Error(w, "invalid port", http.StatusBadRequest)
		return
	}
	label := strings.TrimSpace(q.Get("agent"))
	if label == "" {
		label = claims.Label
	}
	// The session index identifies this tunnel's slot in the agent's pool; a
	// re-registration of the same slot replaces its stale predecessor.
	sessionIdx, err := strconv.Atoi(q.Get("session"))
	if err != nil || sessionIdx < 0 {
		sessionIdx = 0
	}
	instance := q.Get("instance")
	if !instancePattern.MatchString(instance) {
		http.Error(w, "invalid instance", http.StatusBadRequest)
		return
	}
	takeover := q.Get("takeover") == "1"
	id := TokenAgentID(token)

	// Conflict check (rc protocol §8): a live pool held by a DIFFERENT agent
	// instance refuses the handshake unless the newcomer carries the explicit
	// takeover marker. Incumbent reaps dead tunnels first, so a restarted
	// agent (whose predecessor's sockets are gone) is never refused.
	if inc, held := h.hub.Incumbent(id, instance); held && !takeover {
		logs.InfoUser(claims.UserID,
			"rc: agent %s (%q) tunnel %d refused: token held by another instance (%q, %d tunnels, up %s)",
			shortID(id), label, sessionIdx, inc.Info.Label, inc.Tunnels,
			time.Since(inc.ConnectedAt).Round(time.Second))
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":          "conflict",
			"label":          inc.Info.Label,
			"tunnels":        inc.Tunnels,
			"connected_at":   inc.ConnectedAt.UTC().Format(time.RFC3339),
			"connected_secs": int64(time.Since(inc.ConnectedAt).Seconds()),
		})
		return
	}

	ws, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade has already written the HTTP error.
		return
	}
	conn := newWSConn(ws)
	carrier := newNotifyConn(conn)
	sess, err := smux.Client(carrier, muxConfig())
	if err != nil {
		logs.Warn("rc: agent %s smux setup failed: %v", shortID(id), err)
		_ = conn.Close()
		return
	}
	// A displacing takeover must be distinguishable from a network drop, or
	// the loser retries forever (a close of the smux carrier alone surfaces
	// as a generic 1006). Send the SUPERSEDED close frame first; the session
	// close that follows wakes this handler's select for teardown.
	supersede := func() {
		_ = ws.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(supersededCloseCode, supersededCloseReason),
			time.Now().Add(2*time.Second))
	}
	release := h.hub.Register(id, instance, sessionIdx, AgentInfo{UserID: claims.UserID, Label: label, Port: port}, sess, supersede)
	if takeover {
		logs.InfoUser(claims.UserID, "rc: agent %s (%q) instance %s took over the token", shortID(id), label, shortInstance(instance))
	}
	logs.InfoUser(claims.UserID, "rc: agent %s (%q) tunnel %d connected (%d tunnels up)",
		shortID(id), label, sessionIdx, h.hub.Tunnels(id))
	// Close the window between ifEnabled's check and Register: if the feature
	// was disabled in that gap (SetEnabled's CloseAll may have run before this
	// registration landed), drop the tunnel now so nothing outlives a disable.
	if !h.enabled.Load() {
		_ = sess.Close()
	}

	// A tunnel does not outlive its token: close it at expiry so the agent's
	// reconnect hits the handshake check and is refused (rc protocol §8 —
	// tokens are re-checked on every reconnect, never trusted from a previous
	// acceptance).
	expiryTimer := time.AfterFunc(time.Until(claims.Expiry()), func() { _ = sess.Close() })
	defer expiryTimer.Stop()

	// Hold the handler until the session dies — either smux notices (its
	// keepalive detects silent stalls) or the carrier fails hard, which
	// notifyConn reports immediately. Then drop the tunnel from the pool at
	// once so pool state never lags reality.
	select {
	case <-sess.CloseChan():
	case <-carrier.dead:
	}
	_ = sess.Close()
	release()
	_ = conn.Close()
	logs.InfoUser(claims.UserID, "rc: agent %s (%q) tunnel %d disconnected (%d tunnels up)",
		shortID(id), label, sessionIdx, h.hub.Tunnels(id))
}

func bearerToken(r *http.Request) (string, bool) {
	const prefix = "Bearer "
	auth := r.Header.Get("Authorization")
	if len(auth) <= len(prefix) || !strings.EqualFold(auth[:len(prefix)], prefix) {
		return "", false
	}
	return strings.TrimSpace(auth[len(prefix):]), true
}

// --- Viewer (dedicated host) --------------------------------------------

// serveViewer handles every request addressed to the view host. Pairing puts
// the token into a cookie so it leaves the URL bar; from then on each request
// is validated and proxied down an smux stream to the agent.
func (h *Handler) serveViewer(w http.ResponseWriter, r *http.Request) {
	// GET /?t=<token>: validate, set the cookie, then redirect so the token
	// disappears from the address bar and referrers.
	if t := r.URL.Query().Get("t"); t != "" && r.URL.Path == "/" {
		claims, err := ParseToken(h.key, t)
		if err != nil {
			servePairPage(w, http.StatusUnauthorized)
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     viewerCookieName,
			Value:    t,
			Path:     "/",
			Expires:  claims.Expiry(),
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			Secure:   isHTTPS(r),
		})
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	cookie, err := r.Cookie(viewerCookieName)
	if err != nil || cookie.Value == "" {
		servePairPage(w, http.StatusUnauthorized)
		return
	}
	if _, err := ParseToken(h.key, cookie.Value); err != nil {
		servePairPage(w, http.StatusUnauthorized)
		return
	}
	id := TokenAgentID(cookie.Value)
	if _, ok := h.hub.Lookup(id); !ok {
		serveAgentOffline(w)
		return
	}
	h.proxy.ServeHTTP(w, r.WithContext(withViewer(r.Context(), id, "")))
}

// --- Viewer (path mode, memd's own host) --------------------------------

var agentIDPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// servePathViewer serves /rc/t/<agentID>/... — the terminal under a path on
// memd's own host. The viewer's credential is memd's login session; the
// request is proxied only when the logged-in user owns the agent (the tunnel
// token's user id, recorded at agent registration). The /rc/t/<agentID>
// prefix is stripped before proxying and handed to termulaa as
// X-Forwarded-Prefix so its pages render under that base path.
func (h *Handler) servePathViewer(w http.ResponseWriter, r *http.Request) {
	idStr, sub, hasSlash := strings.Cut(strings.TrimPrefix(r.URL.Path, viewerPathPrefix), "/")
	if !agentIDPattern.MatchString(idStr) {
		http.NotFound(w, r)
		return
	}
	if !hasSlash {
		// Canonical form carries the trailing slash so the page's <base>
		// resolves relative URLs under the prefix.
		u := *r.URL
		u.Path += "/"
		http.Redirect(w, r, u.String(), http.StatusMovedPermanently)
		return
	}
	user, ok := h.auth(w, r)
	if !ok {
		serveSignInPage(w)
		return
	}
	// The terminal shares memd's origin, so termulaa's own Origin allowlist is
	// neutralized by the loopback rewrite below. Enforce same-origin here:
	// a browser request from any other origin must not reach the shell.
	if origin := r.Header.Get("Origin"); origin != "" && !sameOrigin(origin, r) {
		http.Error(w, "cross-origin request refused", http.StatusForbidden)
		return
	}
	id := AgentID(idStr)
	info, connected := h.hub.Lookup(id)
	if connected && info.UserID != user.ID {
		// Not this user's agent. 404, not 403 — do not confirm existence.
		http.NotFound(w, r)
		return
	}
	if !connected {
		serveAgentOffline(w)
		return
	}
	prefix := viewerPathPrefix + idStr
	r2 := r.Clone(withViewer(r.Context(), id, prefix))
	r2.URL.Path = "/" + sub
	if r2.URL.RawPath != "" {
		r2.URL.RawPath = strings.TrimPrefix(r2.URL.RawPath, prefix)
	}
	h.proxy.ServeHTTP(w, r2)
}

// sameOrigin reports whether a browser-sent Origin header names this
// deployment's own origin, mirroring isHTTPS's view of the scheme.
func sameOrigin(origin string, r *http.Request) bool {
	scheme := "http"
	if isHTTPS(r) {
		scheme = "https"
	}
	return strings.EqualFold(origin, scheme+"://"+r.Host)
}

// isHTTPS mirrors memd's session-cookie logic: direct TLS, or the
// X-Forwarded-Proto set by the fronting nginx.
func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// --- Management page + API (memd host, memd login) ----------------------

// dashboardRCPath is the SPA's termulaa section (hash routing, so the path
// never reaches the Go router).
const dashboardRCPath = "/#/termulaa"

// servePageRedirect keeps /rc a stable entry point — the termulaa CLI prints
// `<server>/rc` on token expiry and the rc protocol spec says a rendezvous
// SHOULD serve a pairing page there — but the dashboard SPA now owns the
// management UI, so /rc simply redirects into it. No auth here: the SPA
// handles sign-in itself.
func servePageRedirect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	http.Redirect(w, r, dashboardRCPath, http.StatusFound)
}

// mintAPI mints one token for the logged-in user. Body:
//
//	{"label": "my laptop", "ttl": 30}
//
// ttl is in days; absent or zero selects the 30-day default, and anything
// above the cap (36500 days, i.e. effectively never) is clamped.
func (h *Handler) mintAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	user, ok := h.auth(w, r)
	if !ok {
		jsonError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	var body struct {
		Label string `json:"label"`
		TTL   int    `json:"ttl"` // days
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.TTL < 0 {
		jsonError(w, http.StatusBadRequest, "ttl must be positive")
		return
	}
	label := strings.TrimSpace(body.Label)
	if len(label) > 64 {
		label = label[:64]
	}
	token, expires, err := MintToken(h.key, user.ID, label, time.Duration(body.TTL)*24*time.Hour)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "mint failed")
		return
	}
	logs.InfoUser(user.ID, "rc: minted tunnel token for agent %s (%q, expires %s)",
		shortID(TokenAgentID(token)), label, expires.UTC().Format(time.RFC3339))
	resp := map[string]string{
		"token":      token,
		"expires_at": expires.UTC().Format(time.RFC3339),
	}
	if h.viewHost == "" {
		// Path mode: the terminal's URL is knowable at mint time — the agent
		// id is derived from the token.
		resp["open_url"] = viewerPathPrefix + string(TokenAgentID(token)) + "/"
	}
	writeJSON(w, http.StatusOK, resp)
}

// agentsAPI lists the user's currently connected agents. Counts are live pool
// state straight from the hub — an agent with no live tunnel is absent, never
// shown with stale data.
func (h *Handler) agentsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	user, ok := h.auth(w, r)
	if !ok {
		jsonError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	type agentView struct {
		ID          string `json:"id"` // first 8 hex chars of sha256(token)
		Label       string `json:"label"`
		Port        int    `json:"port"`
		Tunnels     int    `json:"tunnels"`
		ConnectedAt string `json:"connected_at"`
		URL         string `json:"url,omitempty"` // path-mode terminal link
	}
	statuses := h.hub.AgentsForUser(user.ID)
	views := make([]agentView, 0, len(statuses))
	for _, s := range statuses {
		v := agentView{
			ID:          shortID(s.ID),
			Label:       s.Info.Label,
			Port:        s.Info.Port,
			Tunnels:     s.Tunnels,
			ConnectedAt: s.ConnectedAt.UTC().Format(time.RFC3339),
		}
		if h.viewHost == "" {
			v.URL = viewerPathPrefix + string(s.ID) + "/"
		}
		views = append(views, v)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"agents":    views,
		"view_host": h.viewHost,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
