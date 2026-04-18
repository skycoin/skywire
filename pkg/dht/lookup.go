package dht

import (
	"context"
	"sort"
	"sync"
)

// Alpha is the Kademlia concurrency parameter — how many parallel
// RPCs are in flight during an iterative lookup.
const Alpha = 3

// iterativeFindNode performs a Kademlia iterative lookup for the K
// closest nodes to target. It returns the peers and, if found during
// a value lookup, the item.
func (n *Node) iterativeLookup(ctx context.Context, target NodeID, wantValue bool) ([]Peer, *MutableItem, error) {
	// Seed shortlist from local routing table.
	shortlist := n.rt.FindClosest(target, K)
	if len(shortlist) == 0 {
		return nil, nil, nil
	}

	queried := make(map[NodeID]bool)
	queried[n.id] = true

	var bestItem *MutableItem
	var mu sync.Mutex

	for {
		// Find unqueried peers in the shortlist.
		mu.Lock()
		var toQuery []Peer
		for _, p := range shortlist {
			if !queried[p.ID] {
				toQuery = append(toQuery, p)
			}
		}
		mu.Unlock()

		if len(toQuery) == 0 {
			break // all K closest have been queried
		}
		if len(toQuery) > Alpha {
			toQuery = toQuery[:Alpha]
		}

		// Query in parallel.
		type result struct {
			peers []Peer
			item  *MutableItem
		}
		results := make(chan result, len(toQuery))

		for _, p := range toQuery {
			queried[p.ID] = true
			go func(p Peer) {
				var res result
				if wantValue {
					resp, err := n.rpcGetValue(ctx, p, target)
					if err == nil {
						res.peers = resp.Closest
						if resp.Item != nil {
							res.item = resp.Item
						}
					}
				} else {
					resp, err := n.rpcFindNode(ctx, p, target)
					if err == nil {
						res.peers = resp.Closest
					}
				}
				results <- res
			}(p)
		}

		// Collect results.
		for i := 0; i < len(toQuery); i++ {
			select {
			case res := <-results:
				mu.Lock()
				for _, p := range res.peers {
					if p.ID != n.id && !containsPeer(shortlist, p.ID) {
						shortlist = append(shortlist, p)
					}
				}
				if res.item != nil && (bestItem == nil || res.item.Seq > bestItem.Seq) {
					bestItem = res.item
				}
				mu.Unlock()
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			}
		}

		// Sort shortlist by distance and trim to K.
		mu.Lock()
		sort.Slice(shortlist, func(i, j int) bool {
			di := Distance(shortlist[i].ID, target)
			dj := Distance(shortlist[j].ID, target)
			return Less(di, dj)
		})
		if len(shortlist) > K {
			shortlist = shortlist[:K]
		}
		mu.Unlock()

		// If we found a value, we can stop early.
		if wantValue && bestItem != nil {
			break
		}
	}

	return shortlist, bestItem, nil
}

func containsPeer(peers []Peer, id NodeID) bool {
	for _, p := range peers {
		if p.ID == id {
			return true
		}
	}
	return false
}
