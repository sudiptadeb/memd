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

// Entry is one log record.
type Entry struct {
	ID      int64     `json:"id"`
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
}

const maxEntries = 500

var (
	mu      sync.Mutex
	entries []Entry
	nextID  int64
)

func append1(level, msg string) {
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
	})
	nextID++
	if len(entries) > maxEntries {
		entries = entries[len(entries)-maxEntries:]
	}
}

// Info logs an informational message.
func Info(format string, args ...any) { append1("info", fmt.Sprintf(format, args...)) }

// Warn logs a warning.
func Warn(format string, args ...any) { append1("warn", fmt.Sprintf(format, args...)) }

// Error logs an error.
func Error(format string, args ...any) { append1("error", fmt.Sprintf(format, args...)) }

// Since returns all entries with id > since. Pass -1 to fetch everything.
func Since(since int64) []Entry {
	mu.Lock()
	defer mu.Unlock()
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if e.ID > since {
			out = append(out, e)
		}
	}
	return out
}
