// Package clidmsg cmd/skywire-cli/commands/dmsg/smb.go
//
// Registers the standalone (dmsg-only) SMTP→dmsg bridge as the `smb`
// subcommand of `skywire dmsg`, so a single skywire binary can launch
// it without requiring a separately-built top-level binary. The
// visor-side in-process bridge has its own control surface at
// `skywire cli mail`; this subcommand is for hosts that don't run a
// visor.
package clidmsg

import (
	smb "github.com/skycoin/skywire/cmd/smb/commands"
)

func init() {
	// Hoist the standalone's RootCmd directly so a single cobra
	// definition serves both the top-level binary (cmd/smb) and the
	// integrated subcommand (`skywire dmsg smb`). Avoids drift
	// between the two surfaces.
	RootCmd.AddCommand(smb.RootCmd)
}
