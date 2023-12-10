// Package skysocksc root.go
package skysocksc

import (
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/servicedisc"
)

var (
	binaryName   = "skysocks-client"
	stateName    = "skysocks-client"
	serviceType  = servicedisc.ServiceTypeProxy
	servicePort  = ":44"
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
	allClients   bool
	clientName   string
	addr         string
)

// RootCmd contains commands that interact with the skywire-visor
var RootCmd = &cobra.Command{
	Use:   "proxy",
	Short: "Skysocks client",
}
