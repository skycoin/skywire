// Package ping cmd/skywire-cli/commands/visor/ping/mux_bandwidth_tui.go c4-vis-cli
// Bubble Tea TUI consumer of the StreamMuxBandwidth gRPC RPC.
//
// Operator-visible: `cli visor ping mux-bw-tui <pk>` runs the same
// multiplexed-route bandwidth test as `mux-bw` but presents it as a
// live dashboard instead of NDJSON: per-route status row,
// throughput sparkline, RTT-probe sparkline (when --probe-rtt is
// set), and a scrollable events log.
//
// Identical flags to `mux-bw`; the only difference is rendering.
// Both subcommands ride the same server-side RPC handler, so a
// change at the server lands in both.
package ping

import (
	"time"

	"github.com/spf13/cobra"
)

func init() {
	// All flags mirror `mux-bw` exactly — the variables are
	// shared across files in this package so the TUI command picks
	// up the same defaults / parser as the NDJSON command.
	muxBandwidthTUICmd.Flags().IntVarP(&muxBwRoutes, "routes", "r", 1,
		"number of parallel route connections (1 = baseline single route)")
	muxBandwidthTUICmd.Flags().DurationVarP(&muxBwDuration, "duration", "d", 30*time.Second,
		"how long to pump bytes (excludes route-setup time)")
	muxBandwidthTUICmd.Flags().IntVarP(&muxBwPacketSizeKb, "size", "s", 32,
		"per-write block size in KB")
	muxBandwidthTUICmd.Flags().IntVar(&muxBwMinHops, "min-hops", 0,
		"route-finder min hops constraint (>=2 excludes the direct transport)")
	muxBandwidthTUICmd.Flags().DurationVar(&muxBwSetupTimeout, "setup-timeout", 30*time.Second,
		"per-route setup timeout")
	muxBandwidthTUICmd.Flags().BoolVar(&muxBwProbeRTT, "probe-rtt", false,
		"also send small-packet RTT probes during the load (queueing-delay measurement)")
	muxBandwidthTUICmd.Flags().DurationVar(&muxBwProbeInterval, "probe-interval", 100*time.Millisecond,
		"interval between RTT probes when --probe-rtt is set")
	muxBandwidthTUICmd.Flags().DurationVar(&muxBwSampleInterval, "sample-interval", 1*time.Second,
		"interval between MuxBandwidthSample events")
	muxBandwidthTUICmd.Flags().BoolVar(&muxBwLocalRoute, "local-route", false,
		"use locally-cached TPD data for route calculation (faster setup; may be stale)")

	RootCmd.AddCommand(muxBandwidthTUICmd)
}

var muxBandwidthTUICmd = &cobra.Command{
	Use:   "mux-bw-tui <pk>",
	Short: "Interactive Bubble Tea TUI for the multiplexed-route bandwidth probe",
	Long: `Live dashboard for StreamMuxBandwidth — same RPC as
'cli visor ping mux-bw', different rendering.

The screen shows:
  * Per-route status row — one tile per route with state, hop count, setup time
  * Live stats line — instant + avg + peak throughput, bytes pumped, active routes
  * Throughput sparkline — last 60 sample-interval ticks
  * RTT sparkline (when --probe-rtt) — last 60 probes
  * Events log — scrollable viewport

For automation / harness consumption use the NDJSON sibling
'cli visor ping mux-bw' instead.

Controls inside the TUI:
  ↑/k, ↓/j     scroll events one line
  PgUp/PgDn    page up/down
  Home/End     top/bottom
  a            toggle auto-scroll
  q/Ctrl+C     quit (results print to stdout on exit)`,
	Args: cobra.ExactArgs(1),
	Run:  runMuxBandwidthTUI,
}
