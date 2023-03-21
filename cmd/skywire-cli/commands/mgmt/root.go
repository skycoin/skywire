// Package mgmt cmd/skywire-cli/commands/mgmt/root.go
package mgmt

import (
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/logging"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
)

var logger = logging.MustGetLogger("skywire-cli")

func init() {
	RootCmd.PersistentFlags().StringVar(&clirpc.Addr, "rpc", "localhost:3435", "RPC server address")
}

// RootCmd contains commands that interact with the Manager
var RootCmd = &cobra.Command{
	Use:   "mgmt",
	Short: "Skywire visor manager",
}
