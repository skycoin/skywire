// Package stats pkg/visor/stats/tracker.go c3-vis-core
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
	"bytes"
	"context"
	"crypto/sha256"
	"runtime/debug"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/telemetrywire"
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

	mu        sync.Mutex
	sink      Sink
	baselines map[uuid.UUID]bandwidthBaseline
	// publishedShards records, per shard (0..15), the MEANINGFUL-content
	// signature of the shard telemetry blob last mirrored to the sink at
	// transports/telemetry/<sh>. The signature is computed over every
	// entry's sent/recv/throughput/latency/type — deliberately EXCLUDING
	// sampled_at (see shardSig) — so a shard whose transports are all idle
	// (only their timestamps advancing each tick) does NOT re-Put and does
	// not churn the telemetry Root. A shard is re-Put only when a
	// meaningful field of one of its transports moves; a shard that goes
	// empty (all its transports closed) is sink-Deleted and dropped from
	// this map so it stops occupying the Root. Guarded by mu.
	publishedShards map[uint8][32]byte
	lastDay         string // YYYY-MM-DD UTC of the most recent sample
	lastPruned      time.Time

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
	ID            uuid.UUID
	Edges         []cipher.PubKey
	Type          string
	Label         string
	SentBytes     uint64
	RecvBytes     uint64
	ThroughputBps float64
	LatencyMS     LatencyTriple
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
		store:           store,
		log:             conf.Logger,
		probes:          probes,
		interval:        conf.SampleInterval,
		keep:            time.Duration(conf.RetentionDays) * 24 * time.Hour,
		publishKeep:     publishKeep,
		sink:            noopSink{},
		baselines:       make(map[uuid.UUID]bandwidthBaseline),
		publishedShards: make(map[uint8][32]byte),
	}
}

// SeedPublishedShards records the shard signatures the sink was already
// primed with before the sampler started — the live set hydrated by
// HydrateSink at startup (see the visor's seedSinkFromStore). Seeding
// them means the first sample tick does NOT redundantly re-Put every
// shard that hydrate already published with identical content; a shard
// only re-Puts once one of its transports actually changes. Call before
// Run. The passed map is adopted directly.
func (t *Tracker) SeedPublishedShards(sigs map[uint8][32]byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if sigs == nil {
		sigs = make(map[uint8][32]byte)
	}
	t.publishedShards = sigs
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
	t.safeSample(time.Now())

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			t.safeSample(now)
		}
	}
}

// safeSample wraps sample with a panic recover so a single bad
// observation (corrupt bbolt row, probe returning unexpected nil,
// per-day rollover edge cases) doesn't silently kill the goroutine
// and freeze the local uptime/bandwidth view for the rest of the
// process lifetime. Recovered panics are logged at Error level with
// the stack so the underlying cause is locatable, then the loop
// keeps ticking — best effort over correctness on a single
// observation. Discovered in production where slot fills dropped
// from ~100% to ~2% across a multi-day window with no log signal;
// without recover the panic-once / dead-forever behavior was
// indistinguishable from "everything is fine".
func (t *Tracker) safeSample(now time.Time) {
	defer func() {
		if r := recover(); r != nil {
			t.log.WithField("panic", r).
				WithField("stack", string(debug.Stack())).
				Error("Stats sampler panicked; recovered. Continuing.")
		}
	}()
	t.sample(now)
}

