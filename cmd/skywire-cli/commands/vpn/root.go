// Package clivpn root.go
package clivpn

import (
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
)

var (
	stateName        = "vpn-client"
	serviceType      = servicedisc.ServiceTypeVPN
	rawData          bool
	sdURL            string
	utURL            string
	cacheFileSD      string
	cacheFileUT      string
	cacheFilesAge    int
	noFilterOnline   bool
	path             string
	isPkg            bool
	isStats          bool
	pubkey           cipher.PubKey
	pk               string
	startingTimeout  int
	jsonOutput       bool
	country          string
	version          string
	geoipURL         string
	useInternal      bool
	useExternal      bool
	existingTpOnly   bool
	forceLocalRoutes bool
)

// RootCmd contains commands that interact with the skywire-visor
var RootCmd = &cobra.Command{
	Use:   "vpn",
	Short: "VPN client",
}
