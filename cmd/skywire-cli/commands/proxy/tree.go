// Package skysocksc cmd/skywire-cli/commands/proxy/tree.go c4-vis-cli
package skysocksc

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/0magnet/bitree"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/cliout"
	"github.com/skycoin/skywire/pkg/proxystatus"
)

var treeColor string

func init() {
	RootCmd.AddCommand(treeCmd)
	treeCmd.Flags().StringVarP(&clientName, "name", "n", "skysocks-client", "name of the running proxy client to inspect")
	treeCmd.Flags().StringVar(&treeColor, "color", "auto", "colorize the tree: auto|always|never")
}

var treeCmd = &cobra.Command{
	Use:   "tree",
	Short: "Render a running proxy's route group as a bilateral route tree",
	Long: `Render an already-running proxy client's live route group as a bilateral
route tree — the SAME model + shape the proxy status page
(http://status.skysocks/) draws, rooted at this visor.

Each active route is a branch: its hop chain to the exit (each hop
carrying its transport [type] · id · rtt), with a left summary block
(R[n], a state dot ● active / ○ standby, the end-to-end route rtt, and
its bandwidth X↑ Y↓). Dead legs are pruned. Public keys are never
truncated.

This is read-only and attach-only: it queries the visor's route-group
telemetry over RPC and never starts, stops, or reshapes the app. It is
the tree companion to 'proxy log' (which streams the events/log).

  proxy tree                  render skysocks-client's route tree
  proxy tree -n vpn-client    render a different client's route tree
  proxy tree --json           machine-readable route-group legs
  proxy tree --color never    plain (no ANSI), e.g. for piping`,
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, _ []string) {
		if clientName == "" {
			clientName = "skysocks-client"
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck,gosec

		infos, err := rpcClient.RouteGroupMuxInfo(clientName)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("RouteGroupMuxInfo: %w", err))
		}

		// JSON contract is the stable boundary: re-marshal the wire response and
		// decode it into a local mirror (so this command doesn't import the visor
		// type), which also feeds --json.
		raw, _ := json.Marshal(infos) //nolint:errcheck
		var rgs []treeRouteGroup
		_ = json.Unmarshal(raw, &rgs) //nolint:errcheck

		if cliout.JSONMode(cmd) {
			internal.Catch(cmd.Flags(), cliout.Print(cmd, rgs))
			return
		}

		if len(rgs) == 0 {
			fmt.Printf("no active route group for app=%s (is the proxy running?)\n", clientName)
			return
		}

		snap := snapshotFromGroups(rgs)
		opts := bitree.Options{}
		if wantColor(treeColor) {
			opts.StyleCell = ansiStyleCell
		}
		fmt.Println(bitree.Render(proxystatus.RouteTree(snap), opts))
	},
}

// wantColor resolves the --color mode; "auto" colorizes only when stdout is a
// terminal.
func wantColor(mode string) bool {
	switch strings.ToLower(mode) {
	case "always", "yes", "true":
		return true
	case "never", "no", "false":
		return false
	default:
		return term.IsTerminal(int(os.Stdout.Fd()))
	}
}

const (
	ansiReset   = "\x1b[0m"
	ansiBold    = "\x1b[1m"
	ansiDim     = "\x1b[2m"
	ansiCyan    = "\x1b[36m"
	ansiGreen   = "\x1b[32m"
	ansiYellow  = "\x1b[33m"
	ansiMagenta = "\x1b[35m"
)

// ansiStyleCell colorizes the route tree for a terminal without changing any
// cell's display width (ANSI escapes are zero-width; bitree lays out from the
// plain text). PKs/labels cyan, transport columns dim, and the left summary
// tinted by state (green active / yellow standby) — the state dot the adapter
// chose selects the hue, so no state word is needed.
func ansiStyleCell(text string, kind bitree.CellKind) string {
	switch kind {
	case bitree.CellRoot:
		return ansiBold + ansiCyan + text + ansiReset
	case bitree.CellLabel:
		// A stream-boundary header (the STREAM layer) reads as a distinct kind of
		// node — bold magenta — vs a plain-cyan hop PK, mirroring the page's badge.
		if strings.HasPrefix(strings.TrimSpace(text), proxystatus.StreamHeaderGlyph) {
			return ansiBold + ansiMagenta + text + ansiReset
		}
		return ansiCyan + text + ansiReset
	case bitree.CellColumn:
		return ansiDim + text + ansiReset
	case bitree.CellLeft:
		if strings.TrimSpace(text) == "" {
			return text
		}
		if strings.Contains(text, proxystatus.GlyphStandby) {
			return ansiYellow + text + ansiReset
		}
		return ansiGreen + text + ansiReset
	default:
		return text
	}
}

