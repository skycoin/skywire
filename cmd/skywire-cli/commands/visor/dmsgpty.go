package visor

import (
dmsgcli	"github.com/skycoin/dmsg/cmd/dmsgpty-cli/commands"
dmsgui	"github.com/skycoin/dmsg/cmd/dmsgpty-ui/commands"
)

func init() {
	RootCmd.AddCommand(
		dmsgui.RootCmd,
		dmsgcli.RootCmd,
	)
}
