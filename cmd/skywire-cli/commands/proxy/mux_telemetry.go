// Package skysocksc cmd/skywire-cli/commands/proxy/mux_telemetry.go c4-vis-cli
package skysocksc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
)

// This is the per-leg telemetry harness for the WASM-routing-policy rig. It
// polls RouteGroupMuxInfo (the same rg.snapshotLegs() the policy on_tick ABI
// sees, so the harness and the policy read ONE source) and emits, on one clock:
//
//   - a "sample" record per route group each tick, carrying every live leg's
//     {route_index, intermediate_pk, transport_kind, inst_send_bps,
//     inst_recv_bps, rtt_ms, retransmits, gate_state, alive}; and
//   - leg-lifecycle event records (established / promoted / demoted / failed /
//     dropped) derived by diffing this tick's leg set against the last.
//
// gate_state is "standby" for a warm standby leg (rules kept, not sending) and
// "active" otherwise — so a warm-standby hot-swap shows as a demoted+promoted
// pair with no throughput dip on the sample series. Lifecycle events share the
// sample's timestamp, so a chart can render them as markers on the leg's line.
//
// Events are derived by polling, so their resolution is the sample interval: a
// leg that appears and vanishes within one tick is not observed. gate_state
// transitions and drops persist, so a ≥1 Hz poll catches them on the next tick.

// muxTeleLeg is one live leg in a sample record.
type muxTeleLeg struct {
	RouteIndex     int     `json:"route_index"`
	IntermediatePK string  `json:"intermediate_pk"`
	TransportKind  string  `json:"transport_kind"`
	InstSendBps    float64 `json:"inst_send_bps"`
	InstRecvBps    float64 `json:"inst_recv_bps"`
	RttMs          float64 `json:"rtt_ms"`
	Retransmits    uint64  `json:"retransmits"`
	GateState      string  `json:"gate_state"` // "active" | "standby"
	Alive          bool    `json:"alive"`
}

// muxTeleRecord is one NDJSON line: either a per-rg "sample" (Legs populated)
// or a leg-lifecycle event (RouteIndex/IntermediatePK/TransportKind populated).
type muxTeleRecord struct {
	Ts   string `json:"ts"`
	Type string `json:"type"` // sample | established | promoted | demoted | failed | dropped
	App  string `json:"app"`
	RG   string `json:"rg"`

	// sample only
	Legs []muxTeleLeg `json:"legs,omitempty"`

	// event only
	RouteIndex     int    `json:"route_index,omitempty"`
	IntermediatePK string `json:"intermediate_pk,omitempty"`
	TransportKind  string `json:"transport_kind,omitempty"`
}

// muxTeleLegState is the per-leg carry-over between polls, keyed by
// rgKey#index, used to compute inst rates and diff lifecycle events.
type muxTeleLegState struct {
	rg             string
	sent, recv     uint64
	standby, alive bool
	intermediatePK string
	transportKind  string
}

// muxTelemetry holds the previous poll's per-leg state so a poll can compute
// inst bps and emit lifecycle events. Zero value (via newMuxTelemetry) is
// ready; the first poll seeds state and emits an "established" per leg.
type muxTelemetry struct {
	app  string
	prev map[string]muxTeleLegState
	at   time.Time
}

func newMuxTelemetry(app string) *muxTelemetry {
	return &muxTelemetry{app: app, prev: map[string]muxTeleLegState{}}
}

// muxTeleKey is the stable cross-poll identity of a leg: rg descriptor + leg
// index (transports are appended, never re-ordered, so index is stable for the
// rg's life).
func muxTeleKey(rgKey string, index int) string {
	return fmt.Sprintf("%s#%d", rgKey, index)
}

func rgDescKey(rg muxRouteGroupInfo) string {
	return fmt.Sprintf("%s:%d>%s:%d", rg.Desc.SrcPK, rg.Desc.SrcPort, rg.Desc.DstPK, rg.Desc.DstPort)
}

