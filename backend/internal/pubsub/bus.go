// Package pubsub distributes namespace change events. In production it is
// backed by Redis 7 Pub/Sub so that several backend replicas see every event;
// a local in-process implementation is provided for tests.
package pubsub

import (
	"context"
	"sync"

	"configplat/internal/model"
)

// Bus is the change-event distribution boundary.
type Bus interface {
	// Publish fans an event out to every subscriber of its (tenant,ns,env).
	Publish(ctx context.Context, ev model.ChangeEvent) error
	// Subscribe registers a consumer. The returned channel is closed when
	// cancel is invoked.
	Subscribe(ctx context.Context, tenantID, namespaceID int64, env string) (<-chan model.ChangeEvent, func())
	Close() error
}

func channelKey(tenantID, namespaceID int64, env string) string {
	return "configplat:change:" + itoa(tenantID) + ":" + itoa(namespaceID) + ":" + env
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	var buf [20]byte
	i := len(buf)
	for n != 0 {
		d := byte(n % 10)
		if d < 0 {
			d = -d
		}
		buf[i-1] = '0' + d
		i--
		n /= 10
	}
	if neg {
		buf[i-1] = '-'
		i--
	}
	return string(buf[i:])
}

// Memory is an in-process Bus for unit tests.
type Memory struct {
	mu   sync.Mutex
	subs map[string]map[int64]chan model.ChangeEvent
	seq  int64
}

func NewMemory() *Memory {
	return &Memory{subs: map[string]map[int64]chan model.ChangeEvent{}}
}

func (m *Memory) Publish(_ context.Context, ev model.ChangeEvent) error {
	key := channelKey(ev.TenantID, ev.NamespaceID, ev.Env)
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ch := range m.subs[key] {
		select {
		case ch <- ev:
		default: // never block the publisher on a slow consumer
		}
	}
	return nil
}

func (m *Memory) Subscribe(_ context.Context, tenantID, namespaceID int64, env string) (<-chan model.ChangeEvent, func()) {
	key := channelKey(tenantID, namespaceID, env)
	ch := make(chan model.ChangeEvent, 16)
	m.mu.Lock()
	m.seq++
	id := m.seq
	if m.subs[key] == nil {
		m.subs[key] = map[int64]chan model.ChangeEvent{}
	}
	m.subs[key][id] = ch
	m.mu.Unlock()
	cancel := func() {
		m.mu.Lock()
		if set, ok := m.subs[key]; ok {
			if c, ok2 := set[id]; ok2 {
				delete(set, id)
				close(c)
			}
		}
		m.mu.Unlock()
	}
	return ch, cancel
}

func (m *Memory) Close() error { return nil }
