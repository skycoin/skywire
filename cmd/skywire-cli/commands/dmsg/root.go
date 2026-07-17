// Package clidmsg cmd/skywire-cli/commands/dmsg/root.go c4-vis-cli
package clidmsg

import (
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/cipher"
)

var (
	rpcAddr string
	sk      cipher.SecKey
	logLvl  string
)

// RootCmd is the command that contains sub-commands which use dmsg.
var RootCmd = &cobra.Command{
	Use:   "dmsg",
	Short: "Dmsg utilities",
	Long:  "Commands that use DMSG for communication",
}

func init() {
	// PtyCmd's subcommands are re-parented onto `cli pty` (see the pty
	// package); they are no longer registered under `cli dmsg`.
	RootCmd.AddCommand(
		curlCmd,
		chatCmd,
		scpCmd,
		catCmd,
		iperfCmd,
	)
}
