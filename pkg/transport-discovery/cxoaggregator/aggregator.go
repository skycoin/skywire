// Package cxoaggregator subscribes to per-visor TreeStore feeds and
// mirrors received telemetry into the TPD's redis store.
//
// Visors publish per-transport bandwidth/latency under the path
// scheme defined in pkg/visor/stats/sink.go:
//
//	transports/<uuid>/current          → JSON LiveSnapshot
//	transports/<uuid>/<YYYY-MM-DD>     → JSON DailyRollup
//	tiers/<tier>/<YYYY-MM-DD>          → 36-byte bitmap
//	services/<slug>/<YYYY-MM-DD>       → 36-byte bitmap
//
// The aggregator subscribes to each known visor's feed (PK is the
// visor's own PK — one identity per node) and feeds the cumulative
// counters in `current` snapshots into store.UpdateBandwidth, which
// computes per-reporter deltas and updates the per-transport and
// per-visor aggregations the legacy /metric, /bandwidth/*, and
// /metrics/* endpoints already read from.
//
// Tier and service bitmap routing is left as a follow-up (the data
// arrives and is held by each Subscriber's local cache; the only
// thing missing is a write-through into TPD's redis uptime tables).
package cxoaggregator

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
)

// VisorSource enumerates the set of visor PKs the aggregator should
// subscribe to. The TPD's existing transport store satisfies this via
// GetAllTransports + edge extraction.
type VisorSource interface {
	KnownVisors(ctx context.Context) ([]cipher.PubKey, error)
}

// BandwidthSink receives per-transport cumulative-counter updates.
// The TPD's redis store satisfies this via UpdateBandwidth.
type BandwidthSink interface {
	UpdateBandwidth(ctx context.Context, transportID string, reporterPK cipher.PubKey, sent, recv uint64) error
}

// liveSnapshot mirrors pkg/visor/stats.LiveSnapshot. We re-declare it
// here (rather than importing pkg/visor/stats from a TPD-side
// package) to keep the dependency direction one-way: visor → spec
// → wire format → TPD-side parser. Any field rename would need to be
// reflected in both places.
type liveSnapshot struct {
	SentBytes    uint64    `json:"sent_bytes"`
	RecvBytes    uint64    `json:"recv_bytes"`
	LatencyMinMS float64   `json:"latency_min_ms,omitempty"`
	LatencyMaxMS float64   `json:"latency_max_ms,omitempty"`
	LatencyAvgMS float64   `json:"latency_avg_ms,omitempty"`
	SampledAt    time.Time `json:"sampled_at"`
}

// Config configures the Aggregator.
type Config struct {
	// ReconcileInterval is how often the aggregator refreshes its
	// subscription set against the VisorSource. Defaults to 60s.
	ReconcileInterval time.Duration
	// Logger overrides the default tagged logger.
	Logger *logging.Logger
}

// Aggregator subscribes to per-visor TreeStore feeds and mirrors
// received bandwidth telemetry into the BandwidthSink.
type Aggregator struct {
	dmsgC  *dmsg.Client
	source VisorSource
	sink   BandwidthSink
	conf   Config
	log    *logging.Logger

	mu     sync.Mutex
	subs   map[cipher.PubKey]*treestore.Subscriber
	cancel context.CancelFunc
	done   chan struct{}
}

// New constructs an Aggregator. The dmsg client is shared with the
// rest of the TPD; subscribers each spin up their own CXO node
// internally (this is the pattern the deleted flat subscriber used
// too — a TPD-wide shared CXO node is a worthwhile follow-up
// optimization).
func New(dmsgC *dmsg.Client, source VisorSource, sink BandwidthSink, conf Config) *Aggregator {
	if conf.ReconcileInterval <= 0 {
		conf.ReconcileInterval = 60 * time.Second
	}
	if conf.Logger == nil {
		conf.Logger = logging.MustGetLogger("tpd-cxo-aggregator")
	}
	return &Aggregator{
		dmsgC:  dmsgC,
		source: source,
		sink:   sink,
		conf:   conf,
		log:    conf.Logger,
		subs:   make(map[cipher.PubKey]*treestore.Subscriber),
		done:   make(chan struct{}),
	}
}

