// Package clilog cmd/skywire-cli/commands/log/single_stats.go
//
// `cli log stats <pk> [path]` — fetch the remote visor's
// /stats[/path] endpoint over dmsghttp. Stats is a typed
// telemetry store; consult the visor's logserver for the
// available paths (the bare `stats` call returns the index).
package clilog

import (
	"context"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/logging"
)

func init() {
	RootCmd.AddCommand(singleStatsCmd)
}

var singleStatsCmd = &cobra.Command{
	Use:   "stats <pk> [path]",
	Short: "Fetch /stats[/path] from a remote visor",
	Long: `Fetch /stats from one visor over dmsghttp. Optional sub-path is
appended verbatim — e.g. ` + "`cli log stats <pk> uptime/breakdown`" + `
fetches /stats/uptime/breakdown. Response (JSON or 503 when the
visor hasn't wired its stats reader) prints to stdout.

Gated by the remote visor's survey_whitelist.`,
	Args: cobra.RangeArgs(1, 2),
	Run: func(cmd *cobra.Command, args []string) {
		log := logging.MustGetLogger("log-cli")
		pk, err := parseTargetPK(args[0])
		if err != nil {
			log.Fatal(err)
		}
		path := "/stats"
		if len(args) == 2 {
			// args[1] is appended verbatim; operators can include
			// query strings (?since=...) or sub-paths (/uptime/...).
			if args[1][0] != '/' {
				path += "/"
			}
			path += args[1]
		}

		ctx, cancel := cmdutil.SignalContext(context.Background(), log)
		defer cancel()

		hc, cleanup, err := dmsgHTTPClient(ctx, 30*time.Second)
		if err != nil {
			log.Fatal(err)
		}
		defer cleanup()

		if err := fetchSurveyEndpoint(ctx, hc, pk, path, os.Stdout); err != nil {
			log.Fatal(err)
		}
	},
}
