// Package notifychannel serves notification channels at /notify: the part of
// ntfy's protocol that a sender and a phone app need, so a client made for
// ntfy.sh works by changing its address to https://<host>/notify/<token>.
//
//	POST|PUT /notify/<token>              publish the body (at most 4096 bytes)
//	GET      /notify/<token>/ws?since=    a WebSocket of events
//	GET      /notify/<token>/json?since=  the same as JSON lines; poll=1 returns at once
//
// Events are ntfy's: "open" when a stream starts, "keepalive" every 45
// seconds, and "message" with id, time (unix seconds) and message. since is a
// message id, "all" or unix seconds; without it a stream sends only what comes
// next. The token is the channel's only lock, as a topic name is on ntfy.sh;
// messages are stored as they came, and senders seal them for the phone.
package notifychannel

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/debkosh/memd/server/internal/account"
	"github.com/debkosh/memd/server/internal/logs"
)

// Prefix is where the channels are mounted.
const Prefix = "/notify"

const (
	// MaxMessage is the largest body a publish takes, as on ntfy.sh.
	MaxMessage = 4096
	// keepaliveEvery is under nginx's default 60 s proxy read timeout. Each
	// keepalive also checks the token still works, so a new token or a
	// deleted channel ends open streams within this long.
	keepaliveEvery = 45 * time.Second
	cleanupEvery   = time.Hour
	// subscriberBuffer is how many messages a slow stream may fall behind
	// before it is ended; the client reconnects with since and catches up.
	subscriberBuffer = 64
)

// Handler is the /notify endpoint.
type Handler struct {
	accounts  *account.Store
	keepalive time.Duration

	mu   sync.Mutex
	subs map[string]map[chan account.NotifyMessage]struct{}

	upgrader websocket.Upgrader
}

// New makes the endpoint over the account database.
func New(accounts *account.Store) *Handler {
	return &Handler{
		accounts:  accounts,
		keepalive: keepaliveEvery,
		subs:      map[string]map[chan account.NotifyMessage]struct{}{},
		// The token in the path is the credential, not a cookie, so any
		// origin may connect; phone apps send none.
		upgrader: websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }},
	}
}

// Mount registers the endpoint on mux.
func (h *Handler) Mount(mux *http.ServeMux) {
	mux.Handle(Prefix+"/", h)
}

// event is one of ntfy's events, as clients parse them.
type event struct {
	ID      string `json:"id"`
	Time    int64  `json:"time"`
	Event   string `json:"event"`
	Topic   string `json:"topic"`
	Message string `json:"message,omitempty"`
}

func messageEvent(channel string, m account.NotifyMessage) event {
	return event{ID: m.ID, Time: m.Time, Event: "message", Topic: channel, Message: m.Message}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	tok, action, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, Prefix+"/"), "/")
	if tok == "" {
		http.NotFound(w, r)
		return
	}
	ch, err := h.accounts.NotifyChannelByToken(r.Context(), tok)
	if errors.Is(err, account.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no such channel")
		return
	}
	if err != nil {
		logs.Error("notify: look up channel: %v", err)
		writeError(w, http.StatusInternalServerError, "server error")
		return
	}
	switch {
	case action == "" && (r.Method == http.MethodPost || r.Method == http.MethodPut):
		h.publish(w, r, ch)
	case action == "ws" && r.Method == http.MethodGet:
		h.serveWS(w, r, ch, tok)
	case action == "json" && r.Method == http.MethodGet:
		h.serveJSON(w, r, ch, tok)
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

func (h *Handler) publish(w http.ResponseWriter, r *http.Request, ch account.NotifyChannel) {
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxMessage+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not read the message")
		return
	}
	if len(body) > MaxMessage {
		writeError(w, http.StatusRequestEntityTooLarge, "the message is longer than 4096 bytes")
		return
	}
	if strings.TrimSpace(string(body)) == "" {
		writeError(w, http.StatusBadRequest, "the message is empty")
		return
	}
	m, err := h.accounts.PublishNotify(r.Context(), ch.ID, string(body))
	if err != nil {
		logs.Error("notify: publish to channel id=%s: %v", ch.ID, err)
		writeError(w, http.StatusInternalServerError, "server error")
		return
	}
	h.broadcast(ch.ID, m)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(messageEvent(ch.ID, m))
}

