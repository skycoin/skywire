//go:build js && wasm

// Package main cmd/wasm-visor/telemetry_js.go c3-vis-wasm
// bandwidth + latency to TPD by publishing a CXO TreeStore feed that TPD's
// cxo-aggregator subscribes to (pkg/visor/init_stats.go). A browser tab has no
// filesystem, so it can't use the native bbolt-backed stats.Store — but the CXO
// publisher itself runs fine with an IN-MEMORY datastore (treestore InMemoryDB;
// the CXDS/IdxDB drives are build-tag-split so this compiles under js/wasm).
//
// This wires that up: an in-memory publisher, the transport manager's entry/
// tombstone leaves, a per-transport transports/<uuid>/current leaf (bandwidth +
// latency) sampled each minute, and an announce loop so TPD subscribes. Without
// it a browser visor's transports never reach TPD /metrics (invisible to rewards).
// Telemetry resets on tab reload (no persistence) — fine; TPD retains published
// rollups within its own window.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
)

// liveSnapshot mirrors the wire shape TPD's cxo-aggregator parses for
// transports/<uuid>/current (bandwidth + latency). The aggregator re-declares the
// same struct on its side to keep the dependency one-way; field JSON tags must
// match both places (pkg/deployment/tpd/cxoaggregator/aggregator.go).
type liveSnapshot struct {
	SentBytes    uint64    `json:"sent_bytes"`
	RecvBytes    uint64    `json:"recv_bytes"`
	LatencyMinMS float64   `json:"latency_min_ms,omitempty"`
	LatencyMaxMS float64   `json:"latency_max_ms,omitempty"`
	LatencyAvgMS float64   `json:"latency_avg_ms,omitempty"`
	SampledAt    time.Time `json:"sampled_at"`
	Type         string    `json:"type,omitempty"`
}

var telemetryPub *treestore.Publisher

const (
	telemetrySampleInterval   = time.Minute
	telemetryAnnounceInterval = 30 * time.Second
)

// startTelemetry brings up the in-memory CXO publisher, wires the transport
// manager to publish transports/<uuid>/entry + tombstone leaves, samples each live
// transport's bandwidth/latency into transports/<uuid>/current every minute, and
// announces to TPD so its cxo-aggregator subscribes to this visor's feed. Called
// once from bootEdge after the transport manager is serving. Best-effort: any
// failure leaves the rest of the visor running.
func startTelemetry(sk cipher.SecKey, tpdPK cipher.PubKey, log *logging.Logger) {
	if dmsgC == nil || tpM == nil {
		return
	}
	pub, err := treestore.NewWithDMSG(dmsgC, sk, treestore.PubConfig{
		InMemoryDB:  true, // no filesystem in a tab — content-addressed cache is fine in memory
		BatchWindow: 10 * time.Second,
		Logger:      log,
	})
	if err != nil {
		vlog("telemetry: CXO publisher init failed: " + err.Error())
		return
	}
	telemetryPub = pub
	telemetryTPDPK = tpdPK
	// Wrap the publisher so the expanded visorStats() can introspect exactly what
	// transport list we publish to TPD (recordingLeafPublisher is observation-only;
	// it delegates every Put/Delete to pub).
	tpM.SetTPDLeafPublisher(recordingLeafPublisher{inner: pub}) // publishes the tp-list snapshot leaf TPD ingests
	vlog(fmt.Sprintf("telemetry: in-memory CXO publisher up (feed %s) — reporting transport bandwidth+latency to TPD %s",
		pub.Feed().Hex()[:8], tpdPK.Hex()[:8]))
	go telemetrySampleLoop()
	go telemetryAnnounceLoop(tpdPK)
}

func telemetrySampleLoop() {
	t := time.NewTicker(telemetrySampleInterval)
	defer t.Stop()
	sampleTransports()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sampleTransports()
		}
	}
}

// sampleTransports publishes a transports/<uuid>/current leaf per live transport,
// carrying its cumulative bandwidth + latency stats.
func sampleTransports() {
	if tpM == nil || telemetryPub == nil {
		return
	}
	now := time.Now()
	tpM.WalkTransports(func(tp *transport.ManagedTransport) bool {
		if tp.IsClosed() {
			return true
		}
		bw := tp.GetBandwidth()
		lat := tp.GetLatencyStats()
		snap := liveSnapshot{
			SentBytes:    bw.SentBytes,
			RecvBytes:    bw.RecvBytes,
			LatencyMinMS: lat.Min,
			LatencyMaxMS: lat.Max,
			LatencyAvgMS: lat.Avg,
			SampledAt:    now,
			Type:         string(tp.Entry.Type),
		}
		if b, err := json.Marshal(snap); err == nil {
			if perr := telemetryPub.Put("transports/"+tp.Entry.ID.String()+"/current", b); perr != nil {
				vlog("telemetry: publish current failed: " + perr.Error())
			}
		}
		return true
	})
}

func telemetryAnnounceLoop(tpdPK cipher.PubKey) {
	t := time.NewTicker(telemetryAnnounceInterval)
	defer t.Stop()
	announce := func() {
		if telemetryPub == nil {
			return
		}
		// Per-attempt deadline: AnnounceTo dials TPD over dmsg (can block on a
		// half-dead transport); cap it so a stuck dial can't starve the next tick.
		dctx, cancel := context.WithTimeout(ctx, telemetryAnnounceInterval/2)
		defer cancel()
		// Feed failures into self-recovery (weaker signal than the uptime
		// heartbeat, so we only report failures here, not successes). A single
		// announce miss is transient and retried next tick; recovery only acts
		// after recoveryFailThreshold consecutive reports.
		err := telemetryPub.AnnounceTo(dctx, tpdPK)
		recordAnnounceResult(err) // introspection: track announce reach (TPD subscribes only after an OK)
		if err != nil {
			reportDmsgFailure("telemetry announce failed")
		}
	}
	announce()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			announce()
		}
	}
}
