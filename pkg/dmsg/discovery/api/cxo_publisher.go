// Package api pkg/dmsg/discovery/api/cxo_publisher.go c1-net-dmsg
//
// CXO publisher for dmsg-discovery's "clients by server" view.
//
// The hypervisor's network visualizer needs to know "which clients
// are currently delegated to each dmsg server" — the same view
// AllClientsByServer / ClientsByServer expose over HTTP. This
// publisher mirrors that as a TreeStore feed.
//
// # Wire shape: ONE batched leaf per server
//
// Each dmsg server gets exactly one leaf carrying that server's whole
// delegated-client set:
//
//	clients-by-server/<server-pk>        // FrameGzip(v1, JSON []disc.Entry)
//
// Older builds published one leaf PER (server, client) PAIR at
// clients-by-server/<server>/<client>/entry — O(#pairs), thousands of
// tiny objects in one Root. A large deployment's Root then could not
// finish filling over a subscriber's short-lived delivering dmsg conn
// (only the top slice landed). Batching to one leaf per server drops
// the object count to O(#servers) (~tens–120), which is what lets the
// Root fill complete. The only consumers (the visor dmsg-entry lookup
// resolver and tpviz's network visualizer) want server-grouped data, so
// per-server batching loses no used addressability.
//
// Encoding is JSON+gzip, not fixed-layout binary: a client's disc.Entry
// carries a signature and a variable-length delegated-server list, so it
// is the "awkward for binary" case — unlike telemetrywire's fixed 53-byte
// rows. The per-server array is JSON-marshaled (entries sorted by client
// PK so unchanged content re-encodes to identical bytes → a wire no-op on
// CXO's content-addressed store), then framed with a leading version byte
// and gzipped (cxoutils.FrameGzip). The version byte gates the format:
// a bumped version is rejected cleanly by an old reader rather than
// misparsed.
//
// # Interop / deploy order
//
// Deploy is SERVICE-FIRST. A not-yet-upgraded reader walking the feed
// looks for the OLD .../<client>/entry leaves, finds none under the new
// batched shape, reports an empty snapshot, and falls back to HTTP — safe
// degradation, never a wrong answer. Upgraded readers prefer the batched
// leaf and still parse an OLD per-item leaf as a fallback, so either
// publisher shape resolves.
//
// A deregistration is the ABSENCE of a client from its server's batched
// leaf — there is no tombstone leaf class (the old per-(server,client)
// tombstones were write-only across the codebase and leaked RAM in the
// publisher's CXDS; observed 327 MB → 921 MB over 12h). Re-encoding a
// server's single leaf on a membership change is far cheaper than the old
// whole-tree churn: the tree is now O(#servers) leaves, so each publish
// clones + encodes a tiny tree.
//
// Server entries are ignored by this publisher — a server PK is the
// path-prefix bucket, not a member of the view.
//
// HTTP-path decoupling: the HTTP register / deregister handlers
// call PublishSetEntry / PublishDelEntry inline. Internally those
// route every mutation through a single buffered-channel worker
// (mirrors SD's pattern) so the HTTP goroutine never blocks on the
// treestore.Publisher mutex — which under load is contended by
// subscriber I/O and can stall register throughput long enough to
// time out visor heartbeats. The per-server client state map is owned
// by that single worker goroutine, so its mutations need no lock.
// Overflow drops are counted; CXO data quality degrades gracefully,
// HTTP stays fast.
package api

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// clientsByServerBatchVersion is the wire-format version byte of the
// batched per-server leaf body (FrameGzip). Bump on any breaking change
// to the leaf encoding; readers reject other values and fall back.
const clientsByServerBatchVersion = 1

// clientsByServerCXOBatchWindow coalesces the publisher's tree mutations before
// it re-encodes + publishes the clients-by-server tree.
//
// Why override the treestore default (1s): publishIfDirty clones AND re-encodes
// the ENTIRE in-memory tree per publish (cloneMemNode / encodeNode / delRoot —
// O(tree size), independent of how many leaves changed). The clients-by-server
// tree spans every delegated (server, client) pair across the whole network, so
// at the 1s default, under the steady register + heartbeat churn there is always
// something dirty and the full tree is re-cloned+encoded every second. That made
// dmsg-discovery GC-bound at ~2.8 cores (observed 2026-06-22: 286% CPU, ~55% in
// scanObject/sweep, ~1.5 TB cumulative alloc under cxo clone/encode).
//
// A larger window does NOT make any single publish more expensive (the encode
// cost is the tree's CURRENT size, not the mutation count since last publish) —
// it just publishes less often. This feed only drives the hypervisor's network
// visualizer, where 30s staleness is imperceptible, so a coarse window cuts the
// whole-tree re-encode frequency ~30x for no meaningful loss. (TPD's CXO
// publishers run 60s; this sits between that and the old 1s.)
const clientsByServerCXOBatchWindow = 30 * time.Second

