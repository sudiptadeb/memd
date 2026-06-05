package ui

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"sync"
	"time"
)

const sessionCookieName = "memd_session"

type Session struct {
	ID         string
	UserID     string
	Username   string
	SuperAdmin bool
	ExpiresAt  time.Time
}

type SessionManager struct {
	mu       sync.Mutex
	sessions map[string]Session
	ttl      time.Duration
}

func NewSessionManager(ttl time.Duration) *SessionManager {
	return &SessionManager{
		sessions: map[string]Session{},
		ttl:      ttl,
	}
}

func (m *SessionManager) Create(w http.ResponseWriter, r *http.Request, userID, username string, superAdmin bool) error {
	id, err := newSessionID()
	if err != nil {
		return err
	}
	expires := time.Now().Add(m.ttl)
	m.mu.Lock()
	m.sessions[id] = Session{
		ID:         id,
		UserID:     userID,
		Username:   username,
		SuperAdmin: superAdmin,
		ExpiresAt:  expires,
	}
	m.mu.Unlock()
	http.SetCookie(w, sessionCookie(r, id, expires))
	return nil
}

func (m *SessionManager) Get(r *http.Request) (Session, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return Session{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[cookie.Value]
	if !ok {
		return Session{}, false
	}
	if time.Now().After(session.ExpiresAt) {
		delete(m.sessions, cookie.Value)
		return Session{}, false
	}
	return session, true
}

func (m *SessionManager) Clear(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		m.mu.Lock()
		delete(m.sessions, cookie.Value)
		m.mu.Unlock()
	}
	http.SetCookie(w, sessionCookie(r, "", time.Unix(0, 0)))
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

func newSessionID() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
