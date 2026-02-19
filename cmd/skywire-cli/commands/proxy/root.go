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
	country         string
	version         string
	minVersion      string
	maxVersion      string
	showOffline     bool
	addr            string
	startingTimeout int
	httpAddr        string
	jsonOutput      bool
	appPort         uint16
	useInternal     bool
	useExternal     bool
	// test command vars
	testURL         string
	testTimeout     int
	testBatchSize   int
	testDelay       int
	testOnlyWithTp  bool
	testVerbose     bool
	testConnectOnly bool
	testVersion     string
	// existing transport flag
	existingTpOnly   bool
	forceLocalRoutes bool
	// multi-hop testing
	viaVisor string
)

// RootCmd contains commands that interact with the skywire-visor
var RootCmd = &cobra.Command{
	Use:   "proxy",
	Short: "Skysocks client",
}