// json is the package-scope jsoniter.ConfigFastest value declared
// in api.go. We reuse it here to avoid pulling in encoding/json
// (which collides with the package-scope name) and to match the
// performance characteristics of the rest of dmsg-discovery's
// hot path.
var _ = json // referenced from api.go; reuse here

// publishQueueDepth bounds in-flight publish operations. Sized for
// DMSG-D's expected mutation rate (every client refresh emits one
// SetEntry that fans out to its DelegatedServers list) with
// headroom for a restart thundering herd. Overflow is dropped;
// HTTP path never blocks.
const publishQueueDepth = 4096

// ClientsByServerCXOPublisher mirrors dmsg-discovery's clients-by-
// server view into a CXO TreeStore feed. Started automatically at
// DMSG-D startup whenever DMSG is enabled; the API calls into it
// via SetClientsByServerCXOPublisher.
type ClientsByServerCXOPublisher struct {
	pub *treestore.Publisher
	log *logging.Logger

	events chan func()
	done   chan struct{}
	wg     sync.WaitGroup

	dropped uint64 // atomic; incremented on queue overflow

	// state is the live per-server client set: server PK -> client PK ->
	// that client's full JSON disc.Entry. Owned exclusively by the single
	// worker goroutine (run), so it needs no lock; the encode of a
	// server's batched leaf reads only from here.
	state map[cipher.PubKey]map[cipher.PubKey][]byte

	mu        sync.Mutex
	lastError error
}

// StartClientsByServerCXOPublisher constructs the publisher backed by
// the given dmsg client and DMSG-D secret key. The publisher's
// allowlist is open — the underlying HTTP routes are public reads,
// so the CXO mirror inherits that. Returns nil + error if the
// publisher can't be created (no dmsg client, listener bind failure,
// etc.); callers log and continue without it (HTTP path stays the
// source of truth).
func StartClientsByServerCXOPublisher(dmsgC *dmsg.Client, sk cipher.SecKey, logger logrus.FieldLogger) (*ClientsByServerCXOPublisher, error) {
	log := logging.MustGetLogger("dmsgd-cxo-clients-pub")

	pub, err := treestore.NewWithDMSG(dmsgC, sk, treestore.PubConfig{
		Logger:      log,
		InMemoryDB:  true, // recomputed from redis on every mutation
		DmsgPort:    skyenv.DmsgDMSGDClientsByServerCXOPort,
		BatchWindow: clientsByServerCXOBatchWindow, // coarse: this feed only drives network-viz; avoids per-second whole-tree re-encode (see const doc)
	})
	if err != nil {
		return nil, err
	}
	pub.SetAllowlist(nil)

	p := &ClientsByServerCXOPublisher{
		pub:    pub,
		log:    log,
		events: make(chan func(), publishQueueDepth),
		done:   make(chan struct{}),
		state:  make(map[cipher.PubKey]map[cipher.PubKey][]byte),
	}
	p.wg.Add(1)
	go p.run()

	if logger != nil {
		logger.WithField("feed_pk", pub.Feed()).WithField("dmsg_port", skyenv.DmsgDMSGDClientsByServerCXOPort).
			Info("CXO clients-by-server publisher running")
	}
	return p, nil
}

// run drains the publish queue serially. Single worker preserves
// happens-before order between mutations for the same path. When
// the underlying treestore mutex is contended by subscriber I/O the
// worker slows down and callers see drops at submit time — but the
// HTTP goroutine never blocks on the mutex.
func (p *ClientsByServerCXOPublisher) run() {
	defer p.wg.Done()
	for {
		select {
		case <-p.done:
			return
		case fn := <-p.events:
			fn()
		}
	}
}

// submit enqueues a publish operation. Non-blocking: drops on
// overflow and bumps the counter so the operator can spot a
// sustained backlog via LastError + structured logs.
func (p *ClientsByServerCXOPublisher) submit(fn func()) {
	select {
	case p.events <- fn:
	default:
		dropped := atomic.AddUint64(&p.dropped, 1)
		if dropped&(dropped-1) == 0 {
			p.log.WithField("dropped_total", dropped).
				Warn("CXO publish queue full; dropping mirror event")
		}
	}
}

