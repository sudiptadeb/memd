package tunnel

import (
	"context"
	"errors"
	"net"
	"sort"
	"sync"
	"time"

	"github.com/xtaci/smux"
)

// ErrNoAgent is returned by DialStream when the agent has no live tunnel.
var ErrNoAgent = errors.New("tunnel: agent is not connected")

// AgentID identifies one agent: the hex sha256 of its tunnel token.
type AgentID string

// AgentInfo describes a connected agent as reported at registration.
type AgentInfo struct {
	UserID string // memd user the token was minted for
	Label  string // human label from the agent (-rc-label)
	Port   int    // local termulaa port the agent splices to
}

// AgentStatus is a live snapshot for the /rc page. Tunnels is the number of
// live smux sessions in the pool right now — it is measured, never assumed:
// an agent with zero live tunnels is simply absent from listings.
type AgentStatus struct {
	ID          AgentID
	Info        AgentInfo
	Tunnels     int
	ConnectedAt time.Time // earliest live tunnel's registration time
}

// Hub tracks the pool of live smux sessions (tunnels) per agent and opens
// streams on them for the proxy. All methods are safe for concurrent use.
// Liveness is direct: sessions live in-process, closed ones are skipped and
// reaped on the spot, so lookups never report stale last-known-good state.
type Hub struct {
	mu     sync.Mutex
	agents map[AgentID]*agentState
}

type agentState struct {
	info    AgentInfo
	members []*member
}

type member struct {
	index int // agent-assigned 0-based slot within its pool
	sess  *smux.Session
	since time.Time
}

// NewHub returns an empty hub.
func NewHub() *Hub {
	return &Hub{agents: make(map[AgentID]*agentState)}
}

// Register adds one tunnel to the agent's pool and returns its release
// function. Pool members register and release independently; the agent's info
// is refreshed by each registration. Per the rc protocol (§8), a registration
// for a (token, session-index) pair replaces any stale predecessor in that
// slot: the old session is closed and dropped immediately rather than waiting
// for its keepalive to notice death. Release is idempotent.
func (h *Hub) Register(id AgentID, sessionIndex int, info AgentInfo, sess *smux.Session) (release func()) {
	m := &member{index: sessionIndex, sess: sess, since: time.Now()}
	h.mu.Lock()
	state := h.agents[id]
	if state == nil {
		state = &agentState{}
		h.agents[id] = state
	}
	state.info = info
	live := state.members[:0]
	for _, other := range state.members {
		if other.index == sessionIndex {
			_ = other.sess.Close()
			continue
		}
		live = append(live, other)
	}
	state.members = append(live, m)
	h.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			state, ok := h.agents[id]
			if !ok {
				return
			}
			for i, other := range state.members {
				if other == m {
					state.members = append(state.members[:i], state.members[i+1:]...)
					break
				}
			}
			if len(state.members) == 0 {
				delete(h.agents, id)
			}
		})
	}
}

// DialStream opens one smux stream to the agent, choosing the least-loaded
// live tunnel in the pool. Dead tunnels are reaped as they are encountered;
// if a chosen tunnel fails mid-dial the next candidate is tried, so a pool
// running below strength keeps serving. Returns ErrNoAgent when no live
// tunnel remains.
func (h *Hub) DialStream(ctx context.Context, id AgentID) (net.Conn, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		m := h.pickLeastLoaded(id)
		if m == nil {
			return nil, ErrNoAgent
		}
		stream, err := m.sess.OpenStream()
		if err != nil {
			// The session died between selection and open (or exhausted its
			// stream ids). Retire it and try the next survivor.
			_ = m.sess.Close()
			continue
		}
		return stream, nil
	}
}

// pickLeastLoaded returns the live pool member with the fewest open streams,
// pruning closed members along the way.
func (h *Hub) pickLeastLoaded(id AgentID) *member {
	h.mu.Lock()
	defer h.mu.Unlock()
	state, ok := h.agents[id]
	if !ok {
		return nil
	}
	h.pruneLocked(id, state)
	var best *member
	bestLoad := 0
	for _, m := range state.members {
		load := m.sess.NumStreams()
		if best == nil || load < bestLoad {
			best, bestLoad = m, load
		}
	}
	return best
}

// Lookup reports the agent's registration info if it has at least one live
// tunnel. It never reports an agent that is not connected right now.
func (h *Hub) Lookup(id AgentID) (AgentInfo, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	state, ok := h.agents[id]
	if !ok {
		return AgentInfo{}, false
	}
	h.pruneLocked(id, state)
	if len(state.members) == 0 {
		return AgentInfo{}, false
	}
	return state.info, true
}

// Tunnels reports the number of live tunnels currently in the agent's pool.
func (h *Hub) Tunnels(id AgentID) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	state, ok := h.agents[id]
	if !ok {
		return 0
	}
	h.pruneLocked(id, state)
	return len(state.members)
}

// AgentsForUser lists the user's currently connected agents for the /rc page,
// ordered by label then id for stable rendering.
func (h *Hub) AgentsForUser(userID string) []AgentStatus {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []AgentStatus
	for id, state := range h.agents {
		if state.info.UserID != userID {
			continue
		}
		h.pruneLocked(id, state)
		if len(state.members) == 0 {
			continue
		}
		status := AgentStatus{ID: id, Info: state.info, Tunnels: len(state.members)}
		for _, m := range state.members {
			if status.ConnectedAt.IsZero() || m.since.Before(status.ConnectedAt) {
				status.ConnectedAt = m.since
			}
		}
		out = append(out, status)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Info.Label != out[j].Info.Label {
			return out[i].Info.Label < out[j].Info.Label
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// pruneLocked drops closed tunnels from the pool and removes the agent when
// none remain. Callers must hold h.mu.
func (h *Hub) pruneLocked(id AgentID, state *agentState) {
	live := state.members[:0]
	for _, m := range state.members {
		if !m.sess.IsClosed() {
			live = append(live, m)
		}
	}
	state.members = live
	if len(state.members) == 0 {
		delete(h.agents, id)
	}
}
