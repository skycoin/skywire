// Package api pkg/deployment/sd/api/cxo_publisher.go c4-net-discovery
//
// CXO publisher for service-discovery's services tree.
//
// Subscribers (the hypervisor's network visualizer + tab-specific
// consumers) read the live set of registered services from a single
// TreeStore feed instead of polling /api/services?type=... over HTTP.
//
// # Wire shape: ONE batched leaf per service type
//
// Each service type gets exactly one leaf carrying that type's whole
// live service set:
//
//	services/<type>/all        // FrameGzip(v1, JSON []servicedisc.Service)
//
// Older builds published one leaf PER service at
// services/<type>/<pk>/entry (+ an optional /tombstone) — O(#services),
// one tiny object per registered service. A large deployment's Root then
// could not finish filling over a subscriber's short-lived delivering
// dmsg conn. Sharding by type collapses the object count to O(#types)
// (~10). The batch leaf lives UNDER the type dir (…/all, not a bare
// services/<type>) so every existing consumer prefix — services/,
// services/visor/, services/<type>/ — still walks straight onto it; a
// service PK is 66 hex chars and never collides with the "all" segment.
//
// Encoding is JSON+gzip, not fixed-layout binary: servicedisc.Service
// carries several optional, variable-length nested structs (Geo, VPNInfo,
// CoinInfo, LocalIPs), so it is the "awkward for binary" case — unlike
// telemetrywire's fixed rows. Services are sorted by PK so an unchanged
// set re-encodes to identical bytes (a CXO content-addressed wire no-op),
// then version-framed + gzipped (cxoutils.FrameGzip). The version byte
// gates the format: a bumped version is rejected cleanly by an old reader
// rather than misparsed.
//
// # Interop / deploy order
//
// Deploy is SERVICE-FIRST. A not-yet-upgraded reader walking a type
// prefix looks for the OLD .../<pk>/entry leaves, finds only the batched
// …/all leaf, reports an empty snapshot for that type, and falls back to
// HTTP — safe degradation. Upgraded readers prefer the batched leaf and
// still parse OLD per-service leaves, so either publisher shape resolves.
//
// A deregistration / expiry is the ABSENCE of a service from its type's
// batched leaf — there is no tombstone leaf class anymore (the old
// per-service tombstones were write-only across the codebase).
//
// # Heartbeat short-circuit
//
// SD's register/heartbeat rate is high. The worker holds the live
// per-type service set and, on a re-register, compares the new marshaled
// Service against the stored one; when the materially-visible content is
// unchanged it skips the re-encode entirely, so a steady heartbeat stream
// does not churn the tree.
//
// HTTP-path decoupling: PutEntry / DelEntry are non-blocking. The
// HTTP register / deregister handlers call them inline, but the
// underlying treestore.Publisher mutex is contended by subscriber
// I/O — under load (many concurrent subscribers) a synchronous Put
// can block the HTTP goroutine long enough to pile up thousands of
// pending registrations and stall the entire visor refresh loop.
// Instead we route every publish through a single buffered-channel
// worker and drop on overflow. The per-type service state map is owned
// by that single worker goroutine, so its mutations need no lock. CXO
// data quality (last-write-wins, up to publishQueueDepth events in
// flight) degrades gracefully; the HTTP path stays fast.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// servicesBatchVersion is the wire-format version byte of the batched
// per-type leaf body (FrameGzip). Bump on any breaking change to the
// leaf encoding; readers reject other values and fall back.
const servicesBatchVersion = 1

// publishQueueDepth bounds in-flight publish operations. Sized for
// SD's expected register rate (a few hundred entries refreshing
// every 90s = a few hundred ops / 90s) with headroom for a restart
// thundering herd. Overflow is dropped; HTTP path never blocks.
const publishQueueDepth = 4096

// ServicesCXOPublisher mirrors SD's services state into a CXO
// TreeStore feed. Started automatically at SD startup whenever
// DMSG is enabled; the API calls into it via
// SetServicesCXOPublisher.
type ServicesCXOPublisher struct {
	pub *treestore.Publisher
	log *logging.Logger

	events chan func()
	done   chan struct{}
	wg     sync.WaitGroup

	dropped uint64 // atomic; incremented on queue overflow

	// state is the live per-type service set: service type -> service PK ->
	// that service's full JSON servicedisc.Service. Owned exclusively by
	// the single worker goroutine (run), so it needs no lock; the encode of
	// a type's batched leaf reads only from here.
	state map[string]map[cipher.PubKey][]byte

	mu        sync.Mutex
	lastError error
}

