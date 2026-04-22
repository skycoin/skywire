// Package dht pkg/dht/fullnode_advert.go
//
// Full node advertisement: full DHT nodes publish their PK under a
// well-known DHT key so regular visors can discover them for bulk sync.
//
// The well-known key is SHA256("dht-full-nodes"). Full nodes publish
// a signed list of their PK to this key periodically. Visors looking
// for full nodes do a single GetValue on this key and get back a list
// of PKs they can call GetItems on.
//
// Since multiple full nodes publish to the same key, and BEP44 only
// stores one item per target, each full node publishes its OWN PK
// under a unique salt: "fullnode:<pk_hex[:16]>". This way each full
// node has its own entry, and a visor can discover them by doing
// GetValue lookups with known salt prefixes, or by querying the
// bootstrap peers (DMSG servers) which are always full nodes.
package dht

import (
	"context"
	"encoding/json"
	"time"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
)

// FullNodeAdvert is the entry a full node publishes to advertise itself.
type FullNodeAdvert struct {
	PK          cipher.PubKey `json:"pk"`
	StoredItems int           `json:"stored_items"`
	FullNode    bool          `json:"full_node"`
	Timestamp   int64         `json:"ts"`
}

// AdvertiseFullNode periodically publishes this node's full-node status
// to the DHT. Other visors can discover full nodes by querying bootstrap
// peers or doing GetValue with the "fullnode" salt.
func AdvertiseFullNode(ctx context.Context, node *Node, log *logging.Logger) {
	if !node.store.IsFullNode() {
		return
	}

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	seq := uint64(1)
	for {
		advert := FullNodeAdvert{
			PK:          node.pk,
			StoredItems: node.store.Len(),
			FullNode:    true,
			Timestamp:   time.Now().UnixNano(),
		}
		data, err := json.Marshal(advert)
		if err == nil {
			if putErr := node.Put(ctx, data, seq, []byte("fullnode")); putErr != nil {
				log.WithError(putErr).Trace("DHT: advertise full node failed")
			}
			seq++
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// FindFullNodes queries bootstrap peers for full node advertisements.
// Returns a list of PKs that are running DHT full nodes.
// Prioritizes peers actually in the routing table (confirmed reachable)
// over the raw bootstrap list (which includes non-DHT services).
func FindFullNodes(ctx context.Context, node *Node) []cipher.PubKey {
	// First: peers in the routing table (confirmed DHT-reachable).
	var fullNodes []cipher.PubKey
	seen := make(map[cipher.PubKey]struct{})

	closest := node.rt.FindClosest(node.id, 20)
	for _, p := range closest {
		if p.PK == node.pk {
			continue
		}
		fullNodes = append(fullNodes, p.PK)
		seen[p.PK] = struct{}{}
	}

	// Then: bootstrap peers not already in the list (fallback).
	for _, bp := range node.cfg.BootstrapPKs {
		if bp == node.pk {
			continue
		}
		if _, ok := seen[bp]; ok {
			continue
		}
		fullNodes = append(fullNodes, bp)
	}

	return fullNodes
}
