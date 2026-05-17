// Package clilog cmd/skywire-cli/commands/log/single_visor_log.go
//
// `cli log file <pk>` — stream a single visor's /visor.log over
// dmsghttp. Requires the remote visor to have been started with
// -s/--save-log; without that flag, the endpoint returns 404 and
// this command surfaces a clear hint.
package clilog

import (
	"context"
	"os"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/logging"
)

func init() {
	RootCmd.AddCommand(singleFileCmd)
}

var singleFileCmd = &cobra.Command{
	Use:   "file <pk>",
	Short: "Stream a single visor's /visor.log to stdout",
	Long: `Stream /visor.log from one visor over dmsghttp. Visor must have been
started with -s/--save-log; without that flag the file doesn't exist
and the endpoint returns 404.

Gated by the remote visor's survey_whitelist.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		log := logging.MustGetLogger("log-cli")
		pk, err := parseTargetPK(args[0])
		if err != nil {
			log.Fatal(err)
		}
		ctx, cancel := cmdutil.SignalContext(context.Background(), log)
		defer cancel()

		// No per-request timeout — log files can be large and slow.
		// ctx cancellation (^C) still terminates the stream.
		hc, cleanup, err := dmsgHTTPClient(ctx, 0)
		if err != nil {
			log.Fatal(err)
		}
		defer cleanup()

		if err := fetchSurveyEndpoint(ctx, hc, pk, "/visor.log", os.Stdout); err != nil {
			log.Fatal(err)
		}
	},
}
