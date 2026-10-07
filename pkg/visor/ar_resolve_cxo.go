// Package visor pkg/visor/ar_resolve_cxo.go
//
// Address lookups from the address resolver's bindings feed. The visor holds
// the feed (about 100 KB for the whole network) and answers stcpr, squicr and
// swtr lookups from it, in microseconds and with no request to the resolver,
// where GET /resolve costs an authenticated round trip of about 140 ms
// (measured in pkg/deployment/ar/arpreview).
//
// A miss of any kind falls through to GET /resolve, which stays the authority.
// The feed trails the resolver's store by its publish window, so an answer may
// be an address the peer has just left. A peer and type are therefore answered
// from the feed at most once per fastResolveRepeatAfter: a second lookup inside
// that window, which usually follows a failed dial, goes to the resolver.
package visor

import (
	"context"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/deployment/ar/arfeed"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

const fastResolveRepeatAfter = 2 * time.Minute

type arBindingsIndex struct {
	acquire sync.Once

	mu      sync.RWMutex
	builtAt time.Time
	index   map[string]*arfeed.PeerBindings

	servedMu sync.Mutex
	served   map[string]time.Time
}

// arResolveFast is installed in front of the address resolver client's
// GET /resolve. It reports false whenever the feed cannot answer.
func (v *Visor) arResolveFast(_ context.Context, tType string, pk cipher.PubKey) (addrresolver.VisorData, bool) {
	t := types.NormalizeType(types.Type(tType))
	if t == types.SUDPH {
		return addrresolver.VisorData{}, false
	}
	mgr := v.CXOSubMgr()
	if mgr == nil {
		return addrresolver.VisorData{}, false
	}
	idx := &v.arBindings
	idx.acquire.Do(func() { mgr.AcquireFor(TabARResolve) })
	last := mgr.LastSync(FeedARBindings)
	if last.IsZero() {
		return addrresolver.VisorData{}, false
	}
	b := idx.lookup(mgr, last, pk.Hex())
	if b == nil {
		return addrresolver.VisorData{}, false
	}
	data := b.Get(t)
	if data == nil || !idx.mayServe(pk.Hex()+"/"+string(t), time.Now()) {
		return addrresolver.VisorData{}, false
	}
	return *data, true
}

// lookup returns pk's bindings from the snapshot of version, rebuilding the
// index when the snapshot changed.
func (idx *arBindingsIndex) lookup(mgr *CXOSubscriptionManager, version time.Time, pkHex string) *arfeed.PeerBindings {
	idx.mu.RLock()
	if idx.index != nil && idx.builtAt.Equal(version) {
		b := idx.index[pkHex]
		idx.mu.RUnlock()
		return b
	}
	idx.mu.RUnlock()

	idx.mu.Lock()
	defer idx.mu.Unlock()
	if idx.index == nil || !idx.builtAt.Equal(version) {
		built := make(map[string]*arfeed.PeerBindings)
		mgr.Walk(FeedARBindings, "", func(_ string, body []byte) bool {
			peers, err := arfeed.DecodeBucket(body)
			if err != nil {
				return true
			}
			for k, b := range peers {
				built[k] = b
			}
			return true
		})
		idx.index, idx.builtAt = built, version
	}
	return idx.index[pkHex]
}

// mayServe reports whether key may be answered from the feed now, and if so
// records that it was.
func (idx *arBindingsIndex) mayServe(key string, now time.Time) bool {
	idx.servedMu.Lock()
	defer idx.servedMu.Unlock()
	if idx.served == nil {
		idx.served = make(map[string]time.Time)
	}
	if at, ok := idx.served[key]; ok && now.Sub(at) < fastResolveRepeatAfter {
		return false
	}
	if len(idx.served) > 4096 {
		for k, at := range idx.served {
			if now.Sub(at) >= fastResolveRepeatAfter {
				delete(idx.served, k)
			}
		}
	}
	idx.served[key] = now
	return true
}

// arAdvertised reports which transport types pk has a binding for, from the
// bindings feed, so automatic transports need not fetch every peer's list
// from the address resolver. ok is false until the feed has synced.
func (v *Visor) arAdvertised(pk cipher.PubKey) (map[types.Type]bool, bool) {
	mgr := v.CXOSubMgr()
	if mgr == nil {
		return nil, false
	}
	idx := &v.arBindings
	idx.acquire.Do(func() { mgr.AcquireFor(TabARResolve) })
	last := mgr.LastSync(FeedARBindings)
	if last.IsZero() {
		return nil, false
	}
	out := map[types.Type]bool{}
	if b := idx.lookup(mgr, last, pk.Hex()); b != nil {
		for _, t := range []types.Type{types.STCPR, types.SUDPH, types.QUIC, types.WT} {
			if b.Get(t) != nil {
				out[t] = true
			}
		}
	}
	return out, true
}
