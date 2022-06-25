package visor

import (
	"net"
	"time"

	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/spf13/cobra"

//	"github.com/skycoin/skywire/cmd/skywire-cli/commands/hvui/hvui"
	"github.com/skycoin/skywire/cmd/skywire-cli/commands/hvui/hvvpn"
//	"github.com/skycoin/skywire/cmd/skywire-cli/commands/hvui/hvdmsg"
//	"github.com/skycoin/skywire/cmd/skywire-cli/commands/hvui/hvskychat"
	"github.com/skycoin/skywire/pkg/visor"
)

var logger = logging.MustGetLogger("skywire-cli")

func init() {
//	RootCmd.AddCommand(hvui.RootCmd)
	RootCmd.AddCommand(hvvpn.RootCmd)
//	RootCmd.AddCommand(hvdmsg.RootCmd)
//	RootCmd.AddCommand(hvskychat.RootCmd)
}

// RootCmd contains commands that interact with the skywire-visor
var RootCmd = &cobra.Command{
	Use:   "hv",
	Short: "open HVUI in browser",
}
