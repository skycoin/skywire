package clivisor

import (
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/spf13/cobra"
)

var logger = logging.MustGetLogger("skywire-cli")

// RootCmd contains commands that interact with the skywire-visor
var RootCmd = &cobra.Command{
	Use:   "visor",
	Short: "Query the Skywire Visor",
}
