// Package logs is a tiny in-memory ring buffer of structured log entries
// surfaced by the web UI's /api/logs endpoint. Singleton-style — every other
// package just calls logs.Info/Warn/Error.
package logs

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// Entry is one log record. userID scopes visibility: an empty userID is a
// system entry, visible only to super admins; a set userID is visible to that
// user (and to super admins). It is unexported so it is never serialised.
type Entry struct {
	ID      int64     `json:"id"`
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
	userID  string
}

const maxEntries = 500

var (
	mu      sync.Mutex
	entries []Entry
	nextID  int64
)

func append1(level, userID, msg string) {
	// Mirror to stderr so headless deployments (systemd, Docker) can see runtime
	// activity; the ring buffer only survives until restart and is UI-only.
	log.Printf("[%s] %s", level, msg)

	mu.Lock()
	defer mu.Unlock()
	entries = append(entries, Entry{
		ID:      nextID,
		Time:    time.Now(),
		Level:   level,
		Message: msg,
		userID:  userID,
	})
	nextID++
	if len(entries) > maxEntries {
		entries = entries[len(entries)-maxEntries:]
	}
}

// Info logs a system informational message (super-admin visibility).
func Info(format string, args ...any) { append1("info", "", fmt.Sprintf(format, args...)) }

// Warn logs a system warning (super-admin visibility).
func Warn(format string, args ...any) { append1("warn", "", fmt.Sprintf(format, args...)) }

// Error logs a system error (super-admin visibility).
func Error(format string, args ...any) { append1("error", "", fmt.Sprintf(format, args...)) }

// InfoUser logs an informational message attributed to a user.
func InfoUser(userID, format string, args ...any) {
	append1("info", userID, fmt.Sprintf(format, args...))
}

// WarnUser logs a warning attributed to a user.
func WarnUser(userID, format string, args ...any) {
	append1("warn", userID, fmt.Sprintf(format, args...))
}

// ErrorUser logs an error attributed to a user.
func ErrorUser(userID, format string, args ...any) {
	append1("error", userID, fmt.Sprintf(format, args...))
}

// SinceForViewer returns entries with id > since that the viewer may see. When
// all is true (super admin), every entry is returned; otherwise only entries
// attributed to userID. Pass since=-1 to fetch everything.
func SinceForViewer(since int64, userID string, all bool) []Entry {
	mu.Lock()
	defer mu.Unlock()
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if e.ID <= since {
			continue
		}
		if all || (userID != "" && e.userID == userID) {
			out = append(out, e)
		}
	}
	return out
}
