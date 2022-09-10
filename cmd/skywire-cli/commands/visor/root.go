package clivisor

import (
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/logging"
)

var logger = logging.MustGetLogger("skywire-cli")

// RootCmd contains commands that interact with the skywire-visor
var RootCmd = &cobra.Command{
	Use:   "visor",
	Short: "Query the Skywire Visor",
}
