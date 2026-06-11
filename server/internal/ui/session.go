package ui

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

const (
	sessionCookieName = "memd_session"
	defaultSessionTTL = 24 * time.Hour
)

// sessionData is the payload sealed into the session cookie. memd keeps no
// server-side session store: the cookie is the session. It is re-validated on
// every request, and the user record is reloaded from the database by ID so
// disable/role changes take effect immediately.
type sessionData struct {
	UserID         string    `json:"uid"`
	Issuer         string    `json:"iss,omitempty"` // OIDC `iss`; empty for local logins
	Subject        string    `json:"sub,omitempty"` // OIDC `sub`; empty for local logins
	Username       string    `json:"usr"`
	SuperAdmin     bool      `json:"adm"`
	IssuedAt       time.Time `json:"iat"`
	AbsoluteExpiry time.Time `json:"abs"` // hard cap on session lifetime

	// OIDC silent-refresh state. Absent for local logins.
	IDTokenExpiry time.Time `json:"ide,omitempty"`
	RefreshToken  string    `json:"rt,omitempty"`
}

func (s sessionData) expired(now time.Time) bool {
	return s.AbsoluteExpiry.IsZero() || now.After(s.AbsoluteExpiry)
}

func (s sessionData) needsRefresh(now time.Time) bool {
	return !s.IDTokenExpiry.IsZero() && now.After(s.IDTokenExpiry)
}

// SessionManager seals and opens session cookies with authenticated encryption
// (AES-256-GCM). The key is derived from the deployment secret; both local and
// OIDC sessions use the same envelope, so refresh tokens live encrypted inside
// the cookie rather than in a server-side store.
type SessionManager struct {
	aead        cipher.AEAD
	absoluteTTL time.Duration
	ephemeral   bool
}

// NewSessionManager derives the cookie key from secret. When secret is empty a
// random key is generated; sessions then survive only until the process
// restarts (ephemeral is reported so the caller can warn).
func NewSessionManager(secret string, absoluteTTL time.Duration) (*SessionManager, error) {
	if absoluteTTL <= 0 {
		absoluteTTL = defaultSessionTTL
	}
	var key [32]byte
	ephemeral := false
	if secret == "" {
		if _, err := rand.Read(key[:]); err != nil {
			return nil, err
		}
		ephemeral = true
	} else {
		key = sha256.Sum256([]byte(secret))
	}
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &SessionManager{aead: aead, absoluteTTL: absoluteTTL, ephemeral: ephemeral}, nil
}

// Ephemeral reports whether the cookie key is process-local (no secret set).
func (m *SessionManager) Ephemeral() bool { return m.ephemeral }

// AbsoluteTTL is the hard session-lifetime cap applied to new sessions.
func (m *SessionManager) AbsoluteTTL() time.Duration { return m.absoluteTTL }

// Issue seals the session into the cookie, filling in issue/expiry timestamps.
func (m *SessionManager) Issue(w http.ResponseWriter, r *http.Request, data sessionData) error {
	now := time.Now()
	if data.IssuedAt.IsZero() {
		data.IssuedAt = now
	}
	if data.AbsoluteExpiry.IsZero() {
		data.AbsoluteExpiry = data.IssuedAt.Add(m.absoluteTTL)
	}
	token, err := m.seal(data)
	if err != nil {
		return err
	}
	http.SetCookie(w, sessionCookie(r, token, data.AbsoluteExpiry))
	return nil
}

// Read opens and validates the session cookie. It returns false when the cookie
// is missing, tampered, or past its absolute expiry.
func (m *SessionManager) Read(r *http.Request) (sessionData, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return sessionData{}, false
	}
	var data sessionData
	if err := m.open(cookie.Value, &data); err != nil {
		return sessionData{}, false
	}
	if data.expired(time.Now()) {
		return sessionData{}, false
	}
	return data, true
}

// Clear removes the session cookie.
func (m *SessionManager) Clear(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, sessionCookie(r, "", time.Unix(0, 0)))
}

func (m *SessionManager) seal(v any) (string, error) {
	plaintext, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, m.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ciphertext := m.aead.Seal(nonce, nonce, plaintext, nil)
	return base64.RawURLEncoding.EncodeToString(ciphertext), nil
}

func (m *SessionManager) open(token string, v any) error {
	// Strict() rejects non-canonical encodings: without it, flipping the unused
	// trailing bits of the last base64 character decodes to identical bytes, so
	// a "tampered" token could still authenticate.
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil {
		return err
	}
	ns := m.aead.NonceSize()
	if len(raw) < ns {
		return errors.New("session token too short")
	}
	nonce, ciphertext := raw[:ns], raw[ns:]
	plaintext, err := m.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return err
	}
	return json.Unmarshal(plaintext, v)
}

func sessionCookie(r *http.Request, value string, expires time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    value,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isHTTPS(r),
	}
}

func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return r.Header.Get("X-Forwarded-Proto") == "https"
}
