package ui

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/url"
	"time"

	"github.com/sudiptadeb/memd/server/internal/account"
	"github.com/sudiptadeb/memd/server/internal/logs"
	"golang.org/x/oauth2"
)

const (
	oidcTxCookieName = "memd_oidc_tx"
	oidcTxTTL        = 10 * time.Minute
)

// oidcTx is the short-lived state carried (encrypted) between the login redirect
// and the callback: CSRF state, replay nonce, the PKCE verifier, and where to
// return the user afterwards.
type oidcTx struct {
	State    string `json:"st"`
	Nonce    string `json:"no"`
	Verifier string `json:"pv"`
	ReturnTo string `json:"rt"`
}

// oidcEnabled reports whether an IdP is configured for this deployment.
func (h *Handler) oidcEnabled() bool { return h.oidc.Provider() != nil }

// oidcLogin starts the Authorization Code + PKCE flow.
func (h *Handler) oidcLogin(w http.ResponseWriter, r *http.Request) {
	provider := h.oidc.Provider()
	if provider == nil {
		http.NotFound(w, r)
		return
	}
	tx := oidcTx{
		State:    randomToken(),
		Nonce:    randomToken(),
		Verifier: oauth2.GenerateVerifier(),
		ReturnTo: safeReturnTo(r.URL.Query().Get("return_to")),
	}
	if err := h.setTxCookie(w, r, tx); err != nil {
		httpErr(w, http.StatusInternalServerError, err)
		return
	}
	http.Redirect(w, r, provider.AuthCodeURL(tx.State, tx.Nonce, tx.Verifier), http.StatusFound)
}

// oidcCallback completes the flow: it validates state, exchanges the code,
// verifies the ID token, provisions/links the user, and issues the session.
func (h *Handler) oidcCallback(w http.ResponseWriter, r *http.Request) {
	provider := h.oidc.Provider()
	if provider == nil {
		http.NotFound(w, r)
		return
	}
	tx, ok := h.readTxCookie(r)
	h.clearTxCookie(w, r)
	if !ok {
		h.loginError(w, r, "login session expired; please try again")
		return
	}
	if q := r.URL.Query(); q.Get("error") != "" {
		logs.Warn("oidc callback error: %s %s", q.Get("error"), q.Get("error_description"))
		h.loginError(w, r, "the identity provider rejected the sign-in")
		return
	}
	if r.URL.Query().Get("state") != tx.State {
		h.loginError(w, r, "invalid sign-in state")
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		h.loginError(w, r, "missing authorization code")
		return
	}

	tokens, err := provider.Exchange(r.Context(), code, tx.Verifier, tx.Nonce)
	if err != nil {
		logs.Error("oidc exchange failed: %v", err)
		h.loginError(w, r, "could not complete sign-in")
		return
	}

	settings, _, err := h.accounts.GetOIDCSettings(r.Context())
	if err != nil || settings.ProviderID == "" {
		logs.Error("oidc settings unavailable during callback: %v", err)
		h.loginError(w, r, "could not complete sign-in")
		return
	}
	user, err := h.accounts.UpsertOIDCUser(r.Context(), account.OIDCIdentity{
		ProviderID:        settings.ProviderID,
		Issuer:            tokens.Identity.Issuer,
		Subject:           tokens.Identity.Subject,
		Email:             tokens.Identity.Email,
		Name:              tokens.Identity.Name,
		PreferredUsername: tokens.Identity.PreferredUsername,
	})
	if err != nil {
		logs.Error("oidc provisioning failed for iss=%s sub=%s: %v", tokens.Identity.Issuer, tokens.Identity.Subject, err)
		h.loginError(w, r, "could not provision your account")
		return
	}
	if user.Disabled {
		h.loginError(w, r, "this account is disabled")
		return
	}

	if err := h.sessions.Issue(w, r, sessionData{
		UserID:        user.ID,
		Issuer:        user.Issuer,
		Subject:       user.Subject,
		Username:      user.Username,
		SuperAdmin:    user.SuperAdmin,
		IDTokenExpiry: tokens.IDTokenExpiry,
		RefreshToken:  tokens.RefreshToken,
	}); err != nil {
		httpErr(w, http.StatusInternalServerError, err)
		return
	}
	logs.InfoUser(user.ID, "oidc login: %q (id=%s, iss=%s, sub=%s)", user.Username, user.ID, user.Issuer, user.Subject)
	http.Redirect(w, r, tx.ReturnTo, http.StatusFound)
}

// refreshSession silently renews an OIDC session's ID token while under the
// absolute lifetime cap. It returns the (re-issued) session, or false when the
// caller should redirect to login.
func (h *Handler) refreshSession(w http.ResponseWriter, r *http.Request, session sessionData) (sessionData, bool) {
	provider := h.oidc.Provider()
	if provider == nil || session.RefreshToken == "" {
		return sessionData{}, false
	}
	tokens, err := provider.Refresh(r.Context(), session.RefreshToken)
	if err != nil {
		logs.Warn("oidc refresh failed for sub=%s: %v", session.Subject, err)
		return sessionData{}, false
	}
	if tokens.Identity.Issuer != session.Issuer || tokens.Identity.Subject != session.Subject {
		logs.Warn("oidc refresh identity changed from iss=%s sub=%s to iss=%s sub=%s", session.Issuer, session.Subject, tokens.Identity.Issuer, tokens.Identity.Subject)
		return sessionData{}, false
	}
	session.IDTokenExpiry = tokens.IDTokenExpiry
	if tokens.RefreshToken != "" {
		session.RefreshToken = tokens.RefreshToken
	}
	// Preserve the original absolute expiry (the hard cap is not extended).
	if err := h.sessions.Issue(w, r, session); err != nil {
		return sessionData{}, false
	}
	return session, true
}

// --- transaction cookie helpers ---

func (h *Handler) setTxCookie(w http.ResponseWriter, r *http.Request, tx oidcTx) error {
	token, err := h.sessions.seal(tx)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     oidcTxCookieName,
		Value:    token,
		Path:     "/",
		Expires:  time.Now().Add(oidcTxTTL),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isHTTPS(r),
	})
	return nil
}

func (h *Handler) readTxCookie(r *http.Request) (oidcTx, bool) {
	cookie, err := r.Cookie(oidcTxCookieName)
	if err != nil || cookie.Value == "" {
		return oidcTx{}, false
	}
	var tx oidcTx
	if err := h.sessions.open(cookie.Value, &tx); err != nil {
		return oidcTx{}, false
	}
	return tx, true
}

func (h *Handler) clearTxCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     oidcTxCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isHTTPS(r),
	})
}

func (h *Handler) loginError(w http.ResponseWriter, r *http.Request, msg string) {
	http.Redirect(w, r, "/?login_error="+url.QueryEscape(msg), http.StatusFound)
}

func randomToken() string {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

// safeReturnTo only permits same-site, path-only redirects to avoid open redirects.
func safeReturnTo(raw string) string {
	if raw == "" || raw[0] != '/' || (len(raw) > 1 && raw[1] == '/') {
		return "/"
	}
	return raw
}