// treeRouteGroup mirrors visor.MuxRouteGroupInfo by its JSON contract (the same
// approach mux_info.go uses) so this command needs no visor import. It carries
// the per-leg hop chain + route/transport rtt the bilateral tree needs.
type treeRouteGroup struct {
	Desc struct {
		DstPK string `json:"dst_pk"`
		SrcPK string `json:"src_pk"`
	} `json:"desc"`
	MuxEnabled bool          `json:"mux_enabled"`
	Legs       []treeLegInfo `json:"legs"`
}

type treeLegInfo struct {
	Index          int           `json:"index"`
	TransportID    string        `json:"transport_id"`
	TpType         string        `json:"tp_type"`
	RemotePK       string        `json:"remote_pk"`
	LatencyMS      float64       `json:"latency_ms,omitempty"`
	RouteLatencyMS float64       `json:"route_latency_ms,omitempty"`
	Direct         bool          `json:"direct"`
	SentBytes      uint64        `json:"sent_bytes"`
	RecvBytes      uint64        `json:"recv_bytes"`
	DupBytes       uint64        `json:"dup_bytes"`
	RepairBytes    uint64        `json:"repair_bytes"`
	GoodputUpBps   float64       `json:"goodput_up_bps,omitempty"`
	GoodputDownBps float64       `json:"goodput_down_bps,omitempty"`
	Retransmits    uint64        `json:"retransmits"`
	Alive          bool          `json:"alive"`
	Standby        bool          `json:"standby"`
	Hops           []treeHopInfo `json:"hops,omitempty"`
}

type treeHopInfo struct {
	TpID      string  `json:"tp_id"`
	From      string  `json:"from"`
	To        string  `json:"to"`
	TpType    string  `json:"tp_type"`
	LatencyMS float64 `json:"latency_ms,omitempty"`
}

// toLegs projects one wire route group's legs into the shared proxystatus.Leg
// shape the RouteTree adapter consumes.
func (rg treeRouteGroup) toLegs() []proxystatus.Leg {
	legs := make([]proxystatus.Leg, 0, len(rg.Legs))
	for _, l := range rg.Legs {
		leg := proxystatus.Leg{
			Index:          l.Index,
			TransportID:    l.TransportID,
			TpType:         l.TpType,
			RemotePK:       l.RemotePK,
			LatencyMS:      l.LatencyMS,
			RouteLatencyMS: l.RouteLatencyMS,
			Direct:         l.Direct,
			SentBytes:      l.SentBytes,
			RecvBytes:      l.RecvBytes,
			DupBytes:       l.DupBytes,
			RepairBytes:    l.RepairBytes,
			GoodputUpBps:   l.GoodputUpBps,
			GoodputDownBps: l.GoodputDownBps,
			Retransmits:    l.Retransmits,
			Alive:          l.Alive,
			Standby:        l.Standby,
		}
		for _, h := range l.Hops {
			leg.Hops = append(leg.Hops, proxystatus.Hop{
				TpID:      h.TpID,
				From:      h.From,
				To:        h.To,
				TpType:    h.TpType,
				LatencyMS: h.LatencyMS,
			})
		}
		legs = append(legs, leg)
	}
	return legs
}

// snapshotFromGroups projects EVERY wire route group into the shared
// proxystatus.Snapshot, one Tunnel (stream-level) per group with its own
// packet-level Legs — the exact structure the visor builds for the status page,
// so the CLI and the page render the identical two-level tree.
func snapshotFromGroups(rgs []treeRouteGroup) proxystatus.Snapshot {
	snap := proxystatus.Snapshot{Surface: proxystatus.SurfaceSkysocks, App: clientName}
	for i, rg := range rgs {
		snap.Tunnels = append(snap.Tunnels, proxystatus.Tunnel{
			Index:      i,
			ExitPK:     rg.Desc.DstPK,
			MuxEnabled: rg.MuxEnabled,
			Legs:       rg.toLegs(),
		})
	}
	if len(snap.Tunnels) > 0 {
		snap.MuxEnabled = snap.Tunnels[0].MuxEnabled
		snap.Legs = snap.Tunnels[0].Legs
	}
	return snap
}
