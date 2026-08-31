package ui

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/sudiptadeb/memd/server/internal/account"
	"github.com/sudiptadeb/memd/server/internal/logs"
)

// Phone-app pairing. OIDC accounts cannot use POST /api/auth/login and get
// nothing to re-login with, so the phone is paired from the already-signed-in
// dashboard instead: /api/app/pair mints a short-lived code, the app redeems
// it (no auth) for a long-lived revocable app token, and /api/app/session
// trades that token for a normal session cookie whenever the cookie dies.

const (
	// A-Z2-9 minus the lookalikes 0/O and 1/I: 32 symbols, so 9 chars carry
	// 45 bits — far beyond what the 5-minute TTL and per-IP throttle leave
	// guessable.
	pairingCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	pairingCodeLength   = 9
	pairingCodeTTL      = 5 * time.Minute

	// Belt and braces on top of single-use + TTL: a small per-IP budget of
	// redeem attempts.
	redeemAttemptsPerWindow = 10
	redeemWindow            = time.Minute
)

var errInvalidPairingCode = errors.New("invalid or expired code")

// pairingStore holds pending pairing codes in memory only: a restart forgets
// them, which is fine — the dashboard just mints another. One outstanding code
// per user; minting replaces the previous one.
type pairingStore struct {
	mu     sync.Mutex
	byCode map[string]pairingEntry
	byUser map[string]string
	now    func() time.Time
}

type pairingEntry struct {
	userID    string
	expiresAt time.Time
}

func newPairingStore() *pairingStore {
	return &pairingStore{
		byCode: make(map[string]pairingEntry),
		byUser: make(map[string]string),
		now:    time.Now,
	}
}

func (p *pairingStore) mint(userID string) (code string, expiresAt time.Time, err error) {
	code, err = newPairingCode()
	if err != nil {
		return "", time.Time{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	// Lazy sweep: pending codes are bounded by the user count, so a full pass
	// on mint stays cheap and keeps abandoned codes from lingering.
	for c, e := range p.byCode {
		if now.After(e.expiresAt) {
			delete(p.byCode, c)
			if p.byUser[e.userID] == c {
				delete(p.byUser, e.userID)
			}
		}
	}
	if prev, ok := p.byUser[userID]; ok {
		delete(p.byCode, prev)
	}
	expiresAt = now.Add(pairingCodeTTL)
	p.byCode[code] = pairingEntry{userID: userID, expiresAt: expiresAt}
	p.byUser[userID] = code
	return code, expiresAt, nil
}

// redeem consumes a code: valid codes work exactly once.
func (p *pairingStore) redeem(raw string) (userID string, ok bool) {
	code := normalizePairingCode(raw)
	if len(code) != pairingCodeLength {
		return "", false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	entry, found := p.byCode[code]
	if !found {
		return "", false
	}
	delete(p.byCode, code)
	if p.byUser[entry.userID] == code {
		delete(p.byUser, entry.userID)
	}
	if p.now().After(entry.expiresAt) {
		return "", false
	}
	return entry.userID, true
}

// normalizePairingCode accepts what people type: any case, with or without
// the display grouping (dashes or spaces).
func normalizePairingCode(raw string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(raw)) {
		if r == '-' || r == ' ' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func newPairingCode() (string, error) {
	raw := make([]byte, pairingCodeLength)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	out := make([]byte, pairingCodeLength)
	for i, v := range raw {
		// len(alphabet) is 32, which divides 256 evenly: no modulo bias.
		out[i] = pairingCodeAlphabet[int(v)%len(pairingCodeAlphabet)]
	}
	return string(out), nil
}

// ipLimiter is a fixed-window per-IP attempt counter for the redeem endpoint.
type ipLimiter struct {
	mu     sync.Mutex
	hits   map[string][]time.Time
	limit  int
	window time.Duration
	now    func() time.Time
}

func newIPLimiter(limit int, window time.Duration) *ipLimiter {
	return &ipLimiter{
		hits:   make(map[string][]time.Time),
		limit:  limit,
		window: window,
		now:    time.Now,
	}
}

func (l *ipLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	cutoff := now.Add(-l.window)
	// Keep the map bounded: when it grows past a sane size, drop every key
	// whose window has fully passed.
	if len(l.hits) > 1024 {
		for k, ts := range l.hits {
			if len(ts) == 0 || ts[len(ts)-1].Before(cutoff) {
				delete(l.hits, k)
			}
		}
	}
	ts := l.hits[ip]
	kept := ts[:0]
	for _, t := range ts {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.limit {
		l.hits[ip] = kept
		return false
	}
	l.hits[ip] = append(kept, now)
	return true
}

// clientIP is the throttle key. Behind a reverse proxy every request shares
// the proxy's RemoteAddr, so the last X-Forwarded-For hop — the one appended
// by our own proxy — is preferred, mirroring the trust isHTTPS places in
// X-Forwarded-Proto.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// --- Handlers ---------------------------------------------------------------

// POST /api/app/pair (session-authenticated). Mints the user's pairing code.
func (h *Handler) appPairAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	user := userFromContext(r.Context())
	if user == nil {
		httpErr(w, http.StatusUnauthorized, errors.New("not authenticated"))
		return
	}
	code, expiresAt, err := h.appPairing.mint(user.ID)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"code":       code,
		"expires_at": expiresAt.UTC().Format(time.RFC3339),
	})
}

// POST /api/app/redeem (no auth). Trades a pairing code for an app token plus
// a session cookie. Every miss is the same 401 — a caller learns nothing about
// which part failed.
func (h *Handler) appRedeemAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !h.redeemLimiter.allow(clientIP(r)) {
		httpErr(w, http.StatusTooManyRequests, errors.New("too many attempts; try again in a minute"))
		return
	}
	var body struct {
		Code  string `json:"code"`
		Label string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpErr(w, http.StatusBadRequest, err)
		return
	}
	userID, ok := h.appPairing.redeem(body.Code)
	if !ok {
		httpErr(w, http.StatusUnauthorized, errInvalidPairingCode)
		return
	}
	user, err := h.accounts.UserByID(r.Context(), userID)
	if err != nil || user.Disabled {
		httpErr(w, http.StatusUnauthorized, errInvalidPairingCode)
		return
	}
	tok, secret, err := h.accounts.CreateAppToken(r.Context(), user.ID, body.Label)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err)
		return
	}
	if err := h.sessions.Issue(w, r, sessionData{
		UserID:     user.ID,
		Username:   user.Username,
		SuperAdmin: user.SuperAdmin,
	}); err != nil {
		httpErr(w, http.StatusInternalServerError, err)
		return
	}
	logs.InfoUser(user.ID, "paired the phone app (token id=%s, label=%q)", tok.ID, tok.Label)
	writeJSON(w, http.StatusOK, map[string]any{
		"token": secret,
		"user":  sessionUserFromAccount(user),
	})
}

