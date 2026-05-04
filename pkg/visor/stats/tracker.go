// Package stats — pkg/visor/stats/tracker.go: sampler orchestrator.
//
// The Tracker owns the periodic sampling loop, holds the in-memory
// day-start baselines for per-transport bandwidth deltas, and exposes
// probes that the visor wires into the transport manager, the dmsg
// init code, and the app discovery manager.
//
// Probes are pull-style: the Tracker queries them on each tick rather
// than receiving push events. This avoids any callback / event bus
// machinery and keeps the wiring trivial — the visor just provides
// closures over its existing fields.
package stats

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

// Tracker periodically samples transport bandwidth/latency and tier
// and service uptime, persisting rollups into a Store.
type Tracker struct {
	store    *Store
	log      *logging.Logger
	probes   Probes
	interval time.Duration
	keep     time.Duration
	// publishKeep caps how many trailing days' worth of paths are
	// mirrored to the Sink. 0 means "match retention" (mirror
	// everything). Typical use: store keeps 30 days, publisher
	// exposes only 7.
	publishKeep time.Duration

	mu         sync.Mutex
	sink       Sink
	baselines  map[uuid.UUID]bandwidthBaseline
	lastDay    string // YYYY-MM-DD UTC of the most recent sample
	lastPruned time.Time

	cancel context.CancelFunc
	done   chan struct{}
}

// Probes is the set of pull functions the Tracker calls on each tick.
// All probes are expected to be non-blocking and safe to call from a
// dedicated goroutine.
type Probes struct {
	// Transports returns a snapshot of the visor's currently live
	// transports. The Tracker iterates the result and reads bandwidth
	// + latency from each.
	Transports func() []TransportProbe

	// TierStates returns the current state of each named tier:
	// process / dmsg / skynet. Tiers absent from the map are treated
	// as offline. Returning nil disables tier sampling for the tick.
	TierStates func() map[string]bool

	// ServiceStates returns the current state of each registered
	// service slug. Returning nil disables service sampling for the
	// tick.
	ServiceStates func() map[string]bool
}

// TransportProbe is the per-transport view the Tracker needs each
// tick. The visor adapter constructs these by walking the transport
// manager — keeping the type local insulates the package from the
// wider transport API surface.
type TransportProbe struct {
	ID        uuid.UUID
	Edges     []cipher.PubKey
	Type      string
	Label     string
	SentBytes uint64
	RecvBytes uint64
	LatencyMS LatencyTriple
}

// LatencyTriple carries the live min/max/avg snapshot. Tracker
// merges these into today's daily row and overwrites Current.
type LatencyTriple struct {
	Min, Max, Avg float64
}

type bandwidthBaseline struct {
	day  string
	sent uint64
	recv uint64
}

// Config bundles tracker tunables. Zero values mean "use defaults".
type Config struct {
	SampleInterval time.Duration // default 1m
	RetentionDays  int           // default 30
	// PublishWindowDays caps the number of trailing days mirrored
	// to the Sink. 0 means "match RetentionDays" — the Sink sees
	// every day the bbolt store keeps. Typical: 7 (publisher
	// exposes one week, store retains 30 days).
	PublishWindowDays int
	Logger            *logging.Logger
}

// NewTracker constructs a Tracker but does not start the sample loop;
// call Run to begin. The store is owned by the Tracker for the
// lifetime of Run; Close releases it.
func NewTracker(store *Store, probes Probes, conf Config) *Tracker {
	if conf.SampleInterval <= 0 {
		conf.SampleInterval = time.Minute
	}
	if conf.RetentionDays <= 0 {
		conf.RetentionDays = 30
	}
	if conf.Logger == nil {
		conf.Logger = logging.MustGetLogger("visor-stats")
	}
	publishKeep := time.Duration(conf.PublishWindowDays) * 24 * time.Hour
	if conf.PublishWindowDays <= 0 {
		publishKeep = time.Duration(conf.RetentionDays) * 24 * time.Hour
	}
	return &Tracker{
		store:       store,
		log:         conf.Logger,
		probes:      probes,
		interval:    conf.SampleInterval,
		keep:        time.Duration(conf.RetentionDays) * 24 * time.Hour,
		publishKeep: publishKeep,
		sink:        noopSink{},
		baselines:   make(map[uuid.UUID]bandwidthBaseline),
	}
}

// Store returns the underlying bbolt-backed store. Exposed so HTTP
// handlers and the CXO publisher can read snapshots without going
// through the tracker (which is sample-loop-only).
func (t *Tracker) Store() *Store {
	return t.store
}