// StartServicesCXOPublisher constructs a publisher backed by the
// given DMSG client and SD secret key. The publisher's allowlist is
// open — the underlying SD HTTP routes are public reads, so the CXO
// mirror inherits that. Returns nil + error if the publisher can't be
// created (no dmsg client, listener bind failure, etc.); callers log
// and continue without it (the HTTP path remains the source of truth).
func StartServicesCXOPublisher(_ context.Context, dmsgC *dmsg.Client, sk cipher.SecKey, logger logrus.FieldLogger) (*ServicesCXOPublisher, error) {
	log := logging.MustGetLogger("sd-cxo-services-pub")

	pub, err := treestore.NewWithDMSG(dmsgC, sk, treestore.PubConfig{
		Logger:     log,
		InMemoryDB: true, // services are always recomputed from redis on next mutation/sweep
		DmsgPort:   skyenv.DmsgSDServicesCXOPort,
	})
	if err != nil {
		return nil, err
	}
	pub.SetAllowlist(nil)

	p := &ServicesCXOPublisher{
		pub:    pub,
		log:    log,
		events: make(chan func(), publishQueueDepth),
		done:   make(chan struct{}),
		state:  make(map[string]map[cipher.PubKey][]byte),
	}
	p.wg.Add(1)
	go p.run()

	if logger != nil {
		logger.WithField("feed_pk", pub.Feed()).WithField("dmsg_port", skyenv.DmsgSDServicesCXOPort).
			Info("CXO services publisher running")
	}
	return p, nil
}

