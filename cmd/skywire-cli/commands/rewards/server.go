// Package clirewards cmd/skywire-cli/commands/rewards/server.go c4-vis-cli
package clirewards

import (
	server "github.com/skycoin/skywire/cmd/skywire-cli/commands/rewards/server"
)

func init() {
	RootCmd.AddCommand(
		server.ServerCmd,
		server.LoginChainCmd,
	)
}
