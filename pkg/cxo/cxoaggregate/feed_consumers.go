// Package cxoaggregate pkg/cxo/cxoaggregate/feed_consumers.go c4-net-discovery
//
// In-process delivery of registrations a visor carries on its system feed.
// A service that runs in the same process as the transport discovery
// registers a FeedConsumer here, and TPD's aggregator, which already holds a
// subscription to every visor's system feed, hands each Root's registration
// leaves to it through a Hub. The visor then needs no CXO feed and no conn of
// its own for that service. See docs/design/cxo-one-feed-per-visor.md.
package cxoaggregate

import (
	"hash/fnv"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
	"github.com/skycoin/skywire/pkg/cxo/sysfeed"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
)

// Leaves looks up one of a feed's top-level leaves by name.
type Leaves func(name string) ([]byte, bool)

// PackLeaves reads n's direct leaves out of pack.
func PackLeaves(pack registry.Pack, n *treestore.TreeNode) Leaves {
	return func(name string) ([]byte, bool) { return LeafByName(pack, n, name) }
}

// FeedConsumer ingests one service's registrations from visors' system feeds.
type FeedConsumer struct {
	// Service is the sysfeed name of the service (sysfeed.DmsgDiscovery, ...).
	Service string
	// Consume takes one visor's leaves for this service, looked up by the
	// names the service uses on its own feed. reporter is the visor.
	Consume func(reporter cipher.PubKey, leaf Leaves)
}

type consumerEntry struct{ c FeedConsumer }

var consumers struct {
	mu sync.RWMutex
	m  map[string]*consumerEntry
}

// RegisterFeedConsumer makes c receive registrations from the system feeds
// this process aggregates. The returned func undoes it.
func RegisterFeedConsumer(c FeedConsumer) (unregister func()) {
	e := &consumerEntry{c: c}
	consumers.mu.Lock()
	if consumers.m == nil {
		consumers.m = make(map[string]*consumerEntry)
	}
	consumers.m[c.Service] = e
	consumers.mu.Unlock()
	return func() {
		consumers.mu.Lock()
		if consumers.m[c.Service] == e {
			delete(consumers.m, c.Service)
		}
		consumers.mu.Unlock()
	}
}

// FeedConsumerServices lists the services registered in this process.
func FeedConsumerServices() []string {
	consumers.mu.RLock()
	defer consumers.mu.RUnlock()
	out := make([]string, 0, len(consumers.m))
	for s := range consumers.m {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func consumerList() []FeedConsumer {
	consumers.mu.RLock()
	defer consumers.mu.RUnlock()
	out := make([]FeedConsumer, 0, len(consumers.m))
	for _, e := range consumers.m {
		out = append(out, e.c)
	}
	return out
}

// HubRefresh is how often an unchanged registration is delivered again. The
// services refresh an entry's TTL on every delivery, as they do on every
// heartbeat Root of a visor's own feed (treestore heartbeatInterval, 45 s).
const HubRefresh = 40 * time.Second

// Hub delivers system-feed registration leaves to the registered consumers.
// The same Root can arrive through the targeted fetch and the full fill, and a
// busy feed publishes far more often than registrations change, so a service
// sees a visor's leaves when they change or HubRefresh after the last time.
// The zero value is ready to use.
type Hub struct {
	mu        sync.Mutex
	last      map[hubKey]hubSent
	delivered atomic.Uint64
}

type hubKey struct {
	reporter cipher.PubKey
	service  string
}

type hubSent struct {
	sum uint64
	at  time.Time
}

// Active reports whether any consumer is registered, so a caller can skip
// reading the leaves at all.
func (h *Hub) Active() bool {
	consumers.mu.RLock()
	defer consumers.mu.RUnlock()
	return len(consumers.m) > 0
}

// Deliver hands reporter's registration leaves, keyed by their full names on
// the system feed, to each registered consumer they are for.
func (h *Hub) Deliver(reporter cipher.PubKey, leaves map[string][]byte) {
	if len(leaves) == 0 || reporter.Null() {
		return
	}
	for _, c := range consumerList() {
		prefix := sysfeed.PrefixOf(c.Service)
		if prefix == "" {
			continue
		}
		own := make(map[string][]byte)
		for name, b := range leaves {
			if strings.HasPrefix(name, prefix) {
				own[strings.TrimPrefix(name, prefix)] = b
			}
		}
		if len(own) == 0 || !h.due(hubKey{reporter, c.Service}, sumLeaves(own), time.Now()) {
			continue
		}
		h.delivered.Add(1)
		c.Consume(reporter, func(name string) ([]byte, bool) {
			b, ok := own[name]
			return b, ok
		})
	}
}

// due marks k delivered now unless the same leaves went out within HubRefresh.
func (h *Hub) due(k hubKey, sum uint64, now time.Time) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.last == nil {
		h.last = make(map[hubKey]hubSent)
	}
	if prev, ok := h.last[k]; ok && prev.sum == sum && now.Sub(prev.at) < HubRefresh {
		return false
	}
	h.last[k] = hubSent{sum: sum, at: now}
	return true
}

// Forget drops what the hub remembers of reporter, whose feed is gone.
func (h *Hub) Forget(reporter cipher.PubKey) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for k := range h.last {
		if k.reporter == reporter {
			delete(h.last, k)
		}
	}
}

// Delivered counts the deliveries made.
func (h *Hub) Delivered() uint64 { return h.delivered.Load() }

func sumLeaves(m map[string][]byte) uint64 {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	f := fnv.New64a()
	for _, n := range names {
		f.Write([]byte(n))    //nolint:errcheck,gosec
		f.Write([]byte{0})    //nolint:errcheck,gosec
		f.Write(m[n])         //nolint:errcheck,gosec
		f.Write([]byte{0xff}) //nolint:errcheck,gosec
	}
	return f.Sum64()
}

// RegistrationLeaves returns n's top-level registration leaves. Like the
// other helpers it reads an unfetched child as absent.
func RegistrationLeaves(pack registry.Pack, n *treestore.TreeNode) map[string][]byte {
	count, err := n.Children.Len(pack)
	if err != nil {
		return nil
	}
	var out map[string][]byte
	for i := 0; i < count; i++ {
		var entry treestore.TreeEntry
		if _, err := n.Children.ValueByIndex(pack, i, &entry); err != nil {
			continue
		}
		if len(entry.Leaf) == 0 || !sysfeed.IsRegistration(entry.Name) {
			continue
		}
		if out == nil {
			out = make(map[string][]byte)
		}
		out[entry.Name] = entry.Leaf
	}
	return out
}
