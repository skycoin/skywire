// package commands cmd/skywire-cli/commands/root.go
/*
cli is a command line client for interacting with a skycoin node and offline wallet management
*/
package commands

import (
	"fmt"
	"os"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skycoin/src/cli"
	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/spf13/cobra"

	// register the supported wallets
	_ "github.com/skycoin/skycoin/src/wallet/bip44wallet"
	_ "github.com/skycoin/skycoin/src/wallet/collection"
	_ "github.com/skycoin/skycoin/src/wallet/deterministic"
	_ "github.com/skycoin/skycoin/src/wallet/xpubwallet"
)

func init() {
	logging.SetLevel(logrus.WarnLevel)
	cfg, err := cli.LoadConfig()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	skyCLI, err := cli.NewCLI(cfg)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	RootCmd = skyCLI
	RootCmd.Use = "cli"
	RootCmd.Short = "The skycoin command line interface"
	RootCmd.Long = `
	┌─┐┬┌─┬ ┬┌─┐┌─┐┬┌┐┌   ┌─┐┬  ┬
	└─┐├┴┐└┬┘│  │ │││││───│  │  │
	└─┘┴ ┴ ┴ └─┘└─┘┴┘└┘   └─┘┴─┘┴
The skycoin command line interface`

}

// RootCmd represents the base command for the application
var RootCmd = &cobra.Command{
	Use:   "cli",
	Short: "The skycoin command line interface",
	Long: `
	┌─┐┬┌─┬ ┬┌─┐┌─┐┬┌┐┌   ┌─┐┬  ┬
	└─┐├┴┐└┬┘│  │ │││││───│  │  │
	└─┘┴ ┴ ┴ └─┘└─┘┴┘└┘   └─┘┴─┘┴
The skycoin command line interface`,
}
