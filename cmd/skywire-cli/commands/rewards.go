//go:build !js

package commands

import (
	clirewards "github.com/skycoin/skywire/cmd/skywire-cli/commands/rewards"
)

// The reward system operator tools (calculation, distribution, the reward UI
// server and the telegram bot) do not run in a browser, so js/wasm omits them.
func init() {
	clirewards.RootCmd.GroupID = groupRewards
	RootCmd.AddCommand(clirewards.RootCmd)
}
