// Package cxoaggregator pkg/deployment/tpd/cxoaggregator/checkpoints.go c4-net-discovery
//
// Checkpoint rows are applied in short open phases on a wall-clock cycle,
// not on every Root. Unpaced they were ~3.2k match scripts a minute and
// growing with the release (audit 2026-10-16).
//
// A match compares one edge's stored record with the other's, so the two
// records must come from about the same minute: a busy link moves more than
// the 128 MiB its recent checkpoints span in a few minutes. Visors sample on
// wall-clock minute boundaries, so an open phase aligned to the wall clock
// applies both edges' records of the same minutes. A row arriving between
// phases is held, the newest per edge, and applied when the next phase
// opens, beside the other edge's held row. The last minutes of each UTC day
// are an open phase too, so a day's verified bytes are credited to it.
// Verified bytes are the growth of the furthest matched offset, so skipping
// records in between loses none.
package cxoaggregator

import (
	"context"
	"crypto/sha256"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/telemetrywire"
)

// CheckpointSink is a Sink that matches transports' stream checkpoints.
type CheckpointSink interface {
	ApplyCheckpoints(ctx context.Context, id uuid.UUID, reporter cipher.PubKey, sent, recv telemetrywire.DirCheckpoints) error
}

const (
	// checkpointCycle and checkpointOpen: rows are applied during the first
	// checkpointOpen of every checkpointCycle, and the last checkpointOpen of
	// each UTC day.
	checkpointCycle = 10 * time.Minute
	checkpointOpen  = 2 * time.Minute
	// checkpointRefresh is how long an unchanged shard goes without being
	// applied again, well inside the store's 48 h checkpoint TTL.
	checkpointRefresh = time.Hour
	// checkpointFlushTimeout bounds applying the held rows.
	checkpointFlushTimeout = time.Minute
)

// checkpointPhaseOpen reports whether checkpoint rows are applied at t.
func checkpointPhaseOpen(t time.Time) bool {
	t = t.UTC()
	if t.Sub(t.Truncate(checkpointCycle)) < checkpointOpen {
		return true
	}
	return t.Truncate(24*time.Hour).Add(24*time.Hour).Sub(t) <= checkpointOpen
}

type heldCheckpoint struct {
	sent, recv telemetrywire.DirCheckpoints
}

type ckShardSeen struct {
	sum [32]byte
	at  time.Time
}

// checkpointPacer holds rows between open phases. Its zero value is ready.
type checkpointPacer struct {
	mu     sync.Mutex
	held   map[telKey]heldCheckpoint
	shards map[shardKey]ckShardSeen
}

// seen reports whether reporter's shard repeats the bytes last taken within
// checkpointRefresh, and records it otherwise.
func (p *checkpointPacer) seen(reporter cipher.PubKey, shard uint8, sum [32]byte, now time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.shards == nil {
		p.shards = make(map[shardKey]ckShardSeen)
	}
	k := shardKey{reporter, shard}
	if prev, ok := p.shards[k]; ok && prev.sum == sum && now.Sub(prev.at) < checkpointRefresh {
		return true
	}
	p.shards[k] = ckShardSeen{sum: sum, at: now}
	for sk, s := range p.shards {
		if now.Sub(s.at) > 2*checkpointRefresh {
			delete(p.shards, sk)
		}
	}
	return false
}

func (p *checkpointPacer) hold(k telKey, h heldCheckpoint) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.held == nil {
		p.held = make(map[telKey]heldCheckpoint)
	}
	p.held[k] = h
}

// drop forgets a held row that a newer one applied now supersedes.
func (p *checkpointPacer) drop(k telKey) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.held, k)
}

func (p *checkpointPacer) take() map[telKey]heldCheckpoint {
	p.mu.Lock()
	defer p.mu.Unlock()
	h := p.held
	p.held = nil
	return h
}

func (a *Aggregator) checkpointNow() time.Time {
	if a.ckClock != nil {
		return a.ckClock()
	}
	return time.Now()
}

// dispatchCheckpoints applies one transports/checkpoints/<sh> shard, or holds
// its rows until the next open phase.
func (a *Aggregator) dispatchCheckpoints(path string, leaf []byte, reporter cipher.PubKey) {
	sink, ok := a.sink.(CheckpointSink)
	if !ok {
		return
	}
	shard, entries, err := telemetrywire.DecodeCheckpointShard(leaf)
	if err != nil {
		a.log.WithError(err).WithField("path", path).Debug("CXO aggregator: checkpoint shard decode failed")
		return
	}
	now := a.checkpointNow()
	if a.ckPace.seen(reporter, shard, sha256.Sum256(leaf), now) {
		return // unchanged: matching it again against the same records finds nothing new
	}
	open := checkpointPhaseOpen(now)
	ctx, cancel := context.WithTimeout(context.Background(), telemetryShardTimeout)
	defer cancel()
	for _, e := range entries {
		if telemetrywire.ShardOf(e.ID) != shard {
			continue
		}
		k := telKey{id: e.ID, reporter: reporter}
		if !open {
			a.ckPace.hold(k, heldCheckpoint{sent: e.Sent, recv: e.Recv})
			continue
		}
		a.ckPace.drop(k)
		if err := sink.ApplyCheckpoints(ctx, e.ID, reporter, e.Sent, e.Recv); err != nil {
			a.log.WithError(err).WithField("transport", e.ID).Debug("CXO aggregator: ApplyCheckpoints failed")
			if ctx.Err() != nil {
				return
			}
		}
	}
}

// flushCheckpoints applies the held rows once a phase opens.
func (a *Aggregator) flushCheckpoints() {
	if !checkpointPhaseOpen(a.checkpointNow()) {
		return
	}
	sink, ok := a.sink.(CheckpointSink)
	if !ok {
		return
	}
	held := a.ckPace.take()
	if len(held) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), checkpointFlushTimeout)
	defer cancel()
	for k, h := range held {
		if ctx.Err() != nil {
			return
		}
		if err := sink.ApplyCheckpoints(ctx, k.id, k.reporter, h.sent, h.recv); err != nil {
			a.log.WithError(err).WithField("transport", k.id).Debug("CXO aggregator: ApplyCheckpoints failed")
		}
	}
}

// runCheckpointFlush flushes held rows on the telemetry flush cadence until
// ctx ends.
func (a *Aggregator) runCheckpointFlush(ctx context.Context) {
	t := time.NewTicker(telemetryFlushEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.flushCheckpoints()
		}
	}
}
