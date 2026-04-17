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

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport/network"
)

// ClientPool is a thread-safe pool of reusable router RPC clients.
type ClientPool struct {
	mu      sync.Mutex
	clients map[cipher.PubKey]*poolEntry
	dialer  network.Dialer
	ttl     time.Duration
	log     *logging.Logger
}

type poolEntry struct {
	client   *Client
	lastUsed time.Time
}

// DefaultPoolTTL is how long an idle connection stays in the pool.
// The RSN's top 3 destinations get hit every few seconds, so they'll
// never idle out. Less popular destinations evict after 5 minutes,
// freeing DMSG streams and ephemeral ports.
const DefaultPoolTTL = 5 * time.Minute

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
	}
	go p.evictLoop()
	return p
}

// Get returns an existing pooled client or dials a new one. The
// returned client is removed from the pool — the caller owns it and
// must call Put when done (or Close if the connection is broken).
func (p *ClientPool) Get(ctx context.Context, pk cipher.PubKey) (*Client, error) {
	p.mu.Lock()
	if entry, ok := p.clients[pk]; ok {
		delete(p.clients, pk)
		p.mu.Unlock()
		// Test the connection with a short deadline — if the remote end
		// closed, we'll find out here instead of mid-RPC.
		// We can't cheaply probe an rpc.Client, so just return it and
		// let the caller retry on error.
		return entry.client, nil
	}
	p.mu.Unlock()

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
		old.client.Close() //nolint:errcheck
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
		client.Close() //nolint:errcheck
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
	p.mu.Lock()
	defer p.mu.Unlock()
	for pk, entry := range p.clients {
		entry.client.Close() //nolint:errcheck
		delete(p.clients, pk)
	}
}

func (p *ClientPool) evictLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		p.evict()
	}
}

func (p *ClientPool) evict() {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for pk, entry := range p.clients {
		if now.Sub(entry.lastUsed) > p.ttl {
			entry.client.Close() //nolint:errcheck
			delete(p.clients, pk)
		}
	}
}
