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

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
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
//
// NOTE: this is a candidate list, not a verified one — peers in the
// routing table aren't guaranteed to be full nodes. For a verified
// list, use FindAdvertisedFullNodes (filters by signed advert in
// "fullnode" salt).
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

// fullNodeAdvertFreshness is the maximum advert age we accept when
// verifying a peer is a live full node. AdvertiseFullNode publishes
// every 5 minutes, so 30 minutes (6×) tolerates 5 missed publishes
// without unnecessarily forgetting a still-running peer.
const fullNodeAdvertFreshness = 30 * time.Minute

// FindAdvertisedFullNodes returns PKs that have a fresh, signed
// FullNodeAdvert in this node's DHT store under salt "fullnode". Each
// returned peer can be safely targeted by reconcile pushes — the
// signed advert proves the peer accepts the full-node responsibility
// of storing arbitrary mirror puts.
//
// Excludes this node's own PK and bootstrap PKs (caller is expected
// to merge with cfg.BootstrapPKs separately so the unioned list
// drives one loop).
func FindAdvertisedFullNodes(node *Node) []cipher.PubKey {
	if node == nil || node.store == nil {
		return nil
	}
	bootstrap := make(map[cipher.PubKey]struct{}, len(node.cfg.BootstrapPKs))
	for _, pk := range node.cfg.BootstrapPKs {
		bootstrap[pk] = struct{}{}
	}

	cutoff := time.Now().Add(-fullNodeAdvertFreshness).UnixNano()
	var out []cipher.PubKey
	seen := make(map[cipher.PubKey]struct{})
	var cursor uint64
	for {
		items, _, hasMore := node.store.GetItems("fullnode", cursor, 0)
		if len(items) == 0 {
			break
		}
		var maxSeq uint64
		for _, item := range items {
			if item.Seq > maxSeq {
				maxSeq = item.Seq
			}
			var advert FullNodeAdvert
			if err := json.Unmarshal(item.V, &advert); err != nil {
				continue
			}
			if !advert.FullNode {
				continue
			}
			if advert.Timestamp < cutoff {
				continue
			}
			if advert.PK == node.pk {
				continue
			}
			if _, ok := bootstrap[advert.PK]; ok {
				continue
			}
			if _, ok := seen[advert.PK]; ok {
				continue
			}
			seen[advert.PK] = struct{}{}
			out = append(out, advert.PK)
		}
		if !hasMore || maxSeq <= cursor {
			break
		}
		cursor = maxSeq
	}
	return out
}
