package tunnel

import (
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// wsConn adapts a *websocket.Conn to net.Conn so an smux session can ride on
// it. Raw smux bytes travel in WebSocket binary messages. The adapter is
// specified by the rc protocol and must behave identically on both ends:
//
//   - Read drains a buffered remainder before fetching the next message; one
//     message may be consumed across many Read calls.
//   - Write sends exactly one binary message per call, serialized by a mutex
//     (gorilla permits only one concurrent writer).
//   - No WebSocket read deadline or ping/pong scheme is layered on top: smux's
//     own keepalive is the liveness detector.
type wsConn struct {
	ws *websocket.Conn

	readMu sync.Mutex
	rest   []byte // unread remainder of the last message

	writeMu sync.Mutex

	closeOnce sync.Once
	closeErr  error
}

func newWSConn(ws *websocket.Conn) *wsConn { return &wsConn{ws: ws} }

func (c *wsConn) Read(p []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	for len(c.rest) == 0 {
		_, data, err := c.ws.ReadMessage()
		if err != nil {
			return 0, err
		}
		c.rest = data
	}
	n := copy(p, c.rest)
	c.rest = c.rest[n:]
	return n, nil
}

func (c *wsConn) Write(p []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.ws.WriteMessage(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *wsConn) Close() error {
	c.closeOnce.Do(func() { c.closeErr = c.ws.Close() })
	return c.closeErr
}

func (c *wsConn) LocalAddr() net.Addr  { return c.ws.LocalAddr() }
func (c *wsConn) RemoteAddr() net.Addr { return c.ws.RemoteAddr() }

// SetWriteDeadline maps to the WebSocket write deadline (smux uses it to bound
// keepalive writes).
func (c *wsConn) SetWriteDeadline(t time.Time) error { return c.ws.SetWriteDeadline(t) }

// SetReadDeadline is a deliberate no-op: smux's keepalive timeout detects a
// dead peer, and a WS read deadline would tear down healthy idle tunnels.
func (c *wsConn) SetReadDeadline(time.Time) error { return nil }

// SetDeadline sets only the write half, per the read-deadline rationale above.
func (c *wsConn) SetDeadline(t time.Time) error { return c.ws.SetWriteDeadline(t) }

// notifyConn wraps a carrier conn and signals the first read/write error.
// smux only marks a session closed after its keepalive timeout, even when the
// underlying conn has already failed hard; since memd holds the carrier, it
// can convert that hard failure into an immediate session close (the handler
// selects on dead) so the hub's pool state tracks reality without a 30s lag.
// smux's keepalive stays the detector for silent stalls.
type notifyConn struct {
	net.Conn
	once sync.Once
	dead chan struct{}
}

func newNotifyConn(c net.Conn) *notifyConn {
	return &notifyConn{Conn: c, dead: make(chan struct{})}
}

func (c *notifyConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if err != nil {
		c.markDead()
	}
	return n, err
}

func (c *notifyConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if err != nil {
		c.markDead()
	}
	return n, err
}

func (c *notifyConn) markDead() { c.once.Do(func() { close(c.dead) }) }
