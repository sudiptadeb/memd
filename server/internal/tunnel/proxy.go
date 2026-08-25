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

// viewerCtxKey carries the viewer routing decision through the request
// context into the proxy's Director and DialContext: which agent to dial,
// and — in path mode — the stripped URL prefix the terminal is served under.
// An empty prefix means host mode (the dedicated view host).
type viewerCtxKey struct{}

type viewerCtx struct {
	id     AgentID
	prefix string
}

func withViewer(ctx context.Context, id AgentID, prefix string) context.Context {
	return context.WithValue(ctx, viewerCtxKey{}, viewerCtx{id: id, prefix: prefix})
}

func viewerFromContext(ctx context.Context) viewerCtx {
	v, _ := ctx.Value(viewerCtxKey{}).(viewerCtx)
	return v
}

// newProxy builds the single reverse proxy that serves every viewer request —
// both plain HTTP and WebSocket upgrades (httputil.ReverseProxy handles 101
// Switching Protocols natively). "Dialing" opens an smux stream to the
// viewer's agent; the agent splices it to the local termulaa.
func newProxy(hub *Hub) *httputil.ReverseProxy {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return hub.DialStream(ctx, viewerFromContext(ctx).id)
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
			v := viewerFromContext(req.Context())
			info, _ := hub.Lookup(v.id) // a lost agent fails in DialContext with ErrNoAgent
			target := "localhost:" + strconv.Itoa(info.Port)

			// The whole trick that lets an UNMODIFIED termulaa serve remote
			// browsers: its DNS-rebinding guard only accepts loopback Host and
			// Origin values, so rewrite both to the local address the agent
			// splices to. The real destination is fixed by DialContext; the
			// URL host only keys the connection pool per agent.
			req.URL.Scheme = "http"
			req.URL.Host = "rc-" + string(v.id)
			req.Host = target
			if req.Header.Get("Origin") != "" {
				req.Header.Set("Origin", "http://"+target)
			}

			// Do not leak the public deployment into the local process.
			// X-Forwarded-Prefix is never trusted from the client: dropped in
			// host mode, overwritten with the stripped /rc/t/<agent> prefix in
			// path mode so termulaa renders its pages under that base path.
			req.Header.Del("X-Forwarded-Host")
			req.Header.Del("X-Forwarded-Proto")
			req.Header.Del("X-Forwarded-Prefix")
			if v.prefix != "" {
				req.Header.Set("X-Forwarded-Prefix", v.prefix)
				// Path mode shares memd's origin, so every cookie on the
				// request is memd's (login session included) and none is the
				// terminal's. Forward none of them to the agent's machine.
				req.Header.Del("Cookie")
			} else {
				// Host mode: keep termulaa's own cookies (if any), but never
				// the pairing cookie — a bearer secret termulaa has no use for.
				stripCookie(req, viewerCookieName)
			}
		},
		Transport: transport,
		// Immediate flush: terminal output must never sit in a proxy buffer.
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if errors.Is(err, ErrNoAgent) {
				serveAgentOffline(w)
				return
			}
			logs.Warn("rc: proxy error for agent %s: %v", shortID(viewerFromContext(r.Context()).id), err)
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