// FeedPK returns the publisher's feed PK (DMSG-D's own PK).
func (p *ClientsByServerCXOPublisher) FeedPK() cipher.PubKey { return p.pub.Feed() }

// Dropped returns the cumulative count of dropped publish
// operations. Exposed for /health-style introspection.
func (p *ClientsByServerCXOPublisher) Dropped() uint64 {
	if p == nil {
		return 0
	}
	return atomic.LoadUint64(&p.dropped)
}

// Close stops the worker goroutine and the underlying publisher.
// Pending events at the moment of Close are discarded. Safe to
// call multiple times.
func (p *ClientsByServerCXOPublisher) Close() error {
	if p == nil || p.pub == nil {
		return nil
	}
	select {
	case <-p.done:
		// already closed
	default:
		close(p.done)
	}
	p.wg.Wait()
	return p.pub.Close()
}

// PublishSetEntry mirrors a set/update of a client entry. Updates the
// worker's per-server client state — adding the client to every server
// in the new DelegatedServers list, removing it from any server it left
// (compared to old) — and re-encodes each affected server's batched
// leaf. Does nothing for server entries (they're path buckets here, not
// members) or for client entries with empty DelegatedServers (the client
// is absent from every server's leaf, which is exactly a deregistration).
// Non-blocking: the diff and JSON encode of the entry happen on the
// caller's goroutine; the state mutation + per-server re-encode/Put runs
// on the worker.
func (p *ClientsByServerCXOPublisher) PublishSetEntry(oldEntry, newEntry *disc.Entry) {
	if p == nil || p.pub == nil || newEntry == nil || newEntry.Client == nil {
		return
	}
	clientPK := newEntry.Static
	newServers := pkSet(newEntry.Client.DelegatedServers)
	oldServers := map[cipher.PubKey]struct{}{}
	if oldEntry != nil && oldEntry.Client != nil {
		oldServers = pkSet(oldEntry.Client.DelegatedServers)
	}

	// Heartbeat short-circuit. clients-by-server subscribers consume
	// (server, client) membership; Sequence/Timestamp/Signature bumps
	// on otherwise-unchanged entries don't change that view. Under
	// prod heartbeat load (~160 POST /entry/ per second observed
	// 2026-05-19) re-publishing every refresh pegged dmsg-discovery
	// at one full CPU core (13537 cpu-sec over 13309 wall-sec since
	// startup, with 89% of inuse heap under
	// Refs.AppendValues -> encoder.Serialize). Skipping when the
	// materially-visible content is unchanged keeps the leaf set
	// stable while removing the dominant publish work.
	if oldEntry != nil && entryContentEqual(oldEntry, newEntry) {
		return
	}

	body, err := json.Marshal(newEntry)
	if err != nil {
		p.log.WithError(err).Debug("Failed to marshal client entry leaf")
		p.recordError(err)
		return
	}

	p.submit(func() {
		dirty := make(map[cipher.PubKey]struct{}, len(newServers)+len(oldServers))
		// Add / refresh the client under every server in the new list.
		for srv := range newServers {
			p.stateSet(srv, clientPK, body)
			dirty[srv] = struct{}{}
		}
		// Remove the client from every server it left.
		for srv := range oldServers {
			if _, kept := newServers[srv]; kept {
				continue
			}
			p.stateDel(srv, clientPK)
			dirty[srv] = struct{}{}
		}
		p.flushServers(dirty)
	})
}

// PublishDelEntry mirrors a full client-entry delete: removes the client
// from every server it was delegated to and re-encodes those servers'
// batched leaves. oldEntry is the entry being deleted (caller fetches it
// from store before the DelEntry call). The client's absence from a
// server's leaf IS the deregistration signal — there is no tombstone
// leaf (see the package docstring). Non-blocking.
func (p *ClientsByServerCXOPublisher) PublishDelEntry(oldEntry *disc.Entry) {
	if p == nil || p.pub == nil || oldEntry == nil || oldEntry.Client == nil {
		return
	}
	clientPK := oldEntry.Static
	servers := pkSet(oldEntry.Client.DelegatedServers)
	if len(servers) == 0 {
		return
	}
	p.submit(func() {
		dirty := make(map[cipher.PubKey]struct{}, len(servers))
		for srv := range servers {
			p.stateDel(srv, clientPK)
			dirty[srv] = struct{}{}
		}
		p.flushServers(dirty)
	})
}