func (h *Handler) broadcast(channel string, m account.NotifyMessage) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.subs[channel] {
		select {
		case sub <- m:
		default:
			// Too far behind: end its stream; it resumes from its last id.
			delete(h.subs[channel], sub)
			close(sub)
		}
	}
}

func (h *Handler) subscribe(channel string) chan account.NotifyMessage {
	sub := make(chan account.NotifyMessage, subscriberBuffer)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subs[channel] == nil {
		h.subs[channel] = map[chan account.NotifyMessage]struct{}{}
	}
	h.subs[channel][sub] = struct{}{}
	return sub
}

func (h *Handler) unsubscribe(channel string, sub chan account.NotifyMessage) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subs[channel][sub]; ok {
		delete(h.subs[channel], sub)
		close(sub)
	}
	if len(h.subs[channel]) == 0 {
		delete(h.subs, channel)
	}
}

// stream sends open, then what was kept after since, then each new message
// and a keepalive now and then, until send fails, the request ends, the
// stream falls too far behind, or the token stops working. It subscribes
// before reading what was kept, so nothing published in between is missed,
// and skips live messages the backlog already had.
func (h *Handler) stream(ctx context.Context, ch account.NotifyChannel, tok, since string, send func(event) error) {
	sub := h.subscribe(ch.ID)
	defer h.unsubscribe(ch.ID, sub)
	if err := send(event{ID: "", Time: time.Now().Unix(), Event: "open", Topic: ch.ID}); err != nil {
		return
	}
	var last int64
	if since != "" {
		kept, err := h.accounts.NotifyAfter(ctx, ch.ID, since)
		if err != nil {
			logs.Warn("notify: read channel id=%s: %v", ch.ID, err)
			return
		}
		for _, m := range kept {
			if err := send(messageEvent(ch.ID, m)); err != nil {
				return
			}
			last = m.Seq
		}
	}
	tick := time.NewTicker(h.keepalive)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case m, ok := <-sub:
			if !ok {
				return
			}
			if m.Seq <= last {
				continue
			}
			if err := send(messageEvent(ch.ID, m)); err != nil {
				return
			}
		case <-tick.C:
			if c, err := h.accounts.NotifyChannelByToken(ctx, tok); err != nil || c.ID != ch.ID {
				return
			}
			if err := send(event{Time: time.Now().Unix(), Event: "keepalive", Topic: ch.ID}); err != nil {
				return
			}
		}
	}
}

func (h *Handler) serveWS(w http.ResponseWriter, r *http.Request, ch account.NotifyChannel, tok string) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return // Upgrade has answered already.
	}
	defer conn.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Read (and drop) what the client sends, so pings are answered and a
	// close or a dead connection ends the stream.
	go func() {
		defer cancel()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
	h.stream(ctx, ch, tok, r.URL.Query().Get("since"), func(e event) error {
		_ = conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
		return conn.WriteJSON(e)
	})
}

func (h *Handler) serveJSON(w http.ResponseWriter, r *http.Request, ch account.NotifyChannel, tok string) {
	q := r.URL.Query()
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	if poll := q.Get("poll"); poll == "1" || poll == "true" {
		kept, err := h.accounts.NotifyAfter(r.Context(), ch.ID, q.Get("since"))
		if err != nil {
			logs.Warn("notify: read channel id=%s: %v", ch.ID, err)
			writeError(w, http.StatusInternalServerError, "server error")
			return
		}
		for _, m := range kept {
			_ = enc.Encode(messageEvent(ch.ID, m))
		}
		return
	}
	flusher, _ := w.(http.Flusher)
	h.stream(r.Context(), ch, tok, q.Get("since"), func(e event) error {
		if err := enc.Encode(e); err != nil {
			return err
		}
		if flusher != nil {
			flusher.Flush()
		}
		return nil
	})
}

// Start removes old messages every hour until ctx is done.
func (h *Handler) Start(ctx context.Context) {
	go func() {
		t := time.NewTicker(cleanupEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if n, err := h.accounts.CleanupNotify(ctx); err != nil {
					logs.Warn("notify: cleanup: %v", err)
				} else if n > 0 {
					logs.Info("notify: cleanup removed %d old messages", n)
				}
			}
		}
	}()
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"http": status, "error": msg})
}
