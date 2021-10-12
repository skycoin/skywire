package config

import (
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/spf13/cobra"
)

var logger = logging.MustGetLogger("skywire-cli")

// RootCmd contains commands that interact with the config of local skywire-visor
var RootCmd = &cobra.Command{
	Use:   "config",
	Short: "Contains sub-commands that interact with the config of local skywire-visor",
}
