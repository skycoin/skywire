// Package net cmd/skywire-cli/commands/net/root.go
package net

import (
	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
)

func init() {
	RootCmd.PersistentFlags().StringVar(&clirpc.Addr, "rpc", "localhost:3435", "RPC server address")
}

// RootCmd contains commands that interact with the skywire network
var RootCmd = &cobra.Command{
	Use:   "net",
	Short: "Publish and connect to skywire network",
}
