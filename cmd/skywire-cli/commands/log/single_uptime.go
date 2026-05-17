// Package clilog cmd/skywire-cli/commands/log/single_uptime.go
//
// `cli log uptime <pk> [path]` — fetch the remote visor's
// /uptime[/path] endpoint over dmsghttp. Like /stats this is a
// typed telemetry store; bare `uptime` returns the index.
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
	RootCmd.AddCommand(singleUptimeCmd)
}

var singleUptimeCmd = &cobra.Command{
	Use:   "uptime <pk> [path]",
	Short: "Fetch /uptime[/path] from a remote visor",
	Long: `Fetch /uptime from one visor over dmsghttp. Optional sub-path is
appended verbatim. Response (JSON or 503 when the visor hasn't
wired its uptime recorder) prints to stdout.

Gated by the remote visor's survey_whitelist.`,
	Args: cobra.RangeArgs(1, 2),
	Run: func(cmd *cobra.Command, args []string) {
		log := logging.MustGetLogger("log-cli")
		pk, err := parseTargetPK(args[0])
		if err != nil {
			log.Fatal(err)
		}
		path := "/uptime"
		if len(args) == 2 {
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
