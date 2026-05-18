// Package api pkg/dmsg/discovery/api/cxo_publisher.go
//
// CXO publisher for dmsg-discovery's "clients by server" view.
//
// The hypervisor's network visualizer needs to know "which clients
// are currently delegated to each dmsg server" — the same view
// AllClientsByServer / ClientsByServer expose over HTTP. This
// publisher mirrors that as a TreeStore feed:
//
//	clients-by-server/<server-pk>/<client-pk>/entry        // JSON disc.Entry
//
// We deliberately do NOT publish a separate entries/<pk> tree (the
// non-denormalized form) because no consumer we know of subscribes
// to per-PK lookups; the network-visualizer pattern is always
// server-grouped, and AllClientsByServer / ClientsByServer is what
// the existing http path serves.
//
// On a client SetEntry, the publisher diffs the new DelegatedServers
// list against the old (if any) and emits:
//   - Put on clients-by-server/<server>/<client>/entry for every
//     server in the new list.
//   - Delete on clients-by-server/<server>/<client>/entry for every
//     server that was in the old list but not the new (the client
//     moved off that server). Subscribers learn of removals through
//     CXO's standard leaf-disappearance event; pre-2026-05-18 we also
//     wrote a tombstone leaf carrying DeletedAt, but no consumer in
//     the codebase reads tombstone bodies (tpviz and visor/autoconnect
//     both explicitly skip them) and the leaves accumulated forever in
//     the publisher's in-memory CXDS, driving runaway RAM growth on
//     the deployment box (observed: 327 MB → 921 MB over 12h, with
//     ~70% of heap in cipher/encoder.Serialize / memoryCXDS.Set under
//     Refs.AppendValues). Dropping the tombstone write closes the
//     leak without losing any semantics that's currently read.
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
// time out visor heartbeats. Overflow drops are counted; CXO data
// quality degrades gracefully, HTTP stays fast.
package api

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
)

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
		Logger:     log,
		InMemoryDB: true, // recomputed from redis on every mutation
		DmsgPort:   skyenv.DmsgDMSGDClientsByServerCXOPort,
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

// PublishSetEntry mirrors a set/update of a client entry. Emits Puts
// for every server in the new DelegatedServers list and tombstones
// for any server that dropped out (compared to old). Does nothing
// for server entries (they're path buckets here, not members) or
// for client entries with empty DelegatedServers (nothing to publish).
// Non-blocking: the diff and JSON encode happen on the caller's
// goroutine; the per-server Put/Delete fan-out runs on the worker.
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

	body, err := json.Marshal(newEntry)
	if err != nil {
		p.log.WithError(err).Debug("Failed to marshal client entry leaf")
		p.recordError(err)
		return
	}

	p.submit(func() {
		// Put / re-Put for every server in the new list. CXO is
		// content-addressed so unchanged bytes are wire-no-ops.
		for srv := range newServers {
			path := entryLeafPath(srv, clientPK)
			if err := p.pub.Put(path, body); err != nil {
				p.log.WithError(err).WithField("path", path).Debug("Failed to publish clients-by-server entry leaf")
				p.recordError(err)
				continue
			}
			// Defensive: also Delete any tombstone left from an
			// older binary that wrote them. Idempotent — no-op if
			// none exists. Avoids a stale tombstone surviving the
			// upgrade and confusing a future consumer that does
			// read them.
			if err := p.pub.Delete(tombstoneLeafPath(srv, clientPK)); err != nil {
				p.log.WithError(err).Debug("Failed to clear legacy clients-by-server tombstone")
			}
		}

		// Delete the leaf for every server that was in the old list
		// but not the new. Subscribers see the leaf disappear via
		// CXO's normal update channel; we deliberately don't write a
		// tombstone leaf — see the package docstring for the leak
		// rationale.
		for srv := range oldServers {
			if _, kept := newServers[srv]; kept {
				continue
			}
			entryPath := entryLeafPath(srv, clientPK)
			if err := p.pub.Delete(entryPath); err != nil {
				p.log.WithError(err).WithField("path", entryPath).
					Debug("Failed to delete dropped clients-by-server entry leaf")
			}
		}
	})
}

// PublishDelEntry mirrors a full client-entry delete: deletes the
// (server, client) entry leaves for every server the client was
// previously delegated to. oldEntry is the entry being deleted
// (caller fetches it from store before the DelEntry call).
// Non-blocking.
//
// Pre-2026-05-18 this also wrote a tombstone leaf per server; see
// the package docstring for why tombstones were dropped (write-only
// across the codebase + unbounded in-memory growth).
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
		for srv := range servers {
			entryPath := entryLeafPath(srv, clientPK)
			if err := p.pub.Delete(entryPath); err != nil {
				p.log.WithError(err).Debug("Failed to delete clients-by-server entry leaf on full delete")
			}
			// Defensive: clear any tombstone left from an older
			// binary; idempotent if none exists.
			if err := p.pub.Delete(tombstoneLeafPath(srv, clientPK)); err != nil {
				p.log.WithError(err).Debug("Failed to clear legacy clients-by-server tombstone on full delete")
			}
		}
	})
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

// EntryLeafPath / TombstoneLeafPath are exported so subscribers can
// reconstruct paths without duplicating the format string.
func EntryLeafPath(serverPK, clientPK cipher.PubKey) string {
	return entryLeafPath(serverPK, clientPK)
}
func TombstoneLeafPath(serverPK, clientPK cipher.PubKey) string {
	return tombstoneLeafPath(serverPK, clientPK)
}

func entryLeafPath(serverPK, clientPK cipher.PubKey) string {
	return fmt.Sprintf("clients-by-server/%s/%s/entry", serverPK.Hex(), clientPK.Hex())
}
func tombstoneLeafPath(serverPK, clientPK cipher.PubKey) string {
	return fmt.Sprintf("clients-by-server/%s/%s/tombstone", serverPK.Hex(), clientPK.Hex())
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
