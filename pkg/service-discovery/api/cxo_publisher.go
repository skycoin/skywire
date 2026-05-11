// Package api pkg/service-discovery/api/cxo_publisher.go
//
// CXO publisher for service-discovery's services tree.
//
// Subscribers (the hypervisor's network visualizer + tab-specific
// consumers) read the live set of registered services from a single
// TreeStore feed instead of polling /api/services?type=... over HTTP.
// Tree shape:
//
//	services/<type>/<pk>/entry        // JSON-encoded servicedisc.Service
//	services/<type>/<pk>/tombstone    // JSON {"deleted_at": "..."}
//
// Type-as-prefix lets a consumer that only cares about one service
// kind (e.g. just VPN) subscribe to services/vpn/ and skip the rest.
//
// Event-driven: register/update calls Put on success; explicit
// deregister calls Delete (and Put on the tombstone leaf); expiry
// sweep emits tombstones for entries the redis cleanup found stale.
// CXO content-addresses, so re-publishing an unchanged entry is a
// no-op at the wire layer — heartbeats from a still-alive service
// don't burn bandwidth.
//
// HTTP-path decoupling: PutEntry / DelEntry are non-blocking. The
// HTTP register / deregister handlers call them inline, but the
// underlying treestore.Publisher mutex is contended by subscriber
// I/O — under load (many concurrent subscribers) a synchronous Put
// can block the HTTP goroutine long enough to pile up thousands of
// pending registrations and stall the entire visor refresh loop.
// Instead we route every publish through a single buffered-channel
// worker and drop on overflow. CXO data quality (last-write-wins,
// up to publishQueueDepth events in flight) degrades gracefully;
// the HTTP path stays fast.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skyenv"
)

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

// PutEntry mirrors a service register/update. Idempotent: identical
// bytes are a no-op at the wire layer. Best-effort and non-blocking:
// queued for the publish worker; dropped on queue overflow.
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
		path := entryPath(svcType, pk)
		if err := p.pub.Put(path, body); err != nil {
			p.log.WithError(err).WithField("path", path).Debug("Failed to publish service entry leaf")
			p.recordError(err)
			// Also clear any stale tombstone so subscribers don't see
			// a dead-then-alive flap. Best-effort.
			_ = p.pub.Delete(tombstonePath(svcType, pk)) //nolint:errcheck
			return
		}
		// Drop any existing tombstone for the same key — re-registration
		// after expiry should leave only the live entry leaf.
		if err := p.pub.Delete(tombstonePath(svcType, pk)); err != nil {
			p.log.WithError(err).Debug("Failed to clear stale services tombstone")
		}
	})
}

// DelEntry mirrors a service deregister or expiry. Removes the entry
// leaf and writes a tombstone leaf so subscribers see the deletion.
// Best-effort and non-blocking.
func (p *ServicesCXOPublisher) DelEntry(svcType string, pk cipher.PubKey) {
	if p == nil || p.pub == nil || svcType == "" {
		return
	}
	body, err := json.Marshal(struct {
		DeletedAt time.Time `json:"deleted_at"`
	}{DeletedAt: time.Now().UTC()})
	if err != nil {
		p.log.WithError(err).Debug("Failed to marshal services tombstone")
		return
	}
	p.submit(func() {
		if err := p.pub.Delete(entryPath(svcType, pk)); err != nil {
			p.log.WithError(err).WithField("type", svcType).Debug("Failed to delete service entry leaf")
			p.recordError(err)
		}
		if err := p.pub.Put(tombstonePath(svcType, pk), body); err != nil {
			p.log.WithError(err).WithField("type", svcType).Debug("Failed to publish services tombstone")
			p.recordError(err)
		}
	})
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

// EntryPath / TombstonePath are exported so subscribers can construct
// the same leaf paths without duplicating the format string.
func EntryPath(svcType string, pk cipher.PubKey) string     { return entryPath(svcType, pk) }
func TombstonePath(svcType string, pk cipher.PubKey) string { return tombstonePath(svcType, pk) }

func entryPath(svcType string, pk cipher.PubKey) string {
	return fmt.Sprintf("services/%s/%s/entry", svcType, pk.Hex())
}
func tombstonePath(svcType string, pk cipher.PubKey) string {
	return fmt.Sprintf("services/%s/%s/tombstone", svcType, pk.Hex())
}
