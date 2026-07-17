//go:build !tinygo || (js && wasm)

// Package router pkg/router/client_pool.go
//
// ClientPool keeps reusable RPC connections to remote visors so the
// route setup node doesn't pay a full DMSG noise handshake on every
// route setup request. Connections are keyed by remote PK and evicted
// after an idle TTL.
package router

import (
	"context"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport/network"
)

// ClientPool is a thread-safe pool of reusable router RPC clients.
type ClientPool struct {
	mu        sync.Mutex
	clients   map[cipher.PubKey]*poolEntry
	dialer    network.Dialer
	ttl       time.Duration
	log       *logging.Logger
	done      chan struct{}
	closeOnce sync.Once
}

type poolEntry struct {
	client   *Client
	lastUsed time.Time
}

// maxPooledReuseIdle is the longest a pooled connection may have idled and
// still be safely handed back for reuse. It sits just under
// streamIdleTimeoutForPool — the read deadline Client.call() arms on the
// underlying DMSG stream after every call. Once that deadline fires, the
// rpc.Client.input() goroutine times out, exits, and shuts the client down
// permanently; the next call on it returns "connection is shut down". Reusing
// such a corpse is exactly what failed id-reservation against perfectly
// reachable visors and wedged their destination circuit breakers on the live
// setup nodes. The 15s margin leaves a freshly-dequeued connection time to
// start its next call (which re-arms the deadline) before the old one fires.
const maxPooledReuseIdle = streamIdleTimeoutForPool - 15*time.Second

// DefaultPoolTTL is how long an idle connection stays in the pool before the
// eviction loop reaps it. It MUST stay at or below maxPooledReuseIdle: a
// connection that idles past the stream read deadline is already shut down, so
// keeping it pooled only risks Get handing out a dead connection. The RSN's
// hot destinations are hit every few seconds (lastUsed refreshed on Put) so
// they never idle out; colder ones re-dial fresh, which is correct since their
// pooled stream would have died anyway.
const DefaultPoolTTL = maxPooledReuseIdle

// NewClientPool creates a pool that reuses connections to remote visors.
func NewClientPool(dialer network.Dialer, ttl time.Duration) *ClientPool {
	if ttl <= 0 {
		ttl = DefaultPoolTTL
	}
	p := &ClientPool{
		clients: make(map[cipher.PubKey]*poolEntry),
		dialer:  dialer,
		ttl:     ttl,
		log:     logging.MustGetLogger("client_pool"),
		done:    make(chan struct{}),
	}
	go p.evictLoop()
	return p
}

// Get returns an existing pooled client or dials a new one. The
// returned client is removed from the pool — the caller owns it and
// must call Put when done (or Close if the connection is broken).
func (p *ClientPool) Get(ctx context.Context, pk cipher.PubKey) (*Client, error) {
	p.mu.Lock()
	entry, ok := p.clients[pk]
	if ok {
		delete(p.clients, pk)
	}
	p.mu.Unlock()

	if ok {
		// Only reuse a connection that cannot already be dead. Past
		// maxPooledReuseIdle the stream read deadline has fired, the
		// rpc.Client.input() goroutine has exited, and the next call would
		// return "connection is shut down" — failing id-reservation against a
		// reachable visor and tripping its destination circuit breaker. We
		// can't cheaply probe an rpc.Client, but idle time is a reliable proxy
		// for the deterministic shutdown horizon, so discard and dial fresh.
		if time.Since(entry.lastUsed) < maxPooledReuseIdle {
			return entry.client, nil
		}
		entry.client.Close() //nolint:errcheck,gosec
	}

	// Dial a new connection.
	return NewClient(ctx, p.dialer, pk)
}

// Put returns a client to the pool for reuse. If the pool already has
// a connection to this PK (race between concurrent requests to the
// same destination), the older one is closed.
func (p *ClientPool) Put(client *Client) {
	if client == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	pk := client.rPK
	if old, ok := p.clients[pk]; ok {
		old.client.Close() //nolint:errcheck,gosec
	}
	p.clients[pk] = &poolEntry{
		client:   client,
		lastUsed: time.Now(),
	}
}

// Discard closes a client without returning it to the pool. Use this
// when the connection is known to be broken (RPC error, context cancel).
func (p *ClientPool) Discard(client *Client) {
	if client != nil {
		client.Close() //nolint:errcheck,gosec
	}
}

// Size returns the number of pooled connections.
func (p *ClientPool) Size() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.clients)
}

// Close closes all pooled connections and stops the eviction loop.
func (p *ClientPool) Close() {
	// Stop the eviction loop (idempotent — Close may be called more than once).
	p.closeOnce.Do(func() { close(p.done) })
	p.mu.Lock()
	defer p.mu.Unlock()
	for pk, entry := range p.clients {
		entry.client.Close() //nolint:errcheck,gosec
		delete(p.clients, pk)
	}
}

func (p *ClientPool) evictLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-p.done:
			return
		case <-ticker.C:
			p.evict()
		}
	}
}

func (p *ClientPool) evict() {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for pk, entry := range p.clients {
		if now.Sub(entry.lastUsed) > p.ttl {
			entry.client.Close() //nolint:errcheck,gosec
			delete(p.clients, pk)
		}
	}
}
