// Package skysocksc cmd/skywire-cli/commands/proxy/mux_info.go c4-vis-cli
package skysocksc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/cliout"
)

var (
	muxInfoApp      string
	muxInfoWatch    time.Duration
	muxInfoVerbose  bool
	muxInfoNDJSON   string
	muxInfoDuration time.Duration
)

func init() {
	muxInfoCmd.Flags().StringVarP(&muxInfoApp, "name", "n", "skysocks-client", "app name to query (e.g. skysocks-client, vpn-client)")
	muxInfoCmd.Flags().DurationVarP(&muxInfoWatch, "watch", "w", 0, "refresh interval; 0 prints once and exits (e.g. 1s, 500ms)")
	muxInfoCmd.Flags().BoolVarP(&muxInfoVerbose, "verbose", "v", false, "show full PKs / transport IDs (default: short hex prefixes)")
	muxInfoCmd.Flags().StringVar(&muxInfoNDJSON, "ndjson", "", "per-leg telemetry harness: stream NDJSON samples + lifecycle events to FILE ('-' for stdout); interval = --watch (default 1s)")
	muxInfoCmd.Flags().DurationVar(&muxInfoDuration, "duration", 0, "with --ndjson: stop after this long (0 = until ctrl+c)")
	addMuxSub(muxInfoCmd, "mux-info")
}

var muxInfoCmd = &cobra.Command{
	Use:   "info",
	Short: "Show per-mux-leg traffic for an active proxy session",
	Long: `Show per-mux-leg traffic for active route groups belonging to a named app.

Each row is one route within a mux'd route group. Sent/recv counts
are rg-scoped (only what this rg pushed through this leg) — distinct
from the transport-level totals 'tp ls' shows, which span all rg's
on that transport.

The query is local to this visor; what you see is your visor's view
of what it sent/received on each leg, not the peer's view.

Examples:
  skywire cli proxy mux info                    # one snapshot, default app
  skywire cli proxy mux info --watch 1s         # refresh every second
  skywire cli proxy mux info -n vpn-client      # query a different app
  skywire cli proxy mux info --json             # machine-readable`,
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck,gosec

		// Per-leg telemetry harness: NDJSON stream for the routing-policy rig.
		if muxInfoNDJSON != "" {
			runMuxTelemetry(cmd, func() (any, error) { return rpcClient.RouteGroupMuxInfo(muxInfoApp) })
			return
		}

		// One-shot
		if muxInfoWatch <= 0 {
			infos, err := rpcClient.RouteGroupMuxInfo(muxInfoApp)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("RouteGroupMuxInfo: %w", err))
			}
			newMuxRateTracker().render(cmd, infos)
			return
		}

		// Watch mode: clear screen between refreshes for a top-style view.
		// First emit fires immediately, then every muxInfoWatch. One tracker
		// lives across ticks so it can derive per-leg throughput from the byte
		// deltas between polls.
		ticker := time.NewTicker(muxInfoWatch)
		defer ticker.Stop()
		tracker := newMuxRateTracker()
		for {
			fmt.Print("\033[H\033[2J") // clear screen
			infos, err := rpcClient.RouteGroupMuxInfo(muxInfoApp)
			if err != nil {
				fmt.Fprintf(os.Stderr, "RouteGroupMuxInfo: %v\n", err)
			} else {
				tracker.render(cmd, infos)
			}
			fmt.Printf("\n[refresh %s — ctrl+c to stop]\n", muxInfoWatch)
			<-ticker.C
		}
	},
}

// muxRouteGroupInfo is a CLI-side mirror of visor.MuxRouteGroupInfo.
// We unmarshal the visor JSON into this rather than importing the
// visor package's struct because the json struct tags are the
// stable contract.
type muxRouteGroupInfo struct {
	Desc struct {
		DstPK   string `json:"dst_pk"`
		SrcPK   string `json:"src_pk"`
		DstPort int    `json:"dst_port"`
		SrcPort int    `json:"src_port"`
	} `json:"desc"`
	MuxEnabled      bool         `json:"mux_enabled"`
	SACKEnabled     bool         `json:"sack_enabled"`
	PerFrameNoise   bool         `json:"per_frame_noise"`
	Distribution    string       `json:"distribution,omitempty"`
	ReorderPending  int          `json:"reorder_pending,omitempty"`
	ReorderGapAgeMS float64      `json:"reorder_gap_age_ms,omitempty"`
	WriteSeq        uint32       `json:"write_seq,omitempty"`
	Legs            []muxLegInfo `json:"legs"`
}