// sample is the per-tick body. Exposed for tests; callers pass the
// "now" instant so behavior at day boundaries is exercisable without
// sleeping.
//
// All bbolt mutations for this tick — transport records, tier/service
// slot bitmaps, transport timeline bitmaps — go through a single
// Store.UpdateSample transaction so the whole sample commits in one
// fdatasync. Sink mirroring (CXO publisher Put) happens after the tx
// commits, outside the bbolt critical section, so a slow sink doesn't
// hold the write lock.
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

	// entries collects the per-transport telemetry rows to publish AFTER
	// the bbolt tx commits, as compact sharded binary leaves. ranProbe
	// records whether the Transports probe actually ran this tick, so
	// publishShards can distinguish "no probe" (leave the published shards
	// alone) from "probe returned zero live transports" (delete every
	// previously-published shard leaf).
	var entries []telemetrywire.Entry
	var ranProbe bool

	txErr := t.store.UpdateSample(func(stx *SampleTx) error {
		if probe := t.probes.Transports; probe != nil {
			ranProbe = true
			tps := probe()
			entries = make([]telemetrywire.Entry, 0, len(tps))
			for _, tp := range tps {
				if e, err := t.recordTransportTx(stx, tp, utc, today); err != nil {
					t.log.WithError(err).WithField("tp_id", tp.ID).
						Debug("Failed to record transport sample")
				} else {
					entries = append(entries, e)
				}
			}
		}

		// Tier / service online-slot bitmaps are marked in bbolt for the
		// visor's own /stats history, but NOT mirrored to the CXO sink —
		// they are historical telemetry, not current discovery data (see
		// recordTransportTx for why the TPD feed stays current-only).
		if probe := t.probes.TierStates; probe != nil {
			for tier, online := range probe() {
				if !online {
					continue
				}
				if err := stx.MarkTierSlot(tier, utc, slot); err != nil {
					t.log.WithError(err).WithField("tier", tier).
						Debug("Failed to mark tier slot")
				}
			}
		}

		if probe := t.probes.ServiceStates; probe != nil {
			for svc, online := range probe() {
				if !online {
					continue
				}
				if err := stx.MarkServiceSlot(svc, utc, slot); err != nil {
					t.log.WithError(err).WithField("service", svc).
						Debug("Failed to mark service slot")
				}
			}
		}
		return nil
	})
	if txErr != nil {
		t.log.WithError(txErr).Debug("Stats: sample tx failed")
		return
	}

	t.publishShards(entries, ranProbe)
}

// publishShards groups this tick's live-transport entries into the 16
// fixed shards (by telemetrywire.ShardOf), encodes each non-empty shard
// as one compact binary leaf, and mirrors ONLY the shards whose
// meaningful content changed since they were last published. A shard
// that has gone empty (all its transports closed) but was previously
// published is sink-Deleted, so absence-on-the-feed == dead exactly as
// the per-transport reconcile used to guarantee. This keeps the
// telemetry Root at ≤16 telemetry leaves regardless of transport count
// — the whole point of the sharded shape (a busy hub's ~851
// per-transport current leaves collapse to ≤16 objects, so TPD's
// whole-Root fill completes over the short announce conn).
//
// ranProbe == false means the Transports probe didn't run this tick
// (no data to reconcile against); leave the published shards untouched.
//
// The change-gate signature (shardSig) deliberately excludes each
// entry's sampled_at, so a shard whose transports are all idle — their
// only per-tick change being the timestamp advancing — does not re-Put
// and does not churn the Root, matching the anti-churn guarantee of the
// old per-transport publishedSig.
func (t *Tracker) publishShards(entries []telemetrywire.Entry, ranProbe bool) {
	if !ranProbe {
		return
	}

	// Group by shard, then sort each shard's entries by ID so both the
	// signature and the encoded blob are byte-stable across ticks.
	byShard := make(map[uint8][]telemetrywire.Entry, telemetrywire.ShardCount)
	for _, e := range entries {
		sh := telemetrywire.ShardOf(e.ID)
		byShard[sh] = append(byShard[sh], e)
	}
	for sh := range byShard {
		es := byShard[sh]
		sort.Slice(es, func(i, j int) bool {
			return bytes.Compare(es[i].ID[:], es[j].ID[:]) < 0
		})
		byShard[sh] = es
	}

	t.mu.Lock()
	sink := t.sink
	var ops []SinkOp
	var deletes []string
	// Re-Put shards whose meaningful content changed; record the new sig.
	for sh, es := range byShard {
		sig := shardSig(es)
		if prev, ok := t.publishedShards[sh]; ok && prev == sig {
			continue // unchanged — leave the existing leaf in place
		}
		t.publishedShards[sh] = sig
		ops = append(ops, SinkOp{Path: telemetrywire.LeafPath(sh), Value: telemetrywire.EncodeShard(sh, es)})
	}
	// Delete shards that were published before but have no live transports now.
	for sh := range t.publishedShards {
		if _, still := byShard[sh]; !still {
			deletes = append(deletes, telemetrywire.LeafPath(sh))
			delete(t.publishedShards, sh)
		}
	}
	t.mu.Unlock()

	if len(ops) > 0 {
		sink.PutBatch(ops)
	}
	for _, path := range deletes {
		sink.Delete(path)
	}
}

