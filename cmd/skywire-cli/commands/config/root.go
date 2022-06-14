package config

import (
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/cmd/skywire-cli/commands/config/update"
)

var logger = logging.MustGetLogger("skywire-cli")

func init() {
	RootCmd.AddCommand(update.RootCmd)
}

// RootCmd contains commands that interact with the config of local skywire-visor
var RootCmd = &cobra.Command{
	Use:   "config",
	Short: "Generate or update a skywire config",
}
