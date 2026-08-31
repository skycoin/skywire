// Package router pkg/router/pk_sort.go c2-net-routing
package router

import (
	"bytes"

	"github.com/skycoin/skywire/pkg/cipher"
)

// pkLess orders two public keys by their raw bytes. This is IDENTICAL ordering
// to comparing pk.String() (which is Hex(), and hex preserves byte order) but
// with no per-comparison allocation.
//
// It exists for sort comparators on hot paths. pk.String()/Hex() allocates a
// fresh string on every call, and sort invokes the comparator O(n log n) times;
// in calculateLocalRoutes' local-route BFS, a memory profile attributed ~80% of
// the whole search's allocations to the pk.String() comparator on the
// per-expansion children sort over a dense graph. That allocation churn was the
// GC pressure behind the in-browser wasm visor's route-setup CPU bursts. Sorting
// by bytes drops the search's allocations ~9.6× (19.5k → 2.0k per call in a
// dense-graph benchmark) and speeds it up ~37%, with byte-identical routes.
func pkLess(a, b cipher.PubKey) bool { return bytes.Compare(a[:], b[:]) < 0 }