// Run starts the sampler. Returns immediately; sampling continues
// until ctx is canceled or Close is called. Idempotent: a second
// Run on the same tracker is a no-op.
func (t *Tracker) Run(ctx context.Context) {
	t.mu.Lock()
	if t.cancel != nil {
		t.mu.Unlock()
		return
	}
	loopCtx, cancel := context.WithCancel(ctx)
	t.cancel = cancel
	t.done = make(chan struct{})
	t.mu.Unlock()

	go t.loop(loopCtx)
}

// Close stops the sampler and closes the store. Safe to call multiple
// times.
func (t *Tracker) Close() error {
	t.mu.Lock()
	cancel := t.cancel
	done := t.done
	t.cancel = nil
	t.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
	return t.store.Close()
}

func (t *Tracker) loop(ctx context.Context) {
	defer close(t.done)
	tick := time.NewTicker(t.interval)
	defer tick.Stop()

	// Take an immediate sample so the first observations don't have
	// to wait an interval — keeps `current` populated at startup.
	t.sample(time.Now())

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			t.sample(now)
		}
	}
}

// sample is the per-tick body. Exposed for tests; callers pass the
// "now" instant so behavior at day boundaries is exercisable without
// sleeping.
func (t *Tracker) sample(now time.Time) {
	utc := now.UTC()
	today := utc.Format(dateFmt)
	slot := SlotForTime(utc)

	t.mu.Lock()
	dayChanged := t.lastDay != "" && t.lastDay != today
	t.lastDay = today
	t.mu.Unlock()

	if dayChanged {
		t.runRetention(utc)
	}

	if probe := t.probes.Transports; probe != nil {
		for _, tp := range probe() {
			if err := t.recordTransport(tp, utc, today); err != nil {
				t.log.WithError(err).WithField("tp_id", tp.ID).
					Debug("Failed to record transport sample")
			}
		}
	}

	if probe := t.probes.TierStates; probe != nil {
		for tier, online := range probe() {
			if !online {
				continue
			}
			if err := t.store.MarkTierSlot(tier, utc, slot); err != nil {
				t.log.WithError(err).WithField("tier", tier).
					Debug("Failed to mark tier slot")
				continue
			}
			t.mirrorTierBitmap(tier, utc)
		}
	}

	if probe := t.probes.ServiceStates; probe != nil {
		for svc, online := range probe() {
			if !online {
				continue
			}
			if err := t.store.MarkServiceSlot(svc, utc, slot); err != nil {
				t.log.WithError(err).WithField("service", svc).
					Debug("Failed to mark service slot")
				continue
			}
			t.mirrorServiceBitmap(svc, utc)
		}
	}
}

// mirrorTierBitmap pushes the post-write tier bitmap to the sink.
// Called from sample after MarkTierSlot succeeds. Errors are logged
// at debug level — sink mirroring is best-effort and must not block
// the sampler.
func (t *Tracker) mirrorTierBitmap(tier string, now time.Time) {
	bm, err := t.store.TierBitmap(tier, now)
	if err != nil {
		t.log.WithError(err).WithField("tier", tier).Debug("Stats: tier bitmap read failed")
		return
	}
	t.sinkPut(tierBitmapPath(tier, now.UTC().Format(dateFmt)), bm)
}

// mirrorServiceBitmap is the per-service analog of mirrorTierBitmap.
func (t *Tracker) mirrorServiceBitmap(svc string, now time.Time) {
	bm, err := t.store.ServiceBitmap(svc, now)
	if err != nil {
		t.log.WithError(err).WithField("service", svc).Debug("Stats: service bitmap read failed")
		return
	}
	t.sinkPut(serviceBitmapPath(svc, now.UTC().Format(dateFmt)), bm)
}

// sinkPut snapshots the sink under the lock and dispatches the put
// outside it, so a slow sink doesn't pin the sample loop's mutex.
func (t *Tracker) sinkPut(path string, value []byte) {
	t.mu.Lock()
	sink := t.sink
	t.mu.Unlock()
	sink.Put(path, value)
}

// sinkDelete is the per-key delete analog of sinkPut.
func (t *Tracker) sinkDelete(path string) {
	t.mu.Lock()
	sink := t.sink
	t.mu.Unlock()
	sink.Delete(path)
}

