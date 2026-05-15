// Package clidmsg cmd/skywire-cli/commands/dmsg/sub.go
//
// Registers the standalone UDP→dmsg bridge as the `sub` subcommand
// of `skywire dmsg`, so a single skywire binary can launch it
// without requiring a separately-built top-level binary.
package clidmsg

import (
	sub "github.com/skycoin/skywire/cmd/sub/commands"
)

func init() {
	// Hoist the standalone's RootCmd directly so a single cobra
	// definition serves both the top-level binary (cmd/sub) and the
	// integrated subcommand (`skywire dmsg sub`).
	RootCmd.AddCommand(sub.RootCmd)
}
