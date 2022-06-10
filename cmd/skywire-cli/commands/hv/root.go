package hv

import (
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/cmd/skywire-cli/commands/hv/hvdmsg"
	"github.com/skycoin/skywire/cmd/skywire-cli/commands/hv/hvskychat"
	"github.com/skycoin/skywire/cmd/skywire-cli/commands/hv/hvui"
	"github.com/skycoin/skywire/cmd/skywire-cli/commands/hv/hvvpn"
)


func init() {
	RootCmd.AddCommand(hvui.RootCmd)
	RootCmd.AddCommand(hvvpn.RootCmd)
	RootCmd.AddCommand(hvdmsg.RootCmd)
	RootCmd.AddCommand(hvskychat.RootCmd)
}

// RootCmd contains commands that interact with the skywire-visor
var RootCmd = &cobra.Command{
	Use:   "hv",
	Short: "open HVUI in browser",
}
