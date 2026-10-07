// Package api pkg/deployment/tpd/api/cxo_perkey_publisher.go c4-net-discovery
//
// CXO publisher for the per-key transport rollup: every visor's transports
// counted by type, the GET /all-transports/per-key-stats body, at
//
//	perkey/stats
//
// It is the slower tier cxo_stats_publisher.go leaves this body out of. At
// ~38 KB gzipped it is too big for the stats feed a dashboard holds open,
// and the whole point is not to be the all-transports snapshot (~2.6 MB) a
// reader would otherwise download to count per key itself — which is what
// the network view did on every refresh.
//
// The body carries the same completeness stamp as the stats feed, judged on
// the transport total, and the same holdover: after a TPD restart the
// registry refills from near zero, and a per-key table read mid-refill shows
// most visors with too few transports and nothing to say so.
package api

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport"
)

// PerKeyPath is the one leaf the per-key feed writes.
const PerKeyPath = "perkey/stats"

// perKeyPublishInterval is the republish cadence. Registrations lapse on a
// 5-minute TTL, so a minute keeps the counts as fresh as the network view's
// own 5-minute cache wants them without republishing faster than they move.
const perKeyPublishInterval = time.Minute

// PerKeyStats is the body published at PerKeyPath. Keys is the
// /all-transports/per-key-stats body verbatim; the rest is the completeness
// stamp.
type PerKeyStats struct {
	Keys  map[string]map[string]int `json:"keys"`
	Total int                       `json:"total_transports"`

	ObservedAt time.Time `json:"observed_at"`
	Complete   bool      `json:"complete"`
	Confidence string    `json:"confidence"`
	// TrailingPeak is the highest total_transports seen in the last
	// TrailingWindowSeconds.
	TrailingPeak          int `json:"trailing_peak_transports"`
	TrailingWindowSeconds int `json:"trailing_window_seconds"`
}

// PerKeyCXOPublisher publishes PerKeyStats on a timer. Closed by Close.
type PerKeyCXOPublisher struct {
	api *API
	pub *treestore.Publisher
	log *logging.Logger

	cancel context.CancelFunc
	done   chan struct{}

	// Touched only from the publish loop.
	transports completenessTracker
	heldSince  time.Time // when the complete sample on the feed was put; zero if none

	// putFn writes the gzipped leaf: s.pub.Put in production, a seam for
	// tests.
	putFn func(path string, body []byte) error

	mu        sync.Mutex
	lastError error
}

// StartPerKeyCXOPublisher constructs the publisher on the given DMSG client
// and TPD secret key and starts its ticker. The feed is open to any
// subscriber, like the HTTP endpoint it mirrors. Best-effort: HTTP stays the
// source of truth, so the caller should log and continue on error.
func StartPerKeyCXOPublisher(ctx context.Context, api *API, dmsgC *dmsg.Client, sk cipher.SecKey, logger logrus.FieldLogger) (*PerKeyCXOPublisher, error) {
	log := logging.MustGetLogger("tpd-cxo-perkey-pub")

	pub, err := treestore.NewWithDMSG(dmsgC, sk, treestore.PubConfig{
		Logger:     log,
		InMemoryDB: true,
		DmsgPort:   skyenv.DmsgTPDPerKeyCXOPort,
	})
	if err != nil {
		return nil, err
	}
	pub.SetAllowlist(nil)

	pubCtx, cancel := context.WithCancel(ctx)
	p := &PerKeyCXOPublisher{
		api:        api,
		pub:        pub,
		log:        log,
		cancel:     cancel,
		done:       make(chan struct{}),
		transports: newCompletenessTracker(time.Now()),
	}
	p.putFn = pub.Put
	if logger != nil {
		logger.WithField("feed_pk", pub.Feed()).WithField("dmsg_port", skyenv.DmsgTPDPerKeyCXOPort).
			Info("CXO per-key publisher running")
	}
	go p.loop(pubCtx)
	return p, nil
}

// FeedPK returns the publisher's feed PK (TPD's own PK).
func (p *PerKeyCXOPublisher) FeedPK() cipher.PubKey { return p.pub.Feed() }

// Close stops the ticker and tears down the publisher.
func (p *PerKeyCXOPublisher) Close() error {
	if p.cancel != nil {
		p.cancel()
	}
	<-p.done
	return p.pub.Close()
}

func (p *PerKeyCXOPublisher) loop(ctx context.Context) {
	defer close(p.done)

	p.publishOnce(ctx, time.Now().UTC())

	t := time.NewTicker(perKeyPublishInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			p.publishOnce(ctx, now.UTC())
		}
	}
}

// publishOnce writes one sample, unless it looks partial while a complete
// one still stands on the feed within statsIncompleteHoldover — the stats
// feed's rule, for the same reason.
func (p *PerKeyCXOPublisher) publishOnce(ctx context.Context, now time.Time) {
	entries := p.entries(ctx)
	if len(entries) == 0 {
		return
	}
	verdict := p.transports.observe(now, len(entries))
	if !verdict.complete && !p.heldSince.IsZero() && now.Sub(p.heldSince) < statsIncompleteHoldover {
		p.log.Debug("holding last complete per-key sample; this one looks partial")
		return
	}
	raw, err := json.Marshal(PerKeyStats{
		Keys:                  perKeyCounts(entries),
		Total:                 len(entries),
		ObservedAt:            now,
		Complete:              verdict.complete,
		Confidence:            verdict.confidence,
		TrailingPeak:          verdict.peak,
		TrailingWindowSeconds: int(statsTrailingWindow / time.Second),
	})
	if err != nil {
		p.log.WithError(err).Warn("per-key marshal failed")
		p.recordError(err)
		return
	}
	if err := p.putFn(PerKeyPath, cxoutils.Gzip(raw)); err != nil {
		p.log.WithError(err).Warn("per-key publisher Put failed")
		p.recordError(err)
		return
	}
	if verdict.complete {
		p.heldSince = now
	} else {
		p.heldSince = time.Time{}
	}
	p.recordError(nil)
}

// entries reads the set GET /all-transports/per-key-stats counts by default:
// the warm cache, self-transports included, or the store on a cold cache.
func (p *PerKeyCXOPublisher) entries(ctx context.Context) []*transport.Entry {
	if entries := p.api.getTransportsFromCache(true); entries != nil {
		return entries
	}
	entries, err := p.api.store.GetAllTransports(ctx, true)
	if err != nil {
		p.log.WithError(err).Debug("per-key transports read failed; will retry next tick")
		p.recordError(err)
		return nil
	}
	return entries
}

func (p *PerKeyCXOPublisher) recordError(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastError = err
}

// LastError returns the error from the most recent publish attempt, or nil.
func (p *PerKeyCXOPublisher) LastError() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastError
}

// Publisher returns the underlying feed publisher, for introspection.
func (p *PerKeyCXOPublisher) Publisher() *treestore.Publisher { return p.pub }
