// Package ping cmd/skywire-cli/commands/visor/ping/tree.go c4-vis-cli
// interactive Bubble Tea TUI for `cli visor ping tree`.
//
// History: this command used to host a ~2300-line client-side BFS
// + concurrency-limited ping orchestrator + state machine. That code
// had several structural problems on visors with hundreds of
// transports: default concurrency=2 capped throughput at ~4 pings/
// minute (so level 1 of a 500-transport visor took >2 hours and
// level 2 never started), the renderer hid pending entries (making
// progress invisible), and the Bubble Tea TUI's /dev/tty
// requirement made the tool undriveable from CI or coding agents.
//
// #2732 moved the BFS server-side as the StreamPingTree gRPC RPC.
// This file now consumes that stream and renders it with the same
// Bubble Tea TUI shape — header, stats line, scrollable tree
// viewport, footer — that operators were already familiar with.
//
// The NDJSON-driven sibling lives in tree_stream.go (`cli visor ping
// tree-stream`); it feeds the same events to stdout for treeprobe +
// CI consumers. Both subcommands ride the same server-side BFS
// implementation, so a fix at the server lands in both.
package ping

import (
	"time"

	"github.com/spf13/cobra"
)

// ---------------------------------------------------------------------------
// Flags
// ---------------------------------------------------------------------------

// pingTreeFlags is the subset of PingTreeRequest fields that operators
// commonly override. Server-side defaults (in normalizePingTreeRequest)
// kick in when a flag is zero. Flags that controlled client-side BFS
// state machinery in the old implementation (--concurrency, --continuous,
// --max-age, --remove-tp, --remake-tp, etc.) are dropped: concurrency is
// a server-side knob now, continuous-rerun is dead weight for a one-
// shot measurement tool, and transport mutations belong in cli tp.
type pingTreeFlags struct {
	MaxLevel     int
	Hops         int
	Tries        int
	Size         int
	Concurrency  int
	Timeout      time.Duration
	SetupTimeout time.Duration
	OnlineOnly   bool
	Version      string
	UseTpLat     bool
	DmsgOnly     bool
	DmsgPreCheck bool
	Retries      int
	DryRun       bool
	OutputFile   string
}

var treeFlags pingTreeFlags

func init() {
	pingTreeCmd.Flags().IntVarP(&treeFlags.MaxLevel, "max-level", "l", 0,
		"maximum BFS depth (0 = unlimited until expansion exhausts)")
	pingTreeCmd.Flags().IntVar(&treeFlags.Hops, "hops", 0,
		"ping ONLY entries at exactly N hops; other levels are discovered but not pinged")
	pingTreeCmd.Flags().IntVarP(&treeFlags.Tries, "tries", "t", 1,
		"per-transport ping count; PingResult carries aggregated stats")
	pingTreeCmd.Flags().IntVarP(&treeFlags.Size, "size", "s", 2,
		"packet size in KB")
	pingTreeCmd.Flags().IntVarP(&treeFlags.Concurrency, "concurrency", "c", 0,
		"max in-flight pings per BFS level (0 = server default, currently 16)")
	pingTreeCmd.Flags().DurationVarP(&treeFlags.Timeout, "timeout", "o", 30*time.Second,
		"per-ping timeout (after route setup)")
	pingTreeCmd.Flags().DurationVar(&treeFlags.SetupTimeout, "setup-timeout", 30*time.Second,
		"per-transport route-setup timeout")
	pingTreeCmd.Flags().BoolVarP(&treeFlags.OnlineOnly, "online", "g", false,
		"only ping visors marked online in the uptime tracker")
	pingTreeCmd.Flags().StringVarP(&treeFlags.Version, "version", "v", "",
		"filter by minimum visor version (semver)")
	pingTreeCmd.Flags().BoolVar(&treeFlags.UseTpLat, "use-transport-latency", true,
		"at level 1: skip the live ping when the transport already has a smoothed RTT in TransportSummary.LatencyMS")
	pingTreeCmd.Flags().BoolVar(&treeFlags.DmsgOnly, "dmsg-only", false,
		"force the ping path to ride DMSG instead of the skywire router")
	pingTreeCmd.Flags().BoolVar(&treeFlags.DmsgPreCheck, "dmsg-precheck", false,
		"probe DMSG reachability before each route ping; discards unreachable visors early")
	pingTreeCmd.Flags().IntVar(&treeFlags.Retries, "retries", 0,
		"retry attempts on failed pings")
	pingTreeCmd.Flags().BoolVar(&treeFlags.DryRun, "dry-run", false,
		"discovery only; no PingResult events fire (every entry marked latency_source=skipped)")
	pingTreeCmd.Flags().StringVarP(&treeFlags.OutputFile, "output", "O", "",
		"append per-event NDJSON to FILE as the run progresses (for offline analysis)")

	RootCmd.AddCommand(pingTreeCmd)
}

// ---------------------------------------------------------------------------
// Cobra command
// ---------------------------------------------------------------------------

var pingTreeCmd = &cobra.Command{
	Use:   "tree",
	Short: "Interactive Bubble Tea TUI for the ping-tree (server-side BFS over the skywire route graph)",
	Long: `Walk the visor's neighborhood breadth-first, pinging each
discovered visor and rendering the results as a scrollable tree.

The BFS runs server-side via the StreamPingTree gRPC RPC (see
#2732 / pkg/visor/rpcgrpc/server_ping_tree.go); this command is a
thin Bubble Tea TUI on top of that stream.

The non-interactive sibling 'cli visor ping tree-stream' emits the
same events as NDJSON on stdout — use that one for CI, coding-agent
automation, or piping into the treeprobe harness (pkg/util/treeprobe).

Examples:

  # Walk all reachable levels:
  skywire cli visor ping tree

  # Only level 1 (direct neighbors), 5 ping samples each:
  skywire cli visor ping tree --max-level 1 --tries 5

  # Specific hop count for latency-by-hops measurement:
  skywire cli visor ping tree --hops 2 --max-level 2 --tries 5

  # Discovery-only, no pings (visualize the reachable graph):
  skywire cli visor ping tree --dry-run --max-level 2

Controls inside the TUI:
  ↑/k, ↓/j     scroll one line
  PgUp/PgDn    page up/down
  Home/End     top/bottom
  a            toggle auto-scroll
  q/Ctrl+C     quit`,
	Run: runPingTree,
}
