// Package tunnel is memd's reverse-tunnel rendezvous: the server half of the
// termulaa rc protocol (memd is a reference implementation; the normative wire
// spec lives in the termulaa repository under docs/rc-protocol.md).
//
// A termulaa agent dials OUT to memd and keeps a small pool of WebSocket
// connections open; each carries one smux session (a "tunnel"). Every browser
// connection to the view host becomes one stream multiplexed inside a tunnel,
// which the agent splices to the loopback-only termulaa server. memd never
// dials the agent's host, and the agent parses no HTTP.
package tunnel

import (
	"time"

	"github.com/xtaci/smux"
)

// muxConfig is the smux configuration fixed by the rc protocol. It MUST be
// byte-identical on both sides of the tunnel — a mismatch causes silent
// stalls, not errors. Do not tune these values independently.
func muxConfig() *smux.Config {
	c := smux.DefaultConfig()
	c.Version = 2
	c.KeepAliveInterval = 10 * time.Second
	c.KeepAliveTimeout = 30 * time.Second
	c.MaxFrameSize = 32 * 1024
	c.MaxReceiveBuffer = 4 * 1024 * 1024
	c.MaxStreamBuffer = 512 * 1024
	return c
}
