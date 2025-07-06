// Package skysocksc root.go
package skysocksc

import (
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
)

var (
	binaryName      = "skysocks-client"
	stateName       = "skysocks-client"
	serviceType     = servicedisc.ServiceTypeProxy
	rawData         bool
	sdURL           string
	utURL           string
	cacheFileSD     string
	cacheFileUT     string
	cacheFilesAge   int
	isStats         bool
	pubkey          cipher.PubKey
	pk              string
	allClients      bool
	noFilterOnline  bool
	clientName      string
	addr            string
	startingTimeout int
	httpAddr        string
	jsonOutput      bool
)

// RootCmd contains commands that interact with the skywire-visor
var RootCmd = &cobra.Command{
	Use:   "proxy",
	Short: "Skysocks client",
}
