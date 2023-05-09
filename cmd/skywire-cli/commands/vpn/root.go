// Package clivpn root.go
package clivpn

import (
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/servicedisc"
)

var (
	stateName    = "vpn-clent"
	serviceType  = servicedisc.ServiceTypeVPN
	servicePort  = ":3"
	path         string
	isPkg        bool
	isUnFiltered bool
	ver          string
	country      string
	isStats      bool
	pubkey       cipher.PubKey
	pk           string
	count        int
	sdURL        string
	directQuery  bool
	servers      []servicedisc.Service
)

// RootCmd contains commands that interact with the skywire-visor
var RootCmd = &cobra.Command{
	Use:   "vpn",
	Short: "VPN client",
}
