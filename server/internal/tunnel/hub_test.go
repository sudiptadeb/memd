package tunnel

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/xtaci/smux"
)

// sessionPair builds a connected smux client/server pair over an in-memory
// pipe, with the server end accepting and draining streams like a trivial
// agent. It returns the client session — the side a Hub holds.
func sessionPair(t *testing.T) *smux.Session {
	t.Helper()
	c1, c2 := net.Pipe()
	client, err := smux.Client(c1, muxConfig())
	if err != nil {
		t.Fatalf("smux.Client: %v", err)
	}
	server, err := smux.Server(c2, muxConfig())
	if err != nil {
		t.Fatalf("smux.Server: %v", err)
	}
	go func() {
		for {
			stream, err := server.AcceptStream()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(io.Discard, stream) }()
		}
	}()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	return client
}

func TestHubEmptyNeverFabricatesLiveness(t *testing.T) {
	hub := NewHub()
	if _, ok := hub.Lookup("nope"); ok {
		t.Error("Lookup reported an agent that never connected")
	}
	if got := hub.Tunnels("nope"); got != 0 {
		t.Errorf("Tunnels = %d for unknown agent", got)
	}
	if _, err := hub.DialStream(context.Background(), "nope"); !errors.Is(err, ErrNoAgent) {
		t.Errorf("DialStream error = %v, want ErrNoAgent", err)
	}
	if agents := hub.AgentsForUser("user-1"); len(agents) != 0 {
		t.Errorf("AgentsForUser = %v for empty hub", agents)
	}
}

func TestHubRegisterLookupRelease(t *testing.T) {
	hub := NewHub()
	info := AgentInfo{UserID: "user-1", Label: "laptop", Port: 17380}
	release := hub.Register("agent-a", 0, info, sessionPair(t))

	got, ok := hub.Lookup("agent-a")
	if !ok || got != info {
		t.Fatalf("Lookup = %+v, %v", got, ok)
	}
	stream, err := hub.DialStream(context.Background(), "agent-a")
	if err != nil {
		t.Fatalf("DialStream: %v", err)
	}
	_ = stream.Close()

	agents := hub.AgentsForUser("user-1")
	if len(agents) != 1 || agents[0].Tunnels != 1 || agents[0].Info != info {
		t.Fatalf("AgentsForUser = %+v", agents)
	}
	if agents := hub.AgentsForUser("someone-else"); len(agents) != 0 {
		t.Errorf("agent leaked to another user: %+v", agents)
	}

	release()
	release() // idempotent
	if _, ok := hub.Lookup("agent-a"); ok {
		t.Error("agent still visible after release")
	}
	if _, err := hub.DialStream(context.Background(), "agent-a"); !errors.Is(err, ErrNoAgent) {
		t.Errorf("DialStream after release = %v, want ErrNoAgent", err)
	}
}

func TestHubLeastLoadedSpread(t *testing.T) {
	hub := NewHub()
	info := AgentInfo{UserID: "user-1", Port: 17380}
	s0, s1 := sessionPair(t), sessionPair(t)
	hub.Register("agent-a", 0, info, s0)
	hub.Register("agent-a", 1, info, s1)

	var streams []net.Conn
	for i := 0; i < 4; i++ {
		st, err := hub.DialStream(context.Background(), "agent-a")
		if err != nil {
			t.Fatalf("DialStream %d: %v", i, err)
		}
		streams = append(streams, st)
	}
	defer func() {
		for _, st := range streams {
			_ = st.Close()
		}
	}()
	if s0.NumStreams() != 2 || s1.NumStreams() != 2 {
		t.Errorf("streams not spread least-loaded: %d/%d, want 2/2", s0.NumStreams(), s1.NumStreams())
	}
}

func TestHubReapsDeadTunnels(t *testing.T) {
	hub := NewHub()
	info := AgentInfo{UserID: "user-1", Port: 17380}
	s0, s1 := sessionPair(t), sessionPair(t)
	hub.Register("agent-a", 0, info, s0)
	hub.Register("agent-a", 1, info, s1)

	_ = s0.Close()
	if got := hub.Tunnels("agent-a"); got != 1 {
		t.Errorf("Tunnels = %d after one death, want 1", got)
	}
	st, err := hub.DialStream(context.Background(), "agent-a")
	if err != nil {
		t.Fatalf("DialStream with a survivor: %v", err)
	}
	_ = st.Close()

	_ = s1.Close()
	if _, err := hub.DialStream(context.Background(), "agent-a"); !errors.Is(err, ErrNoAgent) {
		t.Errorf("DialStream with all dead = %v, want ErrNoAgent", err)
	}
	if _, ok := hub.Lookup("agent-a"); ok {
		t.Error("Lookup reports a fully dead agent (fabricated liveness)")
	}
	if got := len(hub.AgentsForUser("user-1")); got != 0 {
		t.Errorf("dead agent still listed for user: %d", got)
	}
}

func TestHubReRegistrationReplacesSameSlot(t *testing.T) {
	hub := NewHub()
	info := AgentInfo{UserID: "user-1", Port: 17380}
	stale := sessionPair(t)
	hub.Register("agent-a", 0, info, stale)
	fresh := sessionPair(t)
	hub.Register("agent-a", 0, info, fresh)

	if !stale.IsClosed() {
		t.Error("stale predecessor in the same slot was not closed")
	}
	if got := hub.Tunnels("agent-a"); got != 1 {
		t.Errorf("Tunnels = %d after same-slot re-registration, want 1", got)
	}
	// A different slot joins the pool instead of replacing.
	hub.Register("agent-a", 1, info, sessionPair(t))
	if got := hub.Tunnels("agent-a"); got != 2 {
		t.Errorf("Tunnels = %d with two slots, want 2", got)
	}
}

func TestHubExpiredContext(t *testing.T) {
	hub := NewHub()
	hub.Register("agent-a", 0, AgentInfo{UserID: "user-1"}, sessionPair(t))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := hub.DialStream(ctx, "agent-a"); !errors.Is(err, context.Canceled) {
		t.Errorf("DialStream with canceled ctx = %v", err)
	}
}

func TestHubConcurrency(t *testing.T) {
	hub := NewHub()
	info := AgentInfo{UserID: "user-1", Port: 17380}
	hub.Register("agent-a", 0, info, sessionPair(t))
	hub.Register("agent-a", 1, info, sessionPair(t))

	var wg sync.WaitGroup
	// Dialers open and close streams...
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				st, err := hub.DialStream(context.Background(), "agent-a")
				if err != nil {
					// The churner below may briefly leave only busy pools;
					// ErrNoAgent must not happen for slots 0/1, which stay up.
					t.Errorf("DialStream: %v", err)
					return
				}
				_ = st.Close()
			}
		}()
	}
	// ...while extra tunnels churn in and out of the pool...
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				release := hub.Register("agent-a", slot, info, sessionPair(t))
				time.Sleep(time.Millisecond)
				release()
			}
		}(2 + g)
	}
	// ...and readers snapshot state.
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				hub.Lookup("agent-a")
				hub.AgentsForUser("user-1")
				hub.Tunnels("agent-a")
			}
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("concurrency test deadlocked")
	}
	if got := hub.Tunnels("agent-a"); got != 2 {
		t.Errorf("Tunnels = %d after churn settled, want the 2 stable slots", got)
	}
}
