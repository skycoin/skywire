package clivpn

import (
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/logging"
)

var (
	logger  = logging.MustGetLogger("skywire-cli")
	path    string
	pkg     bool
	ver     string
	country string
	systray bool
	stats   bool
)

// RootCmd contains commands that interact with the skywire-visor
var RootCmd = &cobra.Command{
	Use:   "vpn",
	Short: "controls for VPN client",
}
