// Package clirewardstgbot cmd/skywire-cli/commands/rewards/tgbot/tgbot.go c4-vis-cli
package clirewardstgbot

import (
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/cmdutil"
	rewardsbot "github.com/skycoin/skywire/pkg/deployment/rewards/bot"
)

var filePath string

func init() {

	RootCmd.Flags().StringVarP(&filePath, "watch", "w", "../reward/rewards/transactions0.txt", "File to watch - file where reward transaction IDs are recorded")

}

// RootCmd contains the reward telegram bot command
var RootCmd = &cobra.Command{
	Use:   "bot",
	Short: "reward notification telegram bot",
	Long: `Reward notification telegram bot.

Watches the reward transaction file (--watch) and posts the per-day stats
for each newly-broadcast reward transaction to a telegram chat.

Requires two environment variables:
  TG_BOT_TOKEN   telegram bot API token
  TG_CHAT_ID     numeric telegram chat ID to post into`,
	Run: func(cmd *cobra.Command, _ []string) {
		token, chatID, err := rewardsbot.FromEnv()
		if err != nil {
			cmdutil.Fatal(err)
		}
		if err := rewardsbot.Run(cmd.Context(), token, chatID, filePath); err != nil {
			cmdutil.Fatal(err)
		}
	},
}
