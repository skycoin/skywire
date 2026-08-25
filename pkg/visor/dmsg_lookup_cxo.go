// Package visor pkg/visor/dmsg_lookup_cxo.go c3-vis-core
//
// dmsg-entry LOOKUP over CXO — the consumer/resolver shim that lets a
// visor's dmsg client resolve a peer's discovery entry from the local
// CXO clients-by-server snapshot instead of a fresh HTTP-over-dmsg
// Noise handshake to dmsg-discovery (profiled 76% CPU in handshake
// crypto). The publish side is dmsg-discovery's
// ClientsByServerCXOPublisher (DmsgDMSGDClientsByServerCXOPort, 54),
// which mirrors every client's signed dmsgdisc.Entry into a TreeStore
// feed under clients-by-server/<server>/<client>/entry. The visor
// already ingests that feed for the hypervisor's network visualizer;
// this reuses the same cxosub-managed snapshot as a resolution path.
//
// Wiring: init_dmsg installs the resolver returned here into the dmsg
// client's discovery client (disc.NewCXOEntryClient), where it fronts
// the existing HTTP/direct chain for Entry() only. On a hit the peer
// entry is served with no network round-trip; on ANY miss the wrapped
// client resolves it over HTTP — "not in my CXO tree" is never
// "doesn't exist" (see the deployment-services-over-CXO roadmap's
// back-compat rules).
//
// Provenance: the snapshot comes from exactly one authoritative
// publisher — the configured dmsg-discovery PK (cxoFeedSpec resolves
// FeedDMSGDClientsByServer to dmsgdCXOPeer). There is no per-record
// reporter to police here: dmsg-discovery is the single signer of
// every entry in the feed, so the feed-PK gate already enforces
// provenance. The snapshot's own bodies are dmsg-disc-signed
// dmsgdisc.Entry values.
//
// Lifecycle: cxosub runs the feed as a periodic subscribe → snapshot
// → unsubscribe cycle (no long-lived subscription), so the orphan-
// feed reclaim the roadmap calls for is handled by the manager — the
// visor holds no unbounded CXDS here.
//
// SecKey binding: the cxosub manager subscribes over the visor's
// single dmsg client (v.dmsgC, keyed to the visor SecKey); there is no
// second dmsg client and no zero/random keypair (see
// feedback_never_two_dmsg_clients_same_key).
package visor

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	dmsgdisc "github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
)

// clientsByServerPrefix is the TreeStore path prefix the
// clients-by-server feed publishes under. Leaves are
// clients-by-server/<server-pk-hex>/<client-pk-hex>/entry.
const clientsByServerPrefix = "clients-by-server/"

// dmsgEntryCXOIndex resolves peer discovery entries from the visor's
// CXO clients-by-server snapshot. It memoizes a client-PK → entry
// index built from the snapshot, rebuilding only when the manager
// reports a newer sync (cheap snapshot-version via LastSync), so the
// common lookup is a map read under an RLock.
type dmsgEntryCXOIndex struct {
	v   *Visor
	log *logging.Logger

	acquireOnce sync.Once

	mu      sync.RWMutex
	builtAt time.Time
	index   map[cipher.PubKey]*dmsgdisc.Entry
}

// newDmsgEntryCXOResolver returns the EntryResolveFunc to install into
// the dmsg client's discovery chain, or nil if the resolver can't be
// built (no visor). The returned func is safe for concurrent use.
func (v *Visor) newDmsgEntryCXOResolver() dmsgdisc.EntryResolveFunc {
	if v == nil {
		return nil
	}
	idx := &dmsgEntryCXOIndex{
		v:   v,
		log: v.MasterLogger().PackageLogger("dmsg_lookup_cxo"),
	}
	return idx.resolve
}

