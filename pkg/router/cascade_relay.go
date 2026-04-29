// Package router pkg/router/cascade_relay.go
//
// RSN relay: allows visors to reach the Route Setup Node through
// transport neighbors that have a direct "setup"-labeled transport
// to the RSN. The relay uses route ID 0 CascadeSetup/CascadeAck
// packets, piggybacking on the existing cascade infrastructure.
//
// The relay mechanism works as follows:
// 1. Visor discovers RSN relay peers (cached from previous RSN responses)
// 2. Visor checks if it transports any relay peer
// 3. If yes, sends the setup RPC request to that peer on route ID 0
// 4. The relay peer forwards to the RSN over its "setup" transport
// 5. Response flows back the same path
//
// This is Phase 2 of the cascade protocol spec.
package router

import (
	"context"
	"fmt"
	"sync"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
)

// RSNRelayCache caches relay peer lists received from the RSN.
// Used by visors to find a transport neighbor that can relay to the RSN.
type RSNRelayCache struct {
	mu    sync.RWMutex
	peers map[cipher.PubKey][]cipher.PubKey // RSN PK → relay peer PKs
	log   *logging.Logger
}

// NewRSNRelayCache creates an empty relay cache.
func NewRSNRelayCache(log *logging.Logger) *RSNRelayCache {
	return &RSNRelayCache{
		peers: make(map[cipher.PubKey][]cipher.PubKey),
		log:   log,
	}
}

// Update stores or refreshes the relay peer list for an RSN.
func (rc *RSNRelayCache) Update(rsnPK cipher.PubKey, relayPeers []cipher.PubKey) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.peers[rsnPK] = relayPeers
	rc.log.WithField("rsn", rsnPK.String()).
		WithField("relay_peers", len(relayPeers)).
		Debug("Updated RSN relay peer cache")
}

// Get returns the cached relay peers for an RSN.
func (rc *RSNRelayCache) Get(rsnPK cipher.PubKey) []cipher.PubKey {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	return rc.peers[rsnPK]
}

// FindRelayTransport finds a transport to a relay peer that can reach the RSN.
// Returns the transport and relay peer PK, or an error if no relay path exists.
func (rc *RSNRelayCache) FindRelayTransport(
	ctx context.Context,
	rsnPK cipher.PubKey,
	tm *transport.Manager,
) (*transport.ManagedTransport, cipher.PubKey, error) {
	relayPeers := rc.Get(rsnPK)
	if len(relayPeers) == 0 {
		return nil, cipher.PubKey{}, fmt.Errorf("no cached relay peers for RSN %s", rsnPK.String())
	}

	// Check if we directly transport any relay peer.
	for _, relayPK := range relayPeers {
		var found *transport.ManagedTransport
		tm.WalkTransports(func(tp *transport.ManagedTransport) bool {
			if tp.Remote() == relayPK && !tp.IsClosed() {
				found = tp
				return false // stop walking
			}
			return true
		})
		if found != nil {
			return found, relayPK, nil
		}
	}

	return nil, cipher.PubKey{}, fmt.Errorf("no transport to any RSN relay peer (checked %d peers)", len(relayPeers))
}

// FindDirectRSNTransport checks if we have a direct "setup"-labeled transport to the RSN.
func FindDirectRSNTransport(rsnPK cipher.PubKey, tm *transport.Manager) *transport.ManagedTransport {
	var found *transport.ManagedTransport
	tm.WalkTransports(func(tp *transport.ManagedTransport) bool {
		if tp.Remote() == rsnPK && tp.Entry.Label == transport.LabelSetup && !tp.IsClosed() {
			found = tp
			return false
		}
		return true
	})
	return found
}
