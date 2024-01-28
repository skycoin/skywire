// Package skysocksc root.go
package skysocksc

import (
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/servicedisc"
)

var (
	version        = buildinfo.Version()
	binaryName     = "skysocks-client"
	stateName      = "skysocks-client"
	serviceType    = servicedisc.ServiceTypeProxy
	isUnFiltered   bool
	rawData        bool
	utURL          string
	sdURL          string
	cacheFileSD    string
	cacheFileUT    string
	cacheFilesAge  int
	ver            string
	country        string
	isStats        bool
	pubkey         cipher.PubKey
	pk             string
	allClients     bool
	noFilterOnline bool
	clientName     string
	addr           string
)

// RootCmd contains commands that interact with the skywire-visor
var RootCmd = &cobra.Command{
	Use:   "proxy",
	Short: "Skysocks client",
}
