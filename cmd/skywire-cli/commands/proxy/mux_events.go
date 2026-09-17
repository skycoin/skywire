// Package skysocksc cmd/skywire-cli/commands/proxy/mux_events.go c4-vis-cli
package skysocksc

import (
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
	muxEventsApp     string
	muxEventsVerbose bool
)

func init() {
	muxEventsCmd.Flags().StringVarP(&muxEventsApp, "name", "n", "skysocks-client", "app name to query (e.g. skysocks-client, vpn-client)")
	muxEventsCmd.Flags().BoolVarP(&muxEventsVerbose, "verbose", "v", false, "show full PKs / transport IDs (default: short hex prefixes)")
	addMuxSub(muxEventsCmd, "mux-events")
}

var muxEventsCmd = &cobra.Command{
	Use:   "events",
	Short: "Show what happened to an active proxy session's route groups, and why",
	Long: `Show the recorded changes to the route groups behind a running proxy session,
newest last, with the reason the code gave for each.

This answers "a leg / a tunnel that was here is gone — who took it": leg
adds and removals, parks and promotes, primary re-homes, reorder wedges,
the dial decision behind each group, and the TUNNEL switches the standby
pool makes (tunnel_promoted / tunnel_parked / tunnel_retired).

It is the per-group half of the router's event ring. The whole-visor ring,
including groups belonging to other apps, is 'visor state --select diag'
under .diag.mux_events.

Examples:
  skywire cli proxy mux events                   # default app
  skywire cli proxy mux events -n vpn-client     # a different app
  skywire cli proxy mux events --json            # machine-readable`,
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck,gosec

		infos, err := rpcClient.RouteGroupMuxInfo(muxEventsApp)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("RouteGroupMuxInfo: %w", err))
		}
		renderMuxEvents(cmd, infos)
	},
}

// muxEventGroup is the CLI-side mirror of the slice of visor.MuxRouteGroupInfo
// this command reads — only the identity of each group and its events. Like
// every other mirror here, the json tags are the stable contract; unlike
// muxRouteGroupInfo (which drops `events`, see its doc), this one keeps them,
// which is the whole point of the command.
type muxEventGroup struct {
	Desc struct {
		DstPK   string `json:"dst_pk"`
		SrcPK   string `json:"src_pk"`
		DstPort int    `json:"dst_port"`
		SrcPort int    `json:"src_port"`
	} `json:"desc"`
	TunnelRole string         `json:"tunnel_role,omitempty"`
	Events     []muxEventInfo `json:"events,omitempty"`
}

// muxEventInfo mirrors router.MuxEvent.
type muxEventInfo struct {
	At       time.Time `json:"at"`
	Event    string    `json:"event"`
	App      string    `json:"app,omitempty"`
	By       string    `json:"by,omitempty"`
	LegIndex int       `json:"leg_index"`
	Legs     int       `json:"legs"`
	TpID     string    `json:"tp_id,omitempty"`
	TpType   string    `json:"tp_type,omitempty"`
	RemotePK string    `json:"remote_pk,omitempty"`
	Reason   string    `json:"reason,omitempty"`
}

// renderMuxEvents prints one row per event across every route group the app
// holds, oldest first, so a failover reads as the sequence it was.
func renderMuxEvents(cmd *cobra.Command, infos any) {
	raw, _ := json.Marshal(infos) //nolint:errcheck
	var rgs []muxEventGroup
	_ = json.Unmarshal(raw, &rgs) //nolint:errcheck

	if cliout.JSONMode(cmd) {
		internal.Catch(cmd.Flags(), cliout.Print(cmd, rgs))
		return
	}

	type row struct {
		rg   int
		role string
		ev   muxEventInfo
	}
	rows := make([]row, 0, 32)
	for _, rg := range rgs {
		for _, ev := range rg.Events {
			rows = append(rows, row{rg: rg.Desc.SrcPort, role: rg.TunnelRole, ev: ev})
		}
	}
	if len(rows) == 0 {
		fmt.Printf("no recorded route-group events for app=%s (%d active group(s))\n", muxEventsApp, len(rgs))
		return
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].ev.At.Before(rows[j].ev.At) })

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "time\trg\trole\tevent\tby\tleg\tfirst hop\treason") //nolint:errcheck,gosec
	for _, r := range rows {
		hop := r.ev.RemotePK
		tp := r.ev.TpID
		if !muxEventsVerbose {
			hop = shortHex(hop)
			tp = shortHex(tp)
		}
		first := "-"
		if hop != "" {
			first = r.ev.TpType + ">" + hop + "@" + tp
		}
		role := r.role
		if role == "" {
			role = "-"
		}
		fmt.Fprintf(w, "%s\t%d\t%s\t%s\t%s\t%d/%d\t%s\t%s\n", //nolint:errcheck,gosec
			r.ev.At.Format("15:04:05"), r.rg, role, r.ev.Event, r.ev.By,
			r.ev.LegIndex, r.ev.Legs, first, r.ev.Reason)
	}
	_ = w.Flush() //nolint:errcheck
}

// shortHex is the 8-char prefix the other mux views print for a PK or
// transport id. Empty in, empty out; never applied to a value the operator
// asked for in full (--verbose).
func shortHex(s string) string {
	if len(s) <= 8 {
		return s
	}
	return s[:8]
}
