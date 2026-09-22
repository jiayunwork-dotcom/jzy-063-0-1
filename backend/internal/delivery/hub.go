// Package delivery owns live client connections (long-polling and WebSocket),
// decides which revision a concrete instance is entitled to according to the
// active canary rule, and computes real-time gray-release statistics.
package delivery

import (
	"sync"
	"time"

	"configplat/internal/canary"
	"configplat/internal/model"
)

// waitTimeout is the maximum time a long-poll connection stays suspended.
const waitTimeout = 30 * time.Second

// connKey identifies one subscription scope.
type connKey struct {
	tenantID    int64
	namespaceID int64
	env         string
}

// Conn is one live subscription (long-poll waiter or websocket client).
type Conn struct {
	ID              string
	IP              string
	Key             connKey
	CurrentRevision int64
	ConnectedAt     time.Time

	// wake is closed (once) when a relevant change arrives; long-poll
	// handlers select on it. Replaced for re-arm after a spurious wake.
	wake chan struct{}

	mu     sync.Mutex
	closed bool
}

func newConn(id, ip string, key connKey, rev int64) *Conn {
	return &Conn{
		ID: id, IP: ip, Key: key, CurrentRevision: rev,
		ConnectedAt: time.Now(), wake: make(chan struct{}),
	}
}

func (c *Conn) notify() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		close(c.wake)
		c.closed = true
	}
}

// Wake returns the channel that is closed on a relevant change.
func (c *Conn) Wake() <-chan struct{} { return c.wake }

// Hub tracks every live subscription in this process.
type Hub struct {
	mu sync.Mutex
	// scope key -> conn id -> conn
	conns map[connKey]map[string]*Conn
	idSeq int64
}

// NewHub creates an empty connection hub.
func NewHub() *Hub {
	return &Hub{conns: map[connKey]map[string]*Conn{}}
}

// Register adds a connection. release() removes it.
func (h *Hub) Register(ip string, tenantID, namespaceID int64, env string, currentRev int64) (*Conn, func()) {
	h.mu.Lock()
	h.idSeq++
	id := numID(h.idSeq)
	key := connKey{tenantID, namespaceID, env}
	c := newConn(id, ip, key, currentRev)
	if h.conns[key] == nil {
		h.conns[key] = map[string]*Conn{}
	}
	h.conns[key][id] = c
	h.mu.Unlock()
	return c, func() {
		h.mu.Lock()
		if set, ok := h.conns[key]; ok {
			delete(set, id)
			if len(set) == 0 {
				delete(h.conns, key)
			}
		}
		h.mu.Unlock()
	}
}

// Notify wakes every connection in a scope. The caller (service) decides
// which of them actually receives a new revision; sleeping canary-untargeted
// connections simply re-check and keep waiting.
func (h *Hub) Notify(tenantID, namespaceID int64, env string) {
	h.mu.Lock()
	set := append([]*Conn(nil), h.conns[connKey{tenantID, namespaceID, env}]...)
	h.mu.Unlock()
	for _, c := range set {
		c.notify()
	}
}

// Connections returns a snapshot of connections for a scope.
func (h *Hub) Connections(tenantID, namespaceID int64, env string) []*Conn {
	h.mu.Lock()
	defer h.mu.Unlock()
	set := h.conns[connKey{tenantID, namespaceID, env}]
	out := make([]*Conn, 0, len(set))
	for _, c := range set {
		out = append(out, c)
	}
	return out
}

// CountConnections reports live connection counts per environment and total
// for a namespace.
func (h *Hub) CountConnections(tenantID, namespaceID int64) map[string]int {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := map[string]int{}
	total := 0
	for k, set := range h.conns {
		if k.tenantID == tenantID && k.namespaceID == namespaceID {
			out[k.env] += len(set)
			total += len(set)
		}
	}
	out["*"] = total
	return out
}

// CanaryStats computes live gray-release stats: delivered = connected
// instances currently entitled to the canary revision, total = all connected
// instances in scope.
func (h *Hub) CanaryStats(tenantID, namespaceID int64, env string, r *model.Release) model.CanaryStats {
	conns := h.Connections(tenantID, namespaceID, env)
	var st model.CanaryStats
	st.Total = len(conns)
	seed := CanarySeed(r)
	for _, c := range conns {
		if canary.Match(c.IP, r.Canary, seed) {
			st.Delivered++
		}
	}
	return st
}

// CanarySeed is the stable seed used for percentage selection of a release.
func CanarySeed(r *model.Release) int64 {
	return r.NamespaceID*1_000_003 + r.Revision
}

// ServedRevision applies the canary rule: returns the revision this instance
// IP is currently entitled to, plus whether it differs from stable.
func ServedRevision(r *model.Release, ip string) int64 {
	if r == nil {
		return 0
	}
	if r.Status != model.CanaryActive {
		return r.Revision
	}
	if canary.Match(ip, r.Canary, CanarySeed(r)) {
		return r.Revision
	}
	if r.StableRevision > 0 {
		return r.StableRevision
	}
	return r.Revision - 1
}

func numID(n int64) string {
	const digits = "0123456789abcdef"
	var b [16]byte
	i := len(b)
	if n == 0 {
		return "0"
	}
	for n > 0 {
		i--
		b[i] = digits[n%16]
		n /= 16
	}
	return string(b[i:])
}