// build turns one poll into its NDJSON records and advances the tracker. It is
// deterministic in (rgs, now) given the tracker's prior state, so it is unit
// testable without a live visor. Records are ordered: for each rg, lifecycle
// events (sorted by index) first, then that rg's sample; finally "dropped"
// events for legs that were present last poll but absent now.
func (mt *muxTelemetry) build(rgs []muxRouteGroupInfo, now time.Time) []muxTeleRecord {
	ts := now.UTC().Format(time.RFC3339Nano)
	elapsed := now.Sub(mt.at).Seconds()
	haveRates := !mt.at.IsZero() && elapsed > 0

	var out []muxTeleRecord
	next := make(map[string]muxTeleLegState)
	seen := make(map[string]bool)

	for _, rg := range rgs {
		rgKey := rgDescKey(rg)
		legs := append([]muxLegInfo(nil), rg.Legs...)
		sort.SliceStable(legs, func(i, j int) bool { return legs[i].Index < legs[j].Index })

		var events []muxTeleRecord
		sample := muxTeleRecord{Ts: ts, Type: "sample", App: mt.app, RG: rgKey}

		for _, leg := range legs {
			key := muxTeleKey(rgKey, leg.Index)
			seen[key] = true
			gate := "active"
			if leg.Standby {
				gate = "standby"
			}
			var sendBps, recvBps float64
			prev, hadPrev := mt.prev[key]
			if haveRates && hadPrev {
				if leg.SentBytes >= prev.sent {
					sendBps = float64(leg.SentBytes-prev.sent) / elapsed
				}
				if leg.RecvBytes >= prev.recv {
					recvBps = float64(leg.RecvBytes-prev.recv) / elapsed
				}
			}
			sample.Legs = append(sample.Legs, muxTeleLeg{
				RouteIndex:     leg.Index,
				IntermediatePK: leg.RemotePK,
				TransportKind:  leg.TpType,
				InstSendBps:    sendBps,
				InstRecvBps:    recvBps,
				RttMs:          leg.LatencyMS,
				Retransmits:    leg.Retransmits,
				GateState:      gate,
				Alive:          leg.Alive,
			})

			// Lifecycle events by diff against last poll. On the first poll a
			// leg is "established" (session start); thereafter only genuine
			// transitions fire.
			if !hadPrev {
				events = append(events, mt.event("established", ts, rgKey, leg))
			} else {
				if prev.standby && !leg.Standby {
					events = append(events, mt.event("promoted", ts, rgKey, leg))
				} else if !prev.standby && leg.Standby {
					events = append(events, mt.event("demoted", ts, rgKey, leg))
				}
				if prev.alive && !leg.Alive {
					events = append(events, mt.event("failed", ts, rgKey, leg))
				}
			}

			next[key] = muxTeleLegState{
				rg:             rgKey,
				sent:           leg.SentBytes,
				recv:           leg.RecvBytes,
				standby:        leg.Standby,
				alive:          leg.Alive,
				intermediatePK: leg.RemotePK,
				transportKind:  leg.TpType,
			}
		}

		out = append(out, events...)
		out = append(out, sample)
	}

	// "dropped": legs present last poll, absent now. Sort keys for determinism.
	var droppedKeys []string
	for key := range mt.prev {
		if !seen[key] {
			droppedKeys = append(droppedKeys, key)
		}
	}
	sort.Strings(droppedKeys)
	for _, key := range droppedKeys {
		st := mt.prev[key]
		out = append(out, muxTeleRecord{
			Ts: ts, Type: "dropped", App: mt.app, RG: st.rg,
			RouteIndex:     muxTeleIndexFromKey(key),
			IntermediatePK: st.intermediatePK,
			TransportKind:  st.transportKind,
		})
	}

	mt.prev = next
	mt.at = now
	return out
}

func (mt *muxTelemetry) event(kind, ts, rgKey string, leg muxLegInfo) muxTeleRecord {
	return muxTeleRecord{
		Ts: ts, Type: kind, App: mt.app, RG: rgKey,
		RouteIndex:     leg.Index,
		IntermediatePK: leg.RemotePK,
		TransportKind:  leg.TpType,
	}
}

// runMuxTelemetry drives the per-leg telemetry harness: poll RouteGroupMuxInfo
// on a fixed interval and write each poll's NDJSON records to the sink until
// ctrl+c or --duration elapses. poll returns the raw RPC value, which we
// round-trip through JSON into the CLI mirror struct (the JSON tags are the
// stable contract), so the emitter never imports the visor type.
func runMuxTelemetry(cmd *cobra.Command, poll func() (any, error)) {
	interval := muxInfoWatch
	if interval <= 0 {
		interval = time.Second // the harness's ">=1 Hz" default
	}

	// Sink: FILE (append) or stdout for "-".
	sink := os.Stdout
	if muxInfoNDJSON != "-" {
		f, err := os.OpenFile(muxInfoNDJSON, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("open --ndjson %s: %w", muxInfoNDJSON, err))
		}
		defer f.Close() //nolint:errcheck,gosec
		sink = f
		fmt.Fprintf(os.Stderr, "mux telemetry: app=%s interval=%s → %s (ctrl+c to stop)\n", muxInfoApp, interval, muxInfoNDJSON)
	}
	enc := json.NewEncoder(sink)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var deadline time.Time
	if muxInfoDuration > 0 {
		deadline = time.Now().Add(muxInfoDuration)
	}

	mt := newMuxTelemetry(muxInfoApp)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	writeOnce := func() {
		infos, err := poll()
		if err != nil {
			fmt.Fprintf(os.Stderr, "RouteGroupMuxInfo: %v\n", err)
			return
		}
		raw, _ := json.Marshal(infos) //nolint:errcheck
		var rgs []muxRouteGroupInfo   //nolint:prealloc
		_ = json.Unmarshal(raw, &rgs) //nolint:errcheck
		for _, rec := range mt.build(rgs, time.Now()) {
			_ = enc.Encode(rec) //nolint:errcheck
		}
	}

	writeOnce() // first sample fires immediately
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			writeOnce()
			if !deadline.IsZero() && time.Now().After(deadline) {
				return
			}
		}
	}
}

// muxTeleIndexFromKey pulls the leg index back out of an "rgKey#index" key.
// Returns 0 if malformed (never happens for keys we mint).
func muxTeleIndexFromKey(key string) int {
	i := -1
	for p := len(key) - 1; p >= 0; p-- {
		if key[p] == '#' {
			i = p
			break
		}
	}
	if i < 0 || i+1 >= len(key) {
		return 0
	}
	var idx int
	if _, err := fmt.Sscanf(key[i+1:], "%d", &idx); err != nil {
		return 0
	}
	return idx
}
