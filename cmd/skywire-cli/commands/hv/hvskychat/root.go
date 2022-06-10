package hvskychat

import (
	"fmt"

	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/spf13/cobra"
	"github.com/toqueteos/webbrowser"
)

var logger = logging.MustGetLogger("skywire-cli")

// RootCmd contains commands that interact with the skywire-visor
var RootCmd = &cobra.Command{
	Use:   "skychat",
	Short: "skychat UI",
	Run: func(_ *cobra.Command, _ []string) {
		var url string
		//TODO: get the actual port from config instead of using default value here
		if err := webbrowser.Open(fmt.Sprintf("http://127.0.0.1:8001/")); err != nil {
			logger.Fatal("Failed to open hypervisor UI in browser:", err)
		}
	},
}
