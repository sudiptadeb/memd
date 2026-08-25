package tunnel

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"strconv"
	"strings"
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
// viewer proxy served on the dedicated view host.
type Handler struct {
	hub      *Hub
	key      []byte
	viewHost string
	auth     UserAuth
	proxy    *httputil.ReverseProxy
	upgrader websocket.Upgrader
}

// New builds a tunnel handler. key signs tokens, viewHost is the dedicated
// hostname the terminal is served on, and auth gates the management surface
// behind memd's login.
func New(key []byte, viewHost string, auth UserAuth) *Handler {
	hub := NewHub()
	return &Handler{
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
}

// FromEnv builds the handler from the environment, or returns nil when the
// feature is not (fully) configured. The rc feature is strictly opt-in:
// MEMD_RC_VIEW_HOST enables it, and a token key must be available — from
// MEMD_RC_TOKEN_SECRET or derived from MEMD_SESSION_SECRET.
func FromEnv(auth UserAuth) *Handler {
	viewHost := strings.TrimSpace(os.Getenv("MEMD_RC_VIEW_HOST"))
	rcSecret := os.Getenv("MEMD_RC_TOKEN_SECRET")
	key := TokenKey(rcSecret, os.Getenv("MEMD_SESSION_SECRET"))
	if viewHost == "" {
		if strings.TrimSpace(rcSecret) != "" {
			logs.Warn("rc: MEMD_RC_TOKEN_SECRET is set but MEMD_RC_VIEW_HOST is not; reverse tunnel disabled")
		}
		return nil
	}
	if key == nil {
		logs.Warn("rc: MEMD_RC_VIEW_HOST is set but no token secret is available (set MEMD_RC_TOKEN_SECRET or MEMD_SESSION_SECRET); reverse tunnel disabled")
		return nil
	}
	return New(key, viewHost, auth)
}

// ViewHost is the configured dedicated view hostname.
func (h *Handler) ViewHost() string { return h.viewHost }

// Mount registers the management and agent endpoints on memd's main mux
// (i.e. on the normal memd host, not the view host).
func (h *Handler) Mount(mux *http.ServeMux) {
	mux.HandleFunc("/rc", h.servePage)
	mux.HandleFunc("/rc/app.js", servePageJS)
	mux.HandleFunc("/rc/api/tokens", h.mintAPI)
	mux.HandleFunc("/rc/api/agents", h.agentsAPI)
	mux.HandleFunc("/rc/tunnel", h.serveTunnel)
}

// SplitByHost routes by Host header: requests addressed to the view host go
// to the viewer proxy; everything else falls through to next (memd's normal
// stack) completely unchanged.
func (h *Handler) SplitByHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hostMatches(r.Host, h.viewHost) {
			h.serveViewer(w, r)
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
	id := TokenAgentID(token)

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
	release := h.hub.Register(id, sessionIdx, AgentInfo{UserID: claims.UserID, Label: label, Port: port}, sess)
	logs.InfoUser(claims.UserID, "rc: agent %s (%q) tunnel %d connected (%d tunnels up)",
		shortID(id), label, sessionIdx, h.hub.Tunnels(id))

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
	h.proxy.ServeHTTP(w, r.WithContext(withAgentID(r.Context(), id)))
}

// isHTTPS mirrors memd's session-cookie logic: direct TLS, or the
// X-Forwarded-Proto set by the fronting nginx.
func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// --- Management page + API (memd host, memd login) ----------------------

func (h *Handler) servePage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if _, ok := h.auth(w, r); !ok {
		serveSignInPage(w)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(rcPageHTML))
}

// mintAPI mints one token for the logged-in user. Body:
//
//	{"label": "my laptop", "ttl": 30}
//
// ttl is in days; absent or zero selects the 30-day default, and anything
// above 90 days is clamped.
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
	writeJSON(w, http.StatusOK, map[string]string{
		"token":      token,
		"expires_at": expires.UTC().Format(time.RFC3339),
	})
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
	}
	statuses := h.hub.AgentsForUser(user.ID)
	views := make([]agentView, 0, len(statuses))
	for _, s := range statuses {
		views = append(views, agentView{
			ID:          shortID(s.ID),
			Label:       s.Info.Label,
			Port:        s.Info.Port,
			Tunnels:     s.Tunnels,
			ConnectedAt: s.ConnectedAt.UTC().Format(time.RFC3339),
		})
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
