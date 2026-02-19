// Package clidmsg cmd/skywire-cli/commands/dmsg/root.go
package clidmsg

import (
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
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
	RootCmd.AddCommand(
		curlCmd,
		ptyCmd,
	)
}