func (t *Tracker) recordTransport(tp TransportProbe, now time.Time, today string) error {
	rec, err := t.store.GetTransportRecord(tp.ID)
	if err != nil {
		return err
	}
	if rec == nil {
		rec = &TransportRecord{
			ID:        tp.ID,
			Edges:     tp.Edges,
			Type:      tp.Type,
			Label:     tp.Label,
			FirstSeen: now,
		}
	}
	rec.LastSeen = now
	rec.Current = &LiveSnapshot{
		SentBytes:    tp.SentBytes,
		RecvBytes:    tp.RecvBytes,
		LatencyMinMS: tp.LatencyMS.Min,
		LatencyMaxMS: tp.LatencyMS.Max,
		LatencyAvgMS: tp.LatencyMS.Avg,
		SampledAt:    now,
		Type:         rec.Type,
	}

	t.mu.Lock()
	base, ok := t.baselines[tp.ID]
	if !ok || base.day != today {
		// New day or first sample for this transport: anchor today's
		// baseline at the current cumulative counters. The first
		// daily-row delta will be 0; subsequent samples accumulate.
		base = bandwidthBaseline{day: today, sent: tp.SentBytes, recv: tp.RecvBytes}
		t.baselines[tp.ID] = base
	}
	t.mu.Unlock()

	deltaSent := uint64(0)
	deltaRecv := uint64(0)
	if tp.SentBytes >= base.sent {
		deltaSent = tp.SentBytes - base.sent
	}
	if tp.RecvBytes >= base.recv {
		deltaRecv = tp.RecvBytes - base.recv
	}

	row := findOrAppendDaily(rec, today)
	row.SentBytes = deltaSent
	row.RecvBytes = deltaRecv
	mergeLatency(row, tp.LatencyMS)
	row.Samples++

	if err := t.store.PutTransportRecord(rec); err != nil {
		return err
	}

	// Mirror to sink after the durable write succeeds. Use JSON for
	// transport entries (matches the wire shape served by the
	// /stats/transports/history HTTP endpoint).
	idStr := tp.ID.String()
	if data, err := json.Marshal(rec.Current); err == nil {
		t.sinkPut(currentTransportPath(idStr), data)
	}
	if data, err := json.Marshal(row); err == nil {
		t.sinkPut(dailyTransportPath(idStr, today), data)
	}

	// Per-transport uptime bitmap: set this tick's 5-min slot and
	// mirror the post-update bitmap. CXO content-addressing means an
	// unchanged bitmap (subsequent ticks within the same slot)
	// produces a stable Root and no wire republish — only slot
	// rollovers actually transit DMSG.
	slot := SlotForTime(now)
	if err := t.store.MarkTransportSlot(idStr, now, slot); err != nil {
		t.log.WithError(err).WithField("tp_id", idStr).
			Debug("Stats: MarkTransportSlot failed")
	} else if bm, bErr := t.store.TransportBitmap(idStr, now); bErr == nil {
		t.sinkPut(transportTimelinePath(idStr, today), bm)
	}
	return nil
}

// findOrAppendDaily returns a pointer to today's daily row, creating
// it on append if the most recent row is for an earlier date. The
// daily list is kept sorted by date; new days append to the tail.
func findOrAppendDaily(rec *TransportRecord, today string) *DailyRollup {
	if n := len(rec.Daily); n > 0 && rec.Daily[n-1].Date == today {
		return &rec.Daily[n-1]
	}
	rec.Daily = append(rec.Daily, DailyRollup{Date: today})
	return &rec.Daily[len(rec.Daily)-1]
}

// mergeLatency folds a fresh sample into the day's accumulator. Min
// shrinks toward the smallest non-zero observation; Avg uses Welford's
// online mean over Samples (incremented by the caller after this).
func mergeLatency(row *DailyRollup, s LatencyTriple) {
	if s.Avg <= 0 {
		// No measurement this tick (transport may not have completed
		// a ping cycle yet). Don't pollute the day's running stats.
		return
	}
	if row.LatencyMinMS == 0 || s.Min < row.LatencyMinMS {
		row.LatencyMinMS = s.Min
	}
	if s.Max > row.LatencyMaxMS {
		row.LatencyMaxMS = s.Max
	}
	// Welford-style mean update; row.Samples is the count *before*
	// this sample, so n+1 in the denominator.
	n := float64(row.Samples)
	row.LatencyAvgMS = (row.LatencyAvgMS*n + s.Avg) / (n + 1)
}

