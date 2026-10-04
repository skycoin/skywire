// Package visor pkg/visor/skynet_relay_graph.go c3-vis-core
package visor

import (
	"context"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
)

// relayGraphTTL is how long a destination's transport-graph answer is reused.
// Every dmsg-addressed fetch to a peer with no direct transport asks for it
// before dialing, and the TPD client sends one request at a time, so a page
// making a dozen fetches at once (the wallet's node queries) had them wait in
// line behind a TPD query each — past the browser's timeout, in a tab.
const relayGraphTTL = 30 * time.Second

// relayGraphCache remembers, per destination, the peers the transport graph
// says hold a non-dmsg transport to it. Concurrent lookups for one
// destination share a single query; a failed query is not kept.
type relayGraphCache struct {
	mu sync.Mutex
	m  map[cipher.PubKey]*relayGraphEntry
}

type relayGraphEntry struct {
	done  chan struct{} // closed when the query has finished
	peers map[cipher.PubKey]struct{}
	err   error
	at    time.Time
}

var relayGraph = &relayGraphCache{m: make(map[cipher.PubKey]*relayGraphEntry)}

// peers returns remote's non-dmsg transport peers other than local.
func (c *relayGraphCache) peers(ctx context.Context, dc transport.DiscoveryClient, local, remote cipher.PubKey) (map[cipher.PubKey]struct{}, error) {
	c.mu.Lock()
	e := c.m[remote]
	if e != nil {
		select {
		case <-e.done:
			if e.err == nil && time.Since(e.at) < relayGraphTTL {
				c.mu.Unlock()
				return e.peers, nil
			}
			e = nil
		default:
		}
	}
	if e != nil {
		c.mu.Unlock()
		select {
		case <-e.done:
			return e.peers, e.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	e = &relayGraphEntry{done: make(chan struct{})}
	c.m[remote] = e
	c.pruneLocked()
	c.mu.Unlock()

	e.peers, e.err = queryRelayGraph(ctx, dc, local, remote)
	e.at = time.Now()
	close(e.done)
	return e.peers, e.err
}

// pruneLocked drops answers past their TTL once the map has grown, so a visor
// that fetches from many destinations does not keep every one.
func (c *relayGraphCache) pruneLocked() {
	if len(c.m) < 256 {
		return
	}
	for pk, e := range c.m {
		select {
		case <-e.done:
			if time.Since(e.at) >= relayGraphTTL {
				delete(c.m, pk)
			}
		default:
		}
	}
}

func queryRelayGraph(ctx context.Context, dc transport.DiscoveryClient, local, remote cipher.PubKey) (map[cipher.PubKey]struct{}, error) {
	qctx, cancel := context.WithTimeout(ctx, relayDiscoveryTimeout)
	defer cancel()
	entries, err := dc.GetTransportsByEdge(qctx, remote)
	if err != nil {
		return nil, err
	}
	peers := make(map[cipher.PubKey]struct{}, len(entries))
	for _, e := range entries {
		if e.Type == "dmsg" {
			continue
		}
		if peer := e.RemoteEdge(remote); peer != remote && peer != local {
			peers[peer] = struct{}{}
		}
	}
	return peers, nil
}