type muxLegInfo struct {
	Index       int     `json:"index"`
	TransportID string  `json:"transport_id"`
	TpType      string  `json:"tp_type"`
	RemotePK    string  `json:"remote_pk"`
	LatencyMS   float64 `json:"latency_ms,omitempty"`
	SentBytes   uint64  `json:"sent_bytes"`
	SentPackets uint64  `json:"sent_packets"`
	RecvBytes   uint64  `json:"recv_bytes"`
	RecvPackets uint64  `json:"recv_packets"`
	Retransmits uint64  `json:"retransmits"`
	Alive       bool    `json:"alive"`
	Standby     bool    `json:"standby"`
}

// muxRateTracker remembers the previous poll's per-leg byte counters so
// --watch can render throughput (rate), each leg's share of the total, and an
// aggregate — the live "what am I getting" view — instead of just cumulative
// counters. Keyed by route-group descriptor + leg index (both stable across
// polls). A one-shot render uses a fresh tracker, so rates show "-".
type muxRateTracker struct {
	prev map[string]muxLegBytes
	at   time.Time
}

type muxLegBytes struct{ sent, recv uint64 }

func newMuxRateTracker() *muxRateTracker { return &muxRateTracker{prev: map[string]muxLegBytes{}} }

// humanRate renders a bytes/sec rate (0 or negative → "0B/s").
func humanRate(bytesPerSec float64) string {
	if bytesPerSec <= 0 {
		return "0B/s"
	}
	return humanBytes(uint64(bytesPerSec)) + "/s"
}

