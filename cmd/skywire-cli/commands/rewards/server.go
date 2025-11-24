// Package clirewards cmd/skywire-cli/commands/rewards/server.go
package clirewards

import (
	server "github.com/skycoin/skywire/cmd/skywire-cli/commands/rewards/server"
)

func init() {
	RootCmd.AddCommand(
		server.ServerCmd,
	)
}
