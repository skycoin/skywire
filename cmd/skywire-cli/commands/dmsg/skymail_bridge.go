// Package clidmsg cmd/skywire-cli/commands/dmsg/skymail_bridge.go
//
// Registers the standalone (dmsg-only) skymail-bridge as a subcommand
// of `skywire dmsg`, so a single skywire binary can launch it without
// requiring a separately-built top-level binary. The visor-side
// in-process bridge has its own control surface at `skywire cli mail`;
// this subcommand is for hosts that don't run a visor.
package clidmsg

import (
	smb "github.com/skycoin/skywire/cmd/skymail-bridge/commands"
)

func init() {
	// Hoist the standalone's RootCmd directly so a single cobra
	// definition serves both the top-level binary
	// (cmd/skymail-bridge) and the integrated subcommand
	// (skywire dmsg skymail-bridge). Avoids drift between the two
	// surfaces.
	RootCmd.AddCommand(smb.RootCmd)
}
