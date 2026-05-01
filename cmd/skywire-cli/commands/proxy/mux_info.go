// Package skysocksc cmd/skywire-cli/commands/proxy/mux_info.go
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
)

var (
	muxInfoApp     string
	muxInfoWatch   time.Duration
	muxInfoVerbose bool
)

func init() {
	muxInfoCmd.Flags().StringVarP(&muxInfoApp, "name", "n", "skysocks-client", "app name to query (e.g. skysocks-client, vpn-client)")
	muxInfoCmd.Flags().DurationVarP(&muxInfoWatch, "watch", "w", 0, "refresh interval; 0 prints once and exits (e.g. 1s, 500ms)")
	muxInfoCmd.Flags().BoolVarP(&muxInfoVerbose, "verbose", "v", false, "show full PKs / transport IDs (default: short hex prefixes)")
	RootCmd.AddCommand(muxInfoCmd)
}

var muxInfoCmd = &cobra.Command{
	Use:   "mux-info",
	Short: "Show per-mux-leg traffic for an active proxy session",
	Long: `Show per-mux-leg traffic for active route groups belonging to a named app.

Each row is one route within a mux'd route group. Sent/recv counts
are rg-scoped (only what this rg pushed through this leg) — distinct
from the transport-level totals 'tp ls' shows, which span all rg's
on that transport.

The query is local to this visor; what you see is your visor's view
of what it sent/received on each leg, not the peer's view.

Examples:
  skywire cli proxy mux-info                   # one snapshot, default app
  skywire cli proxy mux-info --watch 1s        # refresh every second
  skywire cli proxy mux-info -n vpn-client     # query a different app
  skywire cli proxy mux-info --json            # machine-readable`,
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck,gosec

		// One-shot
		if muxInfoWatch <= 0 {
			infos, err := rpcClient.RouteGroupMuxInfo(muxInfoApp)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("RouteGroupMuxInfo: %w", err))
			}
			renderMuxInfo(cmd, infos)
			return
		}

		// Watch mode: clear screen between refreshes for a top-style view.
		// First emit fires immediately, then every muxInfoWatch.
		ticker := time.NewTicker(muxInfoWatch)
		defer ticker.Stop()
		for {
			fmt.Print("\033[H\033[2J") // clear screen
			infos, err := rpcClient.RouteGroupMuxInfo(muxInfoApp)
			if err != nil {
				fmt.Fprintf(os.Stderr, "RouteGroupMuxInfo: %v\n", err)
			} else {
				renderMuxInfo(cmd, infos)
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
	MuxEnabled  bool         `json:"mux_enabled"`
	SACKEnabled bool         `json:"sack_enabled"`
	Legs        []muxLegInfo `json:"legs"`
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
}

// renderMuxInfo prints the visor's mux-info response. We re-marshal/
// unmarshal through the local muxRouteGroupInfo struct so the CLI's
// render logic doesn't reach into the visor package's internal type
// — the JSON contract is the stable boundary.
//
// infos is the value returned by rpcClient.RouteGroupMuxInfo; we
// take it as 'any' so the CLI doesn't import the visor type
// directly.
func renderMuxInfo(cmd *cobra.Command, infos any) {
	// JSON path: marshal the wire response directly. Re-encoding via
	// our local mirror struct preserves field naming and avoids
	// importing the visor type for json contract reasons.
	isJSON, _ := cmd.Flags().GetBool(internal.JSONString) //nolint:errcheck
	if isJSON {
		raw, _ := json.Marshal(infos) //nolint:errcheck
		var roundTripped []muxRouteGroupInfo
		_ = json.Unmarshal(raw, &roundTripped)               //nolint:errcheck
		out, _ := json.MarshalIndent(roundTripped, "", "  ") //nolint:errcheck
		fmt.Println(string(out))
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

	for ri, rg := range rgs {
		fmt.Printf("rg[%d] %s:%d → %s:%d  mux=%v sack=%v  legs=%d\n",
			ri,
			shortPK(rg.Desc.SrcPK), rg.Desc.SrcPort,
			shortPK(rg.Desc.DstPK), rg.Desc.DstPort,
			rg.MuxEnabled, rg.SACKEnabled, len(rg.Legs))

		// Sort legs by index so the row order is stable across snapshots.
		sort.SliceStable(rg.Legs, func(i, j int) bool { return rg.Legs[i].Index < rg.Legs[j].Index })

		var b bytes.Buffer
		w := tabwriter.NewWriter(&b, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "  leg\ttp_id\ttype\tremote\tlatency\tsent\tsent_pkts\trecv\trecv_pkts") //nolint:errcheck
		for _, leg := range rg.Legs {
			lat := "-"
			if leg.LatencyMS > 0 {
				lat = fmt.Sprintf("%.0fms", leg.LatencyMS)
			}
			fmt.Fprintf(w, "  %d\t%s\t%s\t%s\t%s\t%s\t%d\t%s\t%d\n", //nolint:errcheck
				leg.Index,
				shortTpID(leg.TransportID),
				leg.TpType,
				shortPK(leg.RemotePK),
				lat,
				humanBytes(leg.SentBytes), leg.SentPackets,
				humanBytes(leg.RecvBytes), leg.RecvPackets,
			)
		}
		w.Flush() //nolint:errcheck,gosec
		fmt.Print(b.String())
		if ri < len(rgs)-1 {
			fmt.Println()
		}
	}
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