// run drains the publish queue serially. Single worker preserves
// happens-before order between Puts and Deletes for the same path
// (registers / heartbeats / tombstones for one service never race
// each other). When the underlying treestore mutex is contended by
// subscriber I/O, the worker slows down and callers see drops at
// submit time — but the HTTP goroutine never blocks on the mutex.
func (p *ServicesCXOPublisher) run() {
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
func (p *ServicesCXOPublisher) submit(fn func()) {
	select {
	case p.events <- fn:
	default:
		dropped := atomic.AddUint64(&p.dropped, 1)
		// Log on the first drop and every power-of-two thereafter
		// (1, 2, 4, 8 …) so a runaway backlog is visible but a
		// transient bump doesn't spam the log.
		if dropped&(dropped-1) == 0 {
			p.log.WithField("dropped_total", dropped).
				Warn("CXO publish queue full; dropping mirror event")
		}
	}
}

// FeedPK returns the publisher's feed PK (SD's own PK, since the
// publisher was constructed with SD's secret key). Subscribers
// connect to this PK on skyenv.DmsgSDServicesCXOPort.
func (p *ServicesCXOPublisher) FeedPK() cipher.PubKey { return p.pub.Feed() }

// Dropped returns the cumulative count of publish operations that
// were dropped because the queue was full at submit time. Exposed
// for /health-style introspection and as a tripwire for
// "subscribers are slow enough that the mirror is degrading".
func (p *ServicesCXOPublisher) Dropped() uint64 {
	if p == nil {
		return 0
	}
	return atomic.LoadUint64(&p.dropped)
}

// Close stops the worker goroutine and the underlying publisher.
// Pending events at the moment of Close are discarded (the
// underlying CXO state is best-effort — a fresh SD startup will
// re-emit the live entries on the next register / heartbeat).
// Safe to call multiple times.
func (p *ServicesCXOPublisher) Close() error {
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

// PutEntry mirrors a service register/update. Updates the worker's
// per-type service state and re-encodes that type's batched leaf — but
// only when the service's materially-visible content changed (heartbeat
// short-circuit), so a steady re-register stream does not churn the tree.
// Best-effort and non-blocking: queued for the publish worker; dropped on
// queue overflow.
func (p *ServicesCXOPublisher) PutEntry(svc *servicedisc.Service) {
	if p == nil || p.pub == nil || svc == nil {
		return
	}
	if svc.Type == "" {
		return
	}
	// Marshal under the caller's goroutine so the worker doesn't
	// serialize JSON encoding behind the publisher mutex. The
	// caller's CPU pays for this either way; doing it pre-queue
	// also means a bad entry is rejected synchronously instead of
	// silently after a queue hop.
	body, err := json.Marshal(svc)
	if err != nil {
		p.log.WithError(err).Debug("Failed to marshal service entry leaf")
		p.recordError(err)
		return
	}
	svcType := svc.Type
	pk := svc.Addr.PubKey()
	p.submit(func() {
		// Heartbeat short-circuit: a re-register whose marshaled Service
		// is byte-identical to what we already hold changes nothing a
		// subscriber sees, so skip the re-encode entirely.
		if cur := p.state[svcType]; cur != nil && bytes.Equal(cur[pk], body) {
			return
		}
		p.stateSet(svcType, pk, body)
		p.flushType(svcType)
	})
}

// DelEntry mirrors a service deregister or expiry. Removes the service
// from its type's set and re-encodes that type's batched leaf; the
// service's absence from the leaf IS the deletion signal (no tombstone
// leaf — see the package docstring). Best-effort and non-blocking.
func (p *ServicesCXOPublisher) DelEntry(svcType string, pk cipher.PubKey) {
	if p == nil || p.pub == nil || svcType == "" {
		return
	}
	p.submit(func() {
		if cur := p.state[svcType]; cur == nil || cur[pk] == nil {
			return // already absent — nothing to re-encode
		}
		p.stateDel(svcType, pk)
		p.flushType(svcType)
	})
}

// stateSet records a service's JSON body under its type. Worker-only.
func (p *ServicesCXOPublisher) stateSet(svcType string, pk cipher.PubKey, body []byte) {
	svcs := p.state[svcType]
	if svcs == nil {
		svcs = make(map[cipher.PubKey][]byte)
		p.state[svcType] = svcs
	}
	svcs[pk] = body
}

// stateDel removes a service from its type's set. Worker-only.
func (p *ServicesCXOPublisher) stateDel(svcType string, pk cipher.PubKey) {
	if svcs := p.state[svcType]; svcs != nil {
		delete(svcs, pk)
	}
}

// flushType re-encodes and Puts the batched leaf for a type, or Deletes
// it (and drops the type from state) when the type has no services left.
// CXO is content-addressed, so a re-Put of an unchanged leaf is a wire
// no-op. Worker-only.
func (p *ServicesCXOPublisher) flushType(svcType string) {
	svcs := p.state[svcType]
	path := batchLeafPath(svcType)
	if len(svcs) == 0 {
		delete(p.state, svcType)
		if err := p.pub.Delete(path); err != nil {
			p.log.WithError(err).WithField("path", path).Debug("Failed to delete emptied services leaf")
		}
		return
	}
	blob := encodeServicesBatch(svcs)
	if err := p.pub.Put(path, blob); err != nil {
		p.log.WithError(err).WithField("path", path).Debug("Failed to publish services batch leaf")
		p.recordError(err)
	}
}

// encodeServicesBatch serializes a type's service set into one batched
// leaf body: a JSON array of the services, sorted by PK so an unchanged
// set re-encodes to identical bytes (a CXO wire no-op), then version-
// framed + gzipped. The array is assembled by concatenating the already-
// marshaled per-service bodies, so each service is encoded exactly once.
func encodeServicesBatch(svcs map[cipher.PubKey][]byte) []byte {
	pks := make([]cipher.PubKey, 0, len(svcs))
	for pk := range svcs {
		pks = append(pks, pk)
	}
	sort.Slice(pks, func(i, j int) bool { return pks[i].Hex() < pks[j].Hex() })
	payload := make([]byte, 0, 2+len(pks)*256)
	payload = append(payload, '[')
	for i, pk := range pks {
		if i > 0 {
			payload = append(payload, ',')
		}
		payload = append(payload, svcs[pk]...)
	}
	payload = append(payload, ']')
	return cxoutils.FrameGzip(servicesBatchVersion, payload)
}

func (p *ServicesCXOPublisher) recordError(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastError = err
}

// LastError returns the most recent publish error, or nil. Exposed
// for /health-style introspection.
func (p *ServicesCXOPublisher) LastError() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastError
}

// BatchLeafPath is exported so subscribers can construct the batched
// per-type leaf path without duplicating the format string.
func BatchLeafPath(svcType string) string { return batchLeafPath(svcType) }

func batchLeafPath(svcType string) string {
	return fmt.Sprintf("services/%s/all", svcType)
}
