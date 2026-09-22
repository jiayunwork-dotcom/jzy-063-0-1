package pubsub

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/redis/go-redis/v9"

	"configplat/internal/model"
)

// Redis is a Bus backed by one Redis Pub/Sub connection. A single process
// subscribes once to a fanout channel; each backend Publish goes through
// Redis so all replicas receive it.
type Redis struct {
	client *redis.Client
	ctx    context.Context
	cancel context.CancelFunc

	mu       sync.Mutex
	channels map[string]map[int64]chan model.ChangeEvent
	psub     *redis.PubSub
	nextID   int64

	stopOnce sync.Once
}

const fanoutPattern = "configplat:change:*"

// NewRedis connects to redis (e.g. "redis:6379"), verifies reachability and
// starts the single fanout receiver.
func NewRedis(parent context.Context, addr, password string, db int) (*Redis, error) {
	client := redis.NewClient(&redis.Options{Addr: addr, Password: password, DB: db})
	if err := client.Ping(parent).Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	r := &Redis{
		client:   client,
		ctx:      ctx,
		cancel:   cancel,
		channels: map[string]map[int64]chan model.ChangeEvent{},
		psub:     client.PSubscribe(ctx, fanoutPattern),
	}
	go r.receiveLoop()
	return r, nil
}

func (r *Redis) receiveLoop() {
	ch := r.psub.Channel()
	for {
		select {
		case <-r.ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			var ev model.ChangeEvent
			if err := json.Unmarshal([]byte(msg.Payload), &ev); err != nil {
				continue
			}
			r.dispatch(msg.Channel, ev)
		}
	}
}

func (r *Redis) dispatch(chanName string, ev model.ChangeEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.channels[chanName] {
		select {
		case c <- ev:
		default:
		}
	}
}

// Publish sends the event through Redis so every replica gets it.
func (r *Redis) Publish(ctx context.Context, ev model.ChangeEvent) error {
	if r.ctx.Err() != nil {
		return errors.New("pubsub 已关闭")
	}
	key := channelKey(ev.TenantID, ev.NamespaceID, ev.Env)
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	return r.client.Publish(ctx, key, b).Err()
}

func (r *Redis) Subscribe(_ context.Context, tenantID, namespaceID int64, env string) (<-chan model.ChangeEvent, func()) {
	key := channelKey(tenantID, namespaceID, env)
	ch := make(chan model.ChangeEvent, 16)
	r.mu.Lock()
	r.nextID++
	id := r.nextID
	if r.channels[key] == nil {
		r.channels[key] = map[int64]chan model.ChangeEvent{}
	}
	r.channels[key][id] = ch
	r.mu.Unlock()
	cancel := func() {
		r.mu.Lock()
		if set, ok := r.channels[key]; ok {
			if c, ok2 := set[id]; ok2 {
				delete(set, id)
				close(c)
			}
		}
		r.mu.Unlock()
	}
	return ch, cancel
}

func (r *Redis) Close() error {
	r.stopOnce.Do(func() {
		r.cancel()
		_ = r.psub.Close()
		_ = r.client.Close()
	})
	return nil
}
