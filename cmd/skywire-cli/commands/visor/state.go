// Package clivisor cmd/skywire-cli/commands/visor/state.go
package clivisor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/cliout"
	"github.com/skycoin/skywire/pkg/visor"
)

const stateHumanMessage = "visor runtime state snapshot\n" +
	"  use --json for the full structure, --shape for its schema,\n" +
	"  --jq '<filter>' to project a field (e.g. --jq '.health'),\n" +
	"  --select <keys> for a cheap server-built subtree, or\n" +
	"  --watch <interval> to stream snapshots as NDJSON."

var (
	stateSelect string
	stateWatch  time.Duration
)

func init() {
	stateCmd.Flags().StringVar(&stateSelect, "select", "", "server-side projection: build only these subtree(s), comma-separated ("+strings.Join(visor.StateSelectKeys, ",")+")")
	stateCmd.Flags().DurationVar(&stateWatch, "watch", 0, "stream snapshots as NDJSON every <interval> (e.g. 1s) until Ctrl-C")
	RootCmd.AddCommand(stateCmd)
}

var stateCmd = &cobra.Command{
	Use:   "state",
	Short: "Curated snapshot of the visor's live internal runtime state",
	Long: `Curated, secrets-free snapshot of the visor's live internal runtime state.

Aggregates the same RPC-safe views the individual subcommands return —
summary, health, service health, routing stats + route-group count, active
routing policy, apps, transports, persistent transports, and which optional
subsystems are wired — into ONE response, so you can project exactly the field
you want with the global --jq / --shape flags instead of stitching together a
dozen subcommands. The visor's secret key is never included.

--select builds ONLY the requested subtree(s) SERVER-side, so a cheap
'--select mux' skips the ~307 KB transports build (full snapshot ~900 KB, mux
~75 KB). Keys (comma-separated): summary, health, routing, mux, apps,
transports, modules, cxo, proxy. The projected keys match the full snapshot's
JSON field names, so --jq expressions transfer unchanged. 'proxy' is opt-in
(the visor-side skysocks proxystatus: per-leg mux + range-split when pushed).

--watch <interval> opens the RPC once and emits one snapshot per tick as NDJSON
(newline-delimited JSON, no banners/ANSI) to stdout until Ctrl-C — a clean pipe
for a chart or Monitor. It composes with --select (cheap projected ticks),
--jq (each tick filtered to one compact line) and --via (a remote visor).

Examples:
  skywire cli visor state --shape                 # print the output schema skeleton
  skywire cli visor state --json                  # full snapshot as JSON
  skywire cli visor state --jq '.health'          # just the health section
  skywire cli visor state --select mux            # server builds ONLY mux_route_groups
  skywire cli visor state --select health,routing # several subtrees
  skywire cli visor state --watch 1s --select mux --jq '.mux_route_groups'
  skywire cli visor state --via dmsg://<pk> --jq '.routing_policy'  # a remote visor`,
	Run: func(cmd *cobra.Command, _ []string) {
		// --shape only needs the schema, not live data: print the skeleton of a
		// zero snapshot without an RPC round-trip, so it works offline / against
		// a visor that predates this call.
		if shape, err := cmd.Flags().GetBool(cliout.ShapeFlag); err == nil && shape {
			internal.PrintOutput(cmd.Flags(), &visor.StateSnapshot{}, stateHumanMessage)
			return
		}

		fields := parseSelect(stateSelect)

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}

		// fetch pulls one snapshot: the projected RPC when --select is set
		// (server builds only those subtrees), otherwise the full snapshot so
		// behavior against an older visor is unchanged.
		fetch := func() (*visor.StateSnapshot, error) {
			if len(fields) > 0 {
				return rpcClient.StateSnapshotProjected(fields)
			}
			return rpcClient.StateSnapshot()
		}

		if stateWatch > 0 {
			watchState(cmd, fetch)
			return
		}

		snap, err := fetch()
		if err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}
		internal.PrintOutput(cmd.Flags(), snap, stateHumanMessage)
	},
}

// parseSelect splits a comma-separated --select value into trimmed, non-empty
// subtree keys. An empty value yields nil (full snapshot).
func parseSelect(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// watchState streams one snapshot per tick as NDJSON to stdout until an
// interrupt. It emits immediately, then every stateWatch interval. --jq filters
// each tick to one compact line; otherwise the whole snapshot is one compact
// line. A per-tick fetch error goes to stderr and the stream continues, so a
// transient RPC hiccup does not end the watch.
func watchState(cmd *cobra.Command, fetch func() (*visor.StateSnapshot, error)) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	jqFilter := cliout.JQFilter(cmd)
	emit := func() {
		snap, err := fetch()
		if err != nil {
			fmt.Fprintln(os.Stderr, "watch:", err)
			return
		}
		if err := writeNDJSON(os.Stdout, snap, jqFilter); err != nil {
			fmt.Fprintln(os.Stderr, "watch:", err)
		}
	}

	streamTicks(ctx, stateWatch, emit)
}

// streamTicks emits immediately, then calls emit every interval until ctx is
// canceled (Ctrl-C). Extracted so the tick loop is unit-testable without a live
// RPC or a real signal.
func streamTicks(ctx context.Context, interval time.Duration, emit func()) {
	emit()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			emit()
		}
	}
}

// writeNDJSON marshals snap as one NDJSON line (or, when jqFilter is set, one
// compact line per jq result). It is deliberately banner/ANSI-free so the output
// pipes cleanly into a chart or Monitor.
func writeNDJSON(w io.Writer, snap *visor.StateSnapshot, jqFilter string) error {
	b, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	if jqFilter == "" {
		_, err = fmt.Fprintf(w, "%s\n", b)
		return err
	}
	lines, err := cliout.ApplyJQCompact(b, jqFilter)
	if err != nil {
		return err
	}
	for _, ln := range lines {
		if _, err := fmt.Fprintf(w, "%s\n", ln); err != nil {
			return err
		}
	}
	return nil
}
