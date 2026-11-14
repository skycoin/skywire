//go:build !tinygo

// Package clirewards cmd/skywire-cli/commands/rewards/server.go c4-vis-cli
package clirewards

import (
	rewardsserver "github.com/skycoin/skywire/pkg/deployment/rewards/server"
)

func init() {
	RootCmd.AddCommand(
		rewardsserver.ServerCmd,
		rewardsserver.LoginChainCmd,
	)
}
