// Package cxoaggregator pkg/deployment/tpd/cxoaggregator/telemetry_dedup.go c4-net-discovery
//
// A visor re-Puts a telemetry shard only when its content changes (the
// tracker's shardSig ignores sampled_at), but every Root — the 45 s
// heartbeat included — carries every shard, and TPD applied every shard of
// every Root. With both edges of ~90k transports reporting that was ~4k
// telemetry applies a second, each a bandwidth script, a latency script,
// a throughput write and an uptime heartbeat, re-writing what the store
// already held: the bulk of prod redis's load (2026-09-29).
//
// An unchanged shard is now applied once. A Root still proves its visor
// and the transports it lists are up, so those get their uptime
// heartbeat — but a transport gets at most one per heartbeatEvery, the
// cadence the store's uptime denominator assumes, not one per Root per
// edge: counting every ingest inflated a live transport's count two to
// four times over and dated its timeline slot to its last change.
package cxoaggregator

import (
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
)

// heartbeatEvery paces the uptime heartbeats one transport's telemetry
// triggers. Well under the 5-minute timeline slot, so no slot is missed; the
// store writes one heartbeat per slot whatever the rate.
const heartbeatEvery = 90 * time.Second

// telemetryStateSweepAfter bounds the memory the dedup state holds: entries
// untouched this long belong to transports or shards that are gone.
const telemetryStateSweepAfter = 30 * time.Minute

type shardKey struct {
	reporter cipher.PubKey
	shard    uint8
}

type shardSeen struct {
	sum [32]byte
	at  time.Time
}

// telemetryState is the dedup state. Its zero value is ready to use.
type telemetryState struct {
	mu        sync.Mutex
	shards    map[shardKey]shardSeen
	beats     map[uuid.UUID]time.Time
	lastSweep time.Time
}

// unchanged reports whether reporter's shard carries the bytes last
// applied in full, refreshing the entry's age if so.
func (t *telemetryState) unchanged(reporter cipher.PubKey, shard uint8, sum [32]byte, now time.Time) bool {
	k := shardKey{reporter, shard}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sweepLocked(now)
	prev, ok := t.shards[k]
	if !ok || prev.sum != sum {
		return false
	}
	t.shards[k] = shardSeen{sum: sum, at: now}
	return true
}

// applied records that reporter's shard with these bytes was applied in
// full. Only then: a shard whose apply ran out of time is applied again.
func (t *telemetryState) applied(reporter cipher.PubKey, shard uint8, sum [32]byte, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.shards == nil {
		t.shards = make(map[shardKey]shardSeen)
	}
	t.shards[shardKey{reporter, shard}] = shardSeen{sum: sum, at: now}
}

// beatDue reports whether transport id is due an uptime heartbeat at now,
// and records it if so.
func (t *telemetryState) beatDue(id uuid.UUID, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.beats == nil {
		t.beats = make(map[uuid.UUID]time.Time)
	}
	if last, ok := t.beats[id]; ok && now.Sub(last) < heartbeatEvery {
		return false
	}
	t.beats[id] = now
	return true
}

// sweepLocked drops entries untouched for telemetryStateSweepAfter, at
// most once per that interval. Caller holds t.mu.
func (t *telemetryState) sweepLocked(now time.Time) {
	if now.Sub(t.lastSweep) < telemetryStateSweepAfter {
		return
	}
	t.lastSweep = now
	for k, s := range t.shards {
		if now.Sub(s.at) > telemetryStateSweepAfter {
			delete(t.shards, k)
		}
	}
	for id, at := range t.beats {
		if now.Sub(at) > telemetryStateSweepAfter {
			delete(t.beats, id)
		}
	}
}