// renderMuxInfo prints the visor's mux info response. We re-marshal/
// unmarshal through the local muxRouteGroupInfo struct so the CLI's
// render logic doesn't reach into the visor package's internal type
// — the JSON contract is the stable boundary.
//
// infos is the value returned by rpcClient.RouteGroupMuxInfo; we
// take it as 'any' so the CLI doesn't import the visor type
// directly.
func (t *muxRateTracker) render(cmd *cobra.Command, infos any) {
	// JSON path: marshal the wire response directly. Re-encoding via
	// our local mirror struct preserves field naming and avoids
	// importing the visor type for json contract reasons.
	if cliout.JSONMode(cmd) {
		raw, _ := json.Marshal(infos) //nolint:errcheck
		var roundTripped []muxRouteGroupInfo
		_ = json.Unmarshal(raw, &roundTripped) //nolint:errcheck
		internal.Catch(cmd.Flags(), cliout.Print(cmd, roundTripped))
		return
	}

	// Re-marshal for the text path's data shape too — same JSON
	// contract end-to-end so add-fields-on-server-only never silently
	// drops out of the CLI render.
	raw, _ := json.Marshal(infos) //nolint:errcheck
	var rgs []muxRouteGroupInfo
	_ = json.Unmarshal(raw, &rgs) //nolint:errcheck

	if len(rgs) == 0 {
		fmt.Printf("no active route groups for app=%s\n", muxInfoApp)
		return
	}

	// Throughput is a rate, so it needs two samples. On the first render (or a
	// one-shot) t.at is zero — rates / share / aggregate show "-"; --watch
	// fills them in from the second tick onward.
	now := time.Now()
	elapsed := now.Sub(t.at).Seconds()
	haveRates := !t.at.IsZero() && elapsed > 0
	next := make(map[string]muxLegBytes)

	for ri, rg := range rgs {
		fmt.Printf("rg[%d] %s:%d → %s:%d  mux=%v sack=%v perframe=%v  legs=%d\n",
			ri,
			shortPK(rg.Desc.SrcPK), rg.Desc.SrcPort,
			shortPK(rg.Desc.DstPK), rg.Desc.DstPort,
			rg.MuxEnabled, rg.SACKEnabled, rg.PerFrameNoise, len(rg.Legs))
		if rg.MuxEnabled {
			// dist + reorder frontier: a rising pending with a growing gap age is
			// the head-of-line-blocking stall signal for a black-holing leg.
			gap := "0"
			if rg.ReorderGapAgeMS > 0 {
				gap = fmt.Sprintf("%.0fms", rg.ReorderGapAgeMS)
			}
			fmt.Printf("       dist=%s  reorder_pending=%d  gap_age=%s  write_seq=%d\n",
				rg.Distribution, rg.ReorderPending, gap, rg.WriteSeq)
		}

		// Sort legs by index so the row order is stable across snapshots.
		sort.SliceStable(rg.Legs, func(i, j int) bool { return rg.Legs[i].Index < rg.Legs[j].Index })

		// rg descriptor is the stable cross-poll key (the list index can shift).
		rgKey := fmt.Sprintf("%s:%d>%s:%d", rg.Desc.SrcPK, rg.Desc.SrcPort, rg.Desc.DstPK, rg.Desc.DstPort)

		// Pass 1: per-leg recv/sent rate from the byte delta since last poll,
		// plus the rg totals (for the aggregate line + each leg's share).
		recvRate := make([]float64, len(rg.Legs))
		sentRate := make([]float64, len(rg.Legs))
		var totRecv, totSent float64
		for li, leg := range rg.Legs {
			key := fmt.Sprintf("%s#%d", rgKey, leg.Index)
			if haveRates {
				if p, ok := t.prev[key]; ok {
					if leg.RecvBytes >= p.recv { // guard counter reset (rg recreated)
						recvRate[li] = float64(leg.RecvBytes-p.recv) / elapsed
					}
					if leg.SentBytes >= p.sent {
						sentRate[li] = float64(leg.SentBytes-p.sent) / elapsed
					}
				}
			}
			next[key] = muxLegBytes{sent: leg.SentBytes, recv: leg.RecvBytes}
			totRecv += recvRate[li]
			totSent += sentRate[li]
		}

		// Pass 2: render. recv/s, sent/s, share, health are the live signals;
		// recv/sent are the cumulative totals. share surfaces the self-healing
		// story — one leg at ~100% means the others are degraded/dropped.
		var b bytes.Buffer
		w := tabwriter.NewWriter(&b, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "  leg\ttp_id\ttype\tremote\tlatency\trecv/s\tsent/s\tshare\thealth\trecv\tsent") //nolint:errcheck
		active := 0
		for li, leg := range rg.Legs {
			lat := "-"
			if leg.LatencyMS > 0 {
				lat = fmt.Sprintf("%.0fms", leg.LatencyMS)
			}
			rrate, srate, share := "-", "-", "-"
			if haveRates {
				rrate, srate = humanRate(recvRate[li]), humanRate(sentRate[li])
				share = "0%"
				if totRecv > 0 {
					share = fmt.Sprintf("%.0f%%", recvRate[li]/totRecv*100)
				}
			}
			health := "idle"
			if recvRate[li] > 0 || sentRate[li] > 0 {
				health, active = "active", active+1
			}
			fmt.Fprintf(w, "  %d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", //nolint:errcheck
				leg.Index,
				shortTpID(leg.TransportID),
				leg.TpType,
				shortPK(leg.RemotePK),
				lat, rrate, srate, share, health,
				humanBytes(leg.RecvBytes), humanBytes(leg.SentBytes),
			)
		}
		w.Flush() //nolint:errcheck,gosec
		fmt.Print(b.String())
		if haveRates {
			fmt.Printf("  total  recv/s=%s  sent/s=%s  (%d/%d legs active)\n",
				humanRate(totRecv), humanRate(totSent), active, len(rg.Legs))
		}
		if ri < len(rgs)-1 {
			fmt.Println()
		}
	}

	t.prev = next
	t.at = now
}

func shortPK(pk string) string {
	if muxInfoVerbose || len(pk) <= 12 {
		return pk
	}
	return pk[:8] + "…" + pk[len(pk)-4:]
}

func shortTpID(id string) string {
	if muxInfoVerbose || len(id) <= 12 {
		return id
	}
	return id[:8] + "…"
}

func humanBytes(n uint64) string {
	const (
		k = 1024
		m = k * 1024
		g = m * 1024
	)
	switch {
	case n >= g:
		return fmt.Sprintf("%.1fG", float64(n)/float64(g))
	case n >= m:
		return fmt.Sprintf("%.1fM", float64(n)/float64(m))
	case n >= k:
		return fmt.Sprintf("%.1fK", float64(n)/float64(k))
	default:
		return fmt.Sprintf("%dB", n)
	}
}