// runRetention drops bitmap keys outside the retention window and
// trims daily rollups on each transport record. Called from the
// sampler when it observes a UTC-day rollover, and once at startup
// (via maybeRunStartupRetention) to catch up missed sweeps.
//
// Sink mirroring follows: anything pruned from the bbolt store is
// deleted from the sink, and anything that's still in bbolt but
// outside the (narrower) publish window is also sink-deleted so the
// publisher's view stays bounded at the configured rolling-window
// size even though the durable store keeps more.
func (t *Tracker) runRetention(now time.Time) {
	cutoff := now.Add(-t.keep)
	if removed, err := t.store.PruneBitmaps(cutoff); err != nil {
		t.log.WithError(err).Warn("Bitmap pruning failed")
	} else if removed > 0 {
		t.log.WithField("removed", removed).Debug("Pruned old bitmap keys")
	}

	cutoffDate := cutoff.UTC().Format(dateFmt)
	records, err := t.store.AllTransportRecords()
	if err != nil {
		t.log.WithError(err).Warn("Retention: enumerate transports failed")
		return
	}
	for _, rec := range records {
		// Sink-delete daily rows that are about to fall out of the
		// bbolt store. (Sink already wouldn't have the ones outside
		// the publish window — those were pruned at the previous
		// midnight — but be defensive.)
		droppedFromBolt := map[string]struct{}{}
		trimmed := rec.Daily[:0]
		for _, d := range rec.Daily {
			if d.Date < cutoffDate {
				droppedFromBolt[d.Date] = struct{}{}
				continue
			}
			trimmed = append(trimmed, d)
		}
		for date := range droppedFromBolt {
			t.sinkDelete(dailyTransportPath(rec.ID.String(), date))
			t.sinkDelete(transportTimelinePath(rec.ID.String(), date))
		}
		if len(trimmed) != len(rec.Daily) {
			rec.Daily = trimmed
			if err := t.store.PutTransportRecord(rec); err != nil {
				t.log.WithError(err).WithField("tp_id", rec.ID).
					Warn("Retention: rewrite transport record failed")
			}
		}
	}

	// Sink-prune dates that remain in bbolt but are outside the
	// publish window — keeps the publisher's exposed dataset
	// bounded at the configured rolling-window size.
	t.sinkPruneOutsidePublishWindow(now)

	t.mu.Lock()
	t.lastPruned = now
	t.mu.Unlock()
}

// sinkPruneOutsidePublishWindow walks the store and sink-deletes any
// path whose date is older than now-publishKeep. Called from
// runRetention after bbolt pruning.
func (t *Tracker) sinkPruneOutsidePublishWindow(now time.Time) {
	if t.publishKeep == 0 || t.publishKeep >= t.keep {
		// No narrower window than retention — nothing to prune.
		return
	}
	publishCutoff := now.Add(-t.publishKeep).UTC().Format(dateFmt)

	records, err := t.store.AllTransportRecords()
	if err != nil {
		return
	}
	for _, rec := range records {
		for _, d := range rec.Daily {
			if d.Date < publishCutoff {
				t.sinkDelete(dailyTransportPath(rec.ID.String(), d.Date))
				t.sinkDelete(transportTimelinePath(rec.ID.String(), d.Date))
			}
		}
	}

	tiers, err := t.store.TierNames()
	if err != nil {
		t.log.WithError(err).Warn("Stats: enumerate tiers for publish-window prune failed")
	}
	for _, tier := range tiers {
		dates, dErr := t.store.TierDates(tier)
		if dErr != nil {
			t.log.WithError(dErr).WithField("tier", tier).Debug("Stats: TierDates failed during prune")
			continue
		}
		for _, d := range dates {
			if d < publishCutoff {
				t.sinkDelete(tierBitmapPath(tier, d))
			}
		}
	}

	services, err := t.store.ServiceNames()
	if err != nil {
		t.log.WithError(err).Warn("Stats: enumerate services for publish-window prune failed")
	}
	for _, svc := range services {
		dates, dErr := t.store.ServiceDates(svc)
		if dErr != nil {
			t.log.WithError(dErr).WithField("service", svc).Debug("Stats: ServiceDates failed during prune")
			continue
		}
		for _, d := range dates {
			if d < publishCutoff {
				t.sinkDelete(serviceBitmapPath(svc, d))
			}
		}
	}

	// Transport timeline bitmaps. Walk the bucket directly rather
	// than rec.Daily — bitmaps can persist for transports whose
	// TransportRecord was deleted (e.g. closed transports during
	// retention) until the bbolt prune sweep drops them.
	tpIDs, err := t.store.TransportBitmapIDs()
	if err != nil {
		t.log.WithError(err).Warn("Stats: enumerate transport bitmaps for publish-window prune failed")
	}
	for _, id := range tpIDs {
		dates, dErr := t.store.TransportBitmapDates(id)
		if dErr != nil {
			t.log.WithError(dErr).WithField("tp_id", id).Debug("Stats: TransportBitmapDates failed during prune")
			continue
		}
		for _, d := range dates {
			if d < publishCutoff {
				t.sinkDelete(transportTimelinePath(id, d))
			}
		}
	}
}
