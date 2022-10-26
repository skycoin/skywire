// Package clivisor cmd/skywire-cli/commands/visor/root.go
package clivisor

import (
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/logging"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
)

var logger = logging.MustGetLogger("skywire-cli")
var removeAll bool

func init() {
	RootCmd.PersistentFlags().StringVar(&clirpc.Addr, "rpc", "localhost:3435", "RPC server address")
}

// RootCmd contains commands that interact with the skywire-visor
var RootCmd = &cobra.Command{
	Use:   "visor",
	Short: "Query the Skywire Visor",
}
