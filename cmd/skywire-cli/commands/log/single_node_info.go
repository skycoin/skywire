// Package clilog cmd/skywire-cli/commands/log/single_node_info.go
//
// `cli log info <pk>` — fetch a single visor's /node-info JSON over
// dmsghttp. Distinct from the bulk-collection mode at the `cli log`
// root (no subcommand), which iterates the entire uptime tracker
// and writes per-visor folders. This one-shot mode is for ad-hoc
// inspection: target one visor, dump the survey to stdout.
package clilog

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/logging"
)

func init() {
	RootCmd.AddCommand(singleInfoCmd)
}

var singleInfoCmd = &cobra.Command{
	Use:   "info <pk>",
	Short: "Fetch a single visor's /node-info survey",
	Long: `Fetch /node-info from one visor over dmsghttp. JSON written to stdout.

Gated by the remote visor's survey_whitelist — the CLI's local PK
(derived from --sk / DMSGCURL_SK / survey_client_sk / ephemeral)
must be allowlisted. Single-target counterpart of bulk ` + "`cli log`" + `
collection.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		log := logging.MustGetLogger("log-cli")
		pk, err := parseTargetPK(args[0])
		if err != nil {
			log.Fatal(err)
		}
		ctx, cancel := cmdutil.SignalContext(context.Background(), log)
		defer cancel()

		hc, cleanup, err := dmsgHTTPClient(ctx, 30*time.Second)
		if err != nil {
			log.Fatal(err)
		}
		defer cleanup()

		var survey map[string]any
		if err := fetchSurveyJSON(ctx, hc, pk, "/node-info", &survey); err != nil {
			log.Fatal(err)
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(survey); err != nil {
			log.Fatal(err)
		}
	},
}
