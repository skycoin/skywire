// Package cxoaggregator pkg/deployment/tpd/cxoaggregator/telemetry_batch.go c4-net-discovery
//
// A telemetry shard carries many transports; applying them one by one was a
// store round trip per script per transport. A shard's due snapshots and due
// heartbeats are now handed to the store together when it can batch them.
package cxoaggregator

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/deployment/tpd/store"
	"github.com/skycoin/skywire/pkg/transport"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// batchSink is implemented by sinks whose store applies telemetry and
// heartbeats in batches (store.TelemetryBatchStore, store.BatchStore).
type batchSink interface {
	ApplyTelemetry(ctx context.Context, updates []store.TelemetryUpdate) error
	RecordTransportHeartbeats(ctx context.Context, entries []*transport.Entry, at time.Time) error
}

// beatWanted reports whether transport id is due an uptime heartbeat now,
// recording it if so (see telemetryState.beatDue).
func (a *Aggregator) beatWanted(id uuid.UUID, tpType string, now time.Time) bool {
	return tpType != "" && a.tel.beatDue(id, now)
}

// applyDue writes the given snapshots, in one batch when the sink can.
func (a *Aggregator) applyDue(ctx context.Context, due []heldSnap) {
	if len(due) == 0 {
		return
	}
	bs, ok := a.sink.(batchSink)
	if !ok {
		for _, h := range due {
			if ctx.Err() != nil {
				return // out of time; every remaining write would fail
			}
			a.applySnap(ctx, h.k, h.s)
		}
		return
	}
	updates := make([]store.TelemetryUpdate, len(due))
	for i, h := range due {
		updates[i] = store.TelemetryUpdate{
			ID: h.k.id, Reporter: h.k.reporter,
			Sent: h.s.sent, Recv: h.s.recv, ThroughputBps: h.s.throughput,
			LatMin: h.s.latMin, LatMax: h.s.latMax, LatAvg: h.s.latAvg,
			Type: h.s.tpType,
		}
	}
	if err := bs.ApplyTelemetry(ctx, updates); err != nil {
		a.log.WithError(err).WithField("transports", len(updates)).Debug("CXO aggregator: batched telemetry apply failed")
	}
}

// beatItem is one due heartbeat. at is when the visor saw the transport up
// (its sample time), so a heartbeat crossing a 5-minute slot boundary in
// transit still credits the slot the transport was up in.
type beatItem struct {
	id     uuid.UUID
	tpType string
	at     time.Time
}

// recordBeats writes the given heartbeats: one batch per sample time when
// the sink can batch (a shard's rows usually share one).
func (a *Aggregator) recordBeats(ctx context.Context, beats []beatItem) {
	if len(beats) == 0 {
		return
	}
	bs, ok := a.sink.(batchSink)
	if !ok {
		for _, b := range beats {
			if ctx.Err() != nil {
				return
			}
			if err := a.sink.RecordTransportHeartbeat(ctx, b.id, b.tpType, b.at); err != nil {
				a.log.WithError(err).WithField("transport", b.id).Debug("CXO aggregator: RecordTransportHeartbeat failed")
			}
		}
		return
	}
	byAt := make(map[time.Time][]*transport.Entry)
	for _, b := range beats {
		byAt[b.at] = append(byAt[b.at], &transport.Entry{ID: b.id, Type: types.Type(b.tpType)})
	}
	for at, entries := range byAt {
		if err := bs.RecordTransportHeartbeats(ctx, entries, at); err != nil {
			a.log.WithError(err).WithField("transports", len(entries)).Debug("CXO aggregator: batched heartbeats failed")
		}
	}
}
