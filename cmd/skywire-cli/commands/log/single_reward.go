// Package clilog cmd/skywire-cli/commands/log/single_reward.go
//
// `cli log reward <pk>` — fetch the remote visor's /reward.txt
// file over dmsghttp. Only exists when the operator has set a
// reward address on the visor.
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
	RootCmd.AddCommand(singleRewardCmd)
}

var singleRewardCmd = &cobra.Command{
	Use:   "reward <pk>",
	Short: "Fetch /reward.txt from a remote visor",
	Long: `Fetch /reward.txt from one visor over dmsghttp. The file only
exists when the operator has set a reward address; absent files
return 404.

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

		hc, cleanup, err := dmsgHTTPClient(ctx, 30*time.Second)
		if err != nil {
			log.Fatal(err)
		}
		defer cleanup()

		if err := fetchSurveyEndpoint(ctx, hc, pk, "/reward.txt", os.Stdout); err != nil {
			log.Fatal(err)
		}
	},
}