// stateSet records clientPK's entry body under serverPK. Worker-only.
func (p *ClientsByServerCXOPublisher) stateSet(serverPK, clientPK cipher.PubKey, body []byte) {
	clients := p.state[serverPK]
	if clients == nil {
		clients = make(map[cipher.PubKey][]byte)
		p.state[serverPK] = clients
	}
	clients[clientPK] = body
}

// stateDel removes clientPK from serverPK's set. Worker-only.
func (p *ClientsByServerCXOPublisher) stateDel(serverPK, clientPK cipher.PubKey) {
	if clients := p.state[serverPK]; clients != nil {
		delete(clients, clientPK)
	}
}

// flushServers re-encodes and Puts the batched leaf for each dirty
// server, or Deletes it (and drops the server from state) when the
// server has no delegated clients left. CXO is content-addressed, so a
// re-Put of an unchanged leaf is a wire no-op. Worker-only.
func (p *ClientsByServerCXOPublisher) flushServers(dirty map[cipher.PubKey]struct{}) {
	for srv := range dirty {
		clients := p.state[srv]
		path := batchLeafPath(srv)
		if len(clients) == 0 {
			delete(p.state, srv)
			if err := p.pub.Delete(path); err != nil {
				p.log.WithError(err).WithField("path", path).
					Debug("Failed to delete emptied clients-by-server leaf")
			}
			continue
		}
		blob := encodeClientsBatch(clients)
		if err := p.pub.Put(path, blob); err != nil {
			p.log.WithError(err).WithField("path", path).Debug("Failed to publish clients-by-server batch leaf")
			p.recordError(err)
		}
	}
}

// encodeClientsBatch serializes a server's client set into one batched
// leaf body: a JSON array of the clients' disc.Entry objects, sorted by
// client PK so an unchanged set re-encodes to identical bytes (a CXO
// wire no-op), then version-framed + gzipped. The array is assembled by
// concatenating the already-marshaled per-client entry bodies, so each
// client is encoded exactly once (on the caller's goroutine, in
// PublishSetEntry) rather than re-marshaled here.
func encodeClientsBatch(clients map[cipher.PubKey][]byte) []byte {
	pks := make([]cipher.PubKey, 0, len(clients))
	for pk := range clients {
		pks = append(pks, pk)
	}
	sort.Slice(pks, func(i, j int) bool { return pks[i].Hex() < pks[j].Hex() })
	payload := make([]byte, 0, 2+len(pks)*256)
	payload = append(payload, '[')
	for i, pk := range pks {
		if i > 0 {
			payload = append(payload, ',')
		}
		payload = append(payload, clients[pk]...)
	}
	payload = append(payload, ']')
	return cxoutils.FrameGzip(clientsByServerBatchVersion, payload)
}

func (p *ClientsByServerCXOPublisher) recordError(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastError = err
}

// LastError returns the most recent publish error, or nil.
func (p *ClientsByServerCXOPublisher) LastError() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastError
}

// BatchLeafPath is exported so subscribers can reconstruct the batched
// per-server leaf path without duplicating the format string.
func BatchLeafPath(serverPK cipher.PubKey) string { return batchLeafPath(serverPK) }

func batchLeafPath(serverPK cipher.PubKey) string {
	return fmt.Sprintf("clients-by-server/%s", serverPK.Hex())
}

// pkSet builds a set-of-PK from a slice; small helper to avoid
// repeated O(N²) membership tests in the diff loops above.
func pkSet(pks []cipher.PubKey) map[cipher.PubKey]struct{} {
	out := make(map[cipher.PubKey]struct{}, len(pks))
	for _, pk := range pks {
		out[pk] = struct{}{}
	}
	return out
}

// entryContentEqual reports whether two entries are identical from
// the clients-by-server view's perspective. Deliberately excludes
// Sequence, Timestamp, and Signature because those bump on every
// heartbeat re-signature without changing the delegated-server
// membership that subscribers consume.
func entryContentEqual(a, b *disc.Entry) bool {
	if a == nil || b == nil {
		return false
	}
	if a.Version != b.Version || a.ClientType != b.ClientType || a.Protocol != b.Protocol {
		return false
	}
	if a.Static != b.Static {
		return false
	}
	if (a.Client == nil) != (b.Client == nil) {
		return false
	}
	if a.Client != nil {
		if len(a.Client.DelegatedServers) != len(b.Client.DelegatedServers) {
			return false
		}
		set := pkSet(a.Client.DelegatedServers)
		for _, pk := range b.Client.DelegatedServers {
			if _, ok := set[pk]; !ok {
				return false
			}
		}
	}
	return true
}
