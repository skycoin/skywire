// Package dmsg pkg/dmsg/dmsg/peer_miss.go
package dmsg

import (
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// peerMissTTL is how long a destination that no peer holds is refused without
// asking the peers again. Offline peers are redialed by every visor that names
// them (a hypervisor, a WebRTC signaling peer), and a server otherwise opens
// a stream on each of its peers for each of those requests. A client that
// connects to a peer inside the window is reachable once it passes, or at once
// through its own delegated servers.
const peerMissTTL = 30 * time.Second

// peerMissCap bounds the cache. Expired entries are pruned when it fills; if
// every entry is still live, new misses are not recorded.
const peerMissCap = 8192

// peerMissed reports whether every peer reported dst absent within peerMissTTL.
func (c *EntityCommon) peerMissed(dst cipher.PubKey) bool {
	c.peerMissMx.Lock()
	defer c.peerMissMx.Unlock()
	until, ok := c.peerMiss[dst]
	if !ok {
		return false
	}
	if time.Now().After(until) {
		delete(c.peerMiss, dst)
		return false
	}
	return true
}

// notePeerMiss records that every peer reported dst absent.
func (c *EntityCommon) notePeerMiss(dst cipher.PubKey) {
	now := time.Now()
	c.peerMissMx.Lock()
	defer c.peerMissMx.Unlock()
	if c.peerMiss == nil {
		c.peerMiss = make(map[cipher.PubKey]time.Time)
	}
	if _, ok := c.peerMiss[dst]; !ok && len(c.peerMiss) >= peerMissCap {
		for pk, until := range c.peerMiss {
			if now.After(until) {
				delete(c.peerMiss, pk)
			}
		}
		if len(c.peerMiss) >= peerMissCap {
			return
		}
	}
	c.peerMiss[dst] = now.Add(peerMissTTL)
}

// clearPeerMiss forgets dst after a peer carried a request to it.
func (c *EntityCommon) clearPeerMiss(dst cipher.PubKey) {
	c.peerMissMx.Lock()
	delete(c.peerMiss, dst)
	c.peerMissMx.Unlock()
}