// POST /api/app/session (bearer app token). Issues a fresh session cookie —
// the app's re-login path once the previous cookie has expired.
func (h *Handler) appSessionAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	tok, user, ok := h.appTokenUser(w, r)
	if !ok {
		return
	}
	// Best effort: a failed timestamp write must not fail the re-login.
	if err := h.accounts.TouchAppToken(r.Context(), tok.ID); err != nil {
		logs.Warn("app token touch failed (id=%s): %v", tok.ID, err)
	}
	if err := h.sessions.Issue(w, r, sessionData{
		UserID:     user.ID,
		Username:   user.Username,
		SuperAdmin: user.SuperAdmin,
	}); err != nil {
		httpErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": sessionUserFromAccount(user)})
}

// GET /api/app/tokens (session-authenticated). The user's paired phones.
func (h *Handler) appTokensAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	user := userFromContext(r.Context())
	if user == nil {
		httpErr(w, http.StatusUnauthorized, errors.New("not authenticated"))
		return
	}
	toks, err := h.accounts.ListAppTokens(r.Context(), user.ID)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": appTokenViews(toks)})
}

// DELETE /api/app/tokens/{id} (session) and DELETE /api/app/tokens/self
// (bearer app token — the app's sign-out). Auth is resolved here rather than
// via requireUser because the /self variant carries no cookie.
func (h *Handler) appTokenAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/app/tokens/"), "/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	if id == "self" {
		raw, ok := bearerToken(r)
		if !ok {
			httpErr(w, http.StatusUnauthorized, errInvalidAppToken)
			return
		}
		if err := h.accounts.RevokeAppTokenByToken(r.Context(), raw); err != nil {
			httpErr(w, http.StatusUnauthorized, errInvalidAppToken)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	user, ok := h.currentUser(w, r)
	if !ok {
		httpErr(w, http.StatusUnauthorized, errors.New("not authenticated"))
		return
	}
	if err := h.accounts.RevokeAppToken(r.Context(), user.ID, id); err != nil {
		httpErr(w, statusForAccountError(err), err)
		return
	}
	logs.InfoUser(user.ID, "un-paired phone app token id=%s", id)
	w.WriteHeader(http.StatusNoContent)
}

var errInvalidAppToken = errors.New("invalid or revoked token")

// appTokenUser authenticates a bearer app token and loads its (active) user.
// On failure it writes the 401 itself and returns ok=false.
func (h *Handler) appTokenUser(w http.ResponseWriter, r *http.Request) (account.AppToken, account.User, bool) {
	raw, ok := bearerToken(r)
	if !ok {
		httpErr(w, http.StatusUnauthorized, errInvalidAppToken)
		return account.AppToken{}, account.User{}, false
	}
	tok, err := h.accounts.AppTokenByToken(r.Context(), raw)
	if err != nil {
		httpErr(w, http.StatusUnauthorized, errInvalidAppToken)
		return account.AppToken{}, account.User{}, false
	}
	user, err := h.accounts.UserByID(r.Context(), tok.UserID)
	if err != nil || user.Disabled {
		httpErr(w, http.StatusUnauthorized, errInvalidAppToken)
		return account.AppToken{}, account.User{}, false
	}
	return tok, user, true
}

func bearerToken(r *http.Request) (string, bool) {
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(auth) <= len(prefix) || !strings.EqualFold(auth[:len(prefix)], prefix) {
		return "", false
	}
	raw := strings.TrimSpace(auth[len(prefix):])
	return raw, raw != ""
}

type appTokenView struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	CreatedAt  string `json:"created_at"`
	LastUsedAt string `json:"last_used_at,omitempty"`
}

func appTokenViews(toks []account.AppToken) []appTokenView {
	out := make([]appTokenView, 0, len(toks))
	for _, tok := range toks {
		v := appTokenView{
			ID:        tok.ID,
			Label:     tok.Label,
			CreatedAt: tok.CreatedAt.UTC().Format(time.RFC3339),
		}
		if tok.LastUsedAt != nil {
			v.LastUsedAt = tok.LastUsedAt.UTC().Format(time.RFC3339)
		}
		out = append(out, v)
	}
	return out
}
