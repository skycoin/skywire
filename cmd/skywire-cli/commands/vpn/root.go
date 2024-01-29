// Package clivpn root.go
package clivpn

import (
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/servicedisc"
)

var (
	version        = buildinfo.Version()
	stateName      = "vpn-client"
	serviceType    = servicedisc.ServiceTypeVPN
	isUnFiltered   bool
	rawData        bool
	utURL          string
	sdURL          string
	cacheFileSD    string
	cacheFileUT    string
	cacheFilesAge  int
	noFilterOnline bool
	path           string
	isPkg          bool
	ver            string
	country        string
	isStats        bool
	pubkey         cipher.PubKey
	pk             string
)

// RootCmd contains commands that interact with the skywire-visor
var RootCmd = &cobra.Command{
	Use:   "vpn",
	Short: "VPN client",
}