// shardSig is the byte-stable, sampled_at-EXCLUDING signature of a
// shard's entries. Two ticks with the same transports carrying the same
// bandwidth/throughput/latency/type produce the same signature even
// though each entry's sampled_at advanced — so an idle shard does not
// re-Put. Entries must already be sorted by ID for stability.
func shardSig(entries []telemetrywire.Entry) [32]byte {
	stable := make([]telemetrywire.Entry, len(entries))
	copy(stable, entries)
	for i := range stable {
		stable[i].SampledAtUnix = 0
	}
	// Any shard byte works here — the signature only compares content;
	// ShardOf is identical for every entry in the slice anyway.
	var sh uint8
	if len(stable) > 0 {
		sh = telemetrywire.ShardOf(stable[0].ID)
	}
	return sha256.Sum256(telemetrywire.EncodeShard(sh, stable))
}

// sinkDelete snapshots the sink under the lock and dispatches the
// delete outside it, so a slow sink doesn't pin the sample loop's
// mutex. Called from the retention paths when a row falls outside the
// publish window (or the retention window) so the publisher's view
// drops the key in sync with bbolt.
func (t *Tracker) sinkDelete(path string) {
	t.mu.Lock()
	sink := t.sink
	t.mu.Unlock()
	sink.Delete(path)
}

// recordTransportTx is the in-tx variant of recordTransport. Reads
// the existing record, merges the new probe, writes back, and marks the
// timeline bit — all under the caller's SampleTx. Returns the compact
// telemetrywire.Entry for this transport, which the caller collects and
// hands to publishShards after the tx commits (the sampled_at-excluding
// change-gate + shard packing happen there, not per-transport).
func (t *Tracker) recordTransportTx(stx *SampleTx, tp TransportProbe, now time.Time, today string) (telemetrywire.Entry, error) {
	rec, err := stx.GetTransportRecord(tp.ID)
	if err != nil {
		return telemetrywire.Entry{}, err
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
		SentBytes:     tp.SentBytes,
		RecvBytes:     tp.RecvBytes,
		ThroughputBps: tp.ThroughputBps,
		LatencyMinMS:  tp.LatencyMS.Min,
		LatencyMaxMS:  tp.LatencyMS.Max,
		LatencyAvgMS:  tp.LatencyMS.Avg,
		SampledAt:     now,
		Type:          rec.Type,
	}

	t.mu.Lock()
	base, ok := t.baselines[tp.ID]
	if !ok || base.day != today {
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

	if err := stx.PutTransportRecord(rec); err != nil {
		return telemetrywire.Entry{}, err
	}

	idStr := tp.ID.String()
	// The daily rollup (row) is persisted to bbolt above (PutTransportRecord)
	// but deliberately NOT mirrored to the CXO sink: no subscriber reads it
	// (TPD's aggregator has no /rollup branch; the visor's own /stats reads it
	// from bbolt), and publishing a per-transport-per-minute leaf onto the
	// telemetry feed steals fill budget from the discovery leaf on the
	// short-lived announce conn — the constraint behind the discovery gap.

	// The per-transport timeline bitmap is marked in bbolt (below) for the
	// visor's own /stats + `visor state` consumption, but is NOT mirrored to
	// the CXO sink. TPD is a discovery service: it needs only the tp-list
	// discovery leaf plus the compact sharded telemetry, and derives its own
	// uptime history from what it observes each cycle (RecordTransportHeartbeat
	// → Redis per-date keys). Publishing 7–30 days of per-transport-per-day
	// bitmaps onto the announce feed was the root of the discovery gap — it
	// grew the Root to ~23k objects, so TPD couldn't fill it over the short
	// announce conn and under-reported the transport list. Historical
	// telemetry stays bbolt-only.
	slot := SlotForTime(now)
	if err := stx.MarkTransportSlot(idStr, now, slot); err != nil {
		t.log.WithError(err).WithField("tp_id", idStr).
			Debug("Stats: MarkTransportSlot failed")
	}
	return snapshotToEntry(tp.ID, rec.Current), nil
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

	// Prune day-start bandwidth baselines for transports not sampled today.
	// A live transport recreates its baseline (keyed to today) on its next
	// sample, so any entry still tagged with an earlier day belongs to a
	// transport that has gone away. Without this, baselines grows unbounded
	// with transport churn — every other prune path reclaimed records/bitmaps
	// but structurally never touched this map.
	today := now.UTC().Format(dateFmt)
	t.mu.Lock()
	for id, base := range t.baselines {
		if base.day != today {
			delete(t.baselines, id)
		}
	}
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