// resolve looks up pk in the CXO clients-by-server snapshot. Returns
// ok=false on any miss (own PK, no manager, no snapshot yet, or pk
// absent) so the caller falls back to HTTP discovery.
func (idx *dmsgEntryCXOIndex) resolve(_ context.Context, pk cipher.PubKey) (*dmsgdisc.Entry, bool) {
	// Never shadow the visor's own entry: registration reads its own
	// entry back to bump the sequence number, and a stale CXO copy
	// would break that. Always take the HTTP path for self.
	if idx.v.conf != nil && pk == idx.v.conf.PK {
		return nil, false
	}
	mgr := idx.v.CXOSubMgr()
	if mgr == nil {
		return nil, false
	}
	// Hold the feed for the client's lifetime so its snapshot stays
	// warm. Idempotent-but-refcounted: acquire exactly once.
	idx.acquireOnce.Do(func() { mgr.AcquireFor(TabDmsgEntryLookup) })

	last := mgr.LastSync(FeedDMSGDClientsByServer)
	if last.IsZero() {
		// No successful sync yet — cache miss, fall back to HTTP.
		return nil, false
	}

	idx.mu.RLock()
	if idx.index != nil && idx.builtAt.Equal(last) {
		entry, ok := idx.index[pk]
		idx.mu.RUnlock()
		return entry, ok
	}
	idx.mu.RUnlock()

	idx.rebuild(mgr, last)

	idx.mu.RLock()
	defer idx.mu.RUnlock()
	entry, ok := idx.index[pk]
	return entry, ok
}

// rebuild walks the clients-by-server snapshot into a fresh client-PK
// → entry index. A client appears under every server it is delegated
// to with an identical entry body, so the last one wins (same
// content). Guarded so a concurrent rebuild for the same snapshot
// version runs at most once.
func (idx *dmsgEntryCXOIndex) rebuild(mgr *CXOSubscriptionManager, version time.Time) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	// Another goroutine may have rebuilt this exact version while we
	// waited for the write lock.
	if idx.index != nil && idx.builtAt.Equal(version) {
		return
	}

	built := make(map[cipher.PubKey]*dmsgdisc.Entry)
	mgr.Walk(FeedDMSGDClientsByServer, clientsByServerPrefix, func(path string, body []byte) bool {
		clientPK, ok := clientPKFromLeafPath(path)
		if !ok {
			return true
		}
		if len(body) == 0 {
			return true
		}
		entry := new(dmsgdisc.Entry)
		if err := json.Unmarshal(body, entry); err != nil {
			return true
		}
		// Only client entries with a usable delegated-server list are
		// worth serving from CXO; anything else must take the HTTP
		// path so it resolves authoritatively.
		if entry.Client == nil || len(entry.Client.DelegatedServers) == 0 {
			return true
		}
		built[clientPK] = entry
		return true
	})

	idx.index = built
	idx.builtAt = version
	if idx.log != nil {
		idx.log.WithField("clients", len(built)).WithField("snapshot_at", version).
			Debug("Rebuilt dmsg clients-by-server CXO lookup index")
	}
}

// clientPKFromLeafPath parses the <client-pk> out of a
// clients-by-server/<server-pk>/<client-pk>/entry leaf path. Returns
// ok=false for any path that isn't a live entry leaf (e.g. a legacy
// tombstone or a malformed segment).
func clientPKFromLeafPath(path string) (cipher.PubKey, bool) {
	if !strings.HasSuffix(path, "/entry") {
		return cipher.PubKey{}, false
	}
	core := strings.TrimSuffix(strings.TrimPrefix(path, clientsByServerPrefix), "/entry")
	// core == "<server-pk>/<client-pk>"
	slash := strings.LastIndexByte(core, '/')
	if slash < 0 {
		return cipher.PubKey{}, false
	}
	clientHex := core[slash+1:]
	if clientHex == "" {
		return cipher.PubKey{}, false
	}
	var pk cipher.PubKey
	if err := pk.Set(clientHex); err != nil {
		return cipher.PubKey{}, false
	}
	return pk, true
}
