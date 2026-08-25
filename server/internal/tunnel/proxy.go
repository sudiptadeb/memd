package tunnel

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"strconv"
	"time"

	"github.com/sudiptadeb/memd/server/internal/logs"
)

// agentCtxKey carries the target AgentID through the request context into the
// proxy's Director and DialContext.
type agentCtxKey struct{}

func withAgentID(ctx context.Context, id AgentID) context.Context {
	return context.WithValue(ctx, agentCtxKey{}, id)
}

func agentIDFromContext(ctx context.Context) AgentID {
	id, _ := ctx.Value(agentCtxKey{}).(AgentID)
	return id
}

// newProxy builds the single reverse proxy that serves every viewer request —
// both plain HTTP and WebSocket upgrades (httputil.ReverseProxy handles 101
// Switching Protocols natively). "Dialing" opens an smux stream to the
// viewer's agent; the agent splices it to the local termulaa.
func newProxy(hub *Hub) *httputil.ReverseProxy {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return hub.DialStream(ctx, agentIDFromContext(ctx))
		},
		// Idle streams may be reused, but only for the same agent: the
		// Director gives each agent a unique URL host (the pool key), so a
		// pooled stream can never be handed to a different agent that
		// happens to report the same local port.
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     90 * time.Second,
	}
	return &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			id := agentIDFromContext(req.Context())
			info, _ := hub.Lookup(id) // a lost agent fails in DialContext with ErrNoAgent
			target := "localhost:" + strconv.Itoa(info.Port)

			// The whole trick that lets an UNMODIFIED termulaa serve remote
			// browsers: its DNS-rebinding guard only accepts loopback Host and
			// Origin values, so rewrite both to the local address the agent
			// splices to. The real destination is fixed by DialContext; the
			// URL host only keys the connection pool per agent.
			req.URL.Scheme = "http"
			req.URL.Host = "rc-" + string(id)
			req.Host = target
			if req.Header.Get("Origin") != "" {
				req.Header.Set("Origin", "http://"+target)
			}

			// Do not leak the public deployment into the local process, and do
			// not forward the viewer's pairing cookie — it is a bearer secret
			// that termulaa has no use for.
			req.Header.Del("X-Forwarded-Host")
			req.Header.Del("X-Forwarded-Proto")
			stripCookie(req, viewerCookieName)
		},
		Transport: transport,
		// Immediate flush: terminal output must never sit in a proxy buffer.
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if errors.Is(err, ErrNoAgent) {
				serveAgentOffline(w)
				return
			}
			logs.Warn("rc: proxy error for agent %s: %v", shortID(agentIDFromContext(r.Context())), err)
			serveStatusPage(w, http.StatusBadGateway, "Tunnel error",
				"The connection to the agent failed. Check the agent's logs and retry.")
		},
	}
}

// stripCookie removes one cookie from the request, keeping all others (the
// proxied termulaa may use cookies of its own).
func stripCookie(req *http.Request, name string) {
	cookies := req.Cookies()
	req.Header.Del("Cookie")
	for _, c := range cookies {
		if c.Name != name {
			req.AddCookie(c)
		}
	}
}