// Run starts the reconciliation loop. Returns immediately; the loop
// continues until ctx is cancelled or Close is called. Idempotent.
func (a *Aggregator) Run(ctx context.Context) {
	a.mu.Lock()
	if a.cancel != nil {
		a.mu.Unlock()
		return
	}
	loopCtx, cancel := context.WithCancel(ctx)
	a.cancel = cancel
	a.mu.Unlock()

	go a.loop(loopCtx)
}

// Close stops the loop and tears down all subscriptions. Idempotent.
func (a *Aggregator) Close() error {
	a.mu.Lock()
	cancel := a.cancel
	a.cancel = nil
	a.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	<-a.done

	a.mu.Lock()
	defer a.mu.Unlock()
	for pk, sub := range a.subs {
		_ = sub.Close() //nolint:errcheck
		delete(a.subs, pk)
	}
	return nil
}

func (a *Aggregator) loop(ctx context.Context) {
	defer close(a.done)
	t := time.NewTicker(a.conf.ReconcileInterval)
	defer t.Stop()

	a.reconcile(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.reconcile(ctx)
		}
	}
}

// reconcile syncs the subscription set with the current known-visors
// list. New PKs get a fresh Subscriber; vanished PKs get closed.
func (a *Aggregator) reconcile(ctx context.Context) {
	pks, err := a.source.KnownVisors(ctx)
	if err != nil {
		a.log.WithError(err).Warn("CXO aggregator: KnownVisors failed")
		return
	}

	want := make(map[cipher.PubKey]struct{}, len(pks))
	for _, pk := range pks {
		want[pk] = struct{}{}
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	for pk := range a.subs {
		if _, ok := want[pk]; !ok {
			_ = a.subs[pk].Close() //nolint:errcheck
			delete(a.subs, pk)
		}
	}
	for pk := range want {
		if _, exists := a.subs[pk]; exists {
			continue
		}
		sub, err := a.subscribe(ctx, pk)
		if err != nil {
			a.log.WithError(err).WithField("visor", pk).
				Debug("CXO aggregator: subscribe failed; will retry next reconcile")
			continue
		}
		a.subs[pk] = sub
	}
}

// subscribe builds a TreeStore subscriber for one visor and wires
// its OnUpdate to the aggregator's writeback path.
func (a *Aggregator) subscribe(_ context.Context, pk cipher.PubKey) (*treestore.Subscriber, error) {
	sub, err := treestore.NewSubscriber(a.dmsgC, pk, treestore.SubConfig{
		Logger:     a.log,
		InMemoryDB: true,
	})
	if err != nil {
		return nil, err
	}
	sub.OnUpdate(a.handleUpdates(pk))
	if err := sub.Connect(pk); err != nil {
		_ = sub.Close() //nolint:errcheck
		return nil, err
	}
	return sub, nil
}

// handleUpdates returns the OnUpdate callback bound to a specific
// reporter PK. Each event's path is parsed; transport leaves with a
// "current" suffix dispatch to the bandwidth sink.
func (a *Aggregator) handleUpdates(reporterPK cipher.PubKey) treestore.UpdateCallback {
	return func(events []treestore.UpdateEvent) {
		for _, ev := range events {
			if ev.Value == nil {
				continue // delete events — nothing to write back
			}
			if id, ok := parseCurrentTransportPath(ev.Path); ok {
				var snap liveSnapshot
				if err := json.Unmarshal(ev.Value, &snap); err != nil {
					a.log.WithError(err).WithField("path", ev.Path).
						Debug("CXO aggregator: live snapshot decode failed")
					continue
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				err := a.sink.UpdateBandwidth(ctx, id.String(), reporterPK, snap.SentBytes, snap.RecvBytes)
				cancel()
				if err != nil {
					a.log.WithError(err).WithField("transport", id).
						Debug("CXO aggregator: UpdateBandwidth failed")
				}
				continue
			}
			// Other paths (daily transport rollups, tier bitmaps,
			// service bitmaps) are not yet routed to redis. The
			// data is held by sub.Get/Walk and exposed when those
			// follow-up writebacks land.
		}
	}
}

// parseCurrentTransportPath returns the transport UUID for paths
// shaped "transports/<uuid>/current", or false otherwise.
func parseCurrentTransportPath(path string) (uuid.UUID, bool) {
	const prefix = "transports/"
	const suffix = "/current"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return uuid.UUID{}, false
	}
	mid := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	if strings.Contains(mid, "/") {
		return uuid.UUID{}, false
	}
	id, err := uuid.Parse(mid)
	if err != nil {
		return uuid.UUID{}, false
	}
	return id, true
}
