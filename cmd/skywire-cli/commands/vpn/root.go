// Package clivpn root.go
package clivpn

import (
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
)

var (
	path         string
	isPkg        bool
	isUnFiltered bool
	ver          string
	country      string
	isStats      bool
	pubkey       cipher.PubKey
	pk           string
	count        int
)

func init() {
	RootCmd.PersistentFlags().StringVar(&clirpc.Addr, "rpc", "localhost:3435", "RPC server address")
}

// RootCmd contains commands that interact with the skywire-visor
var RootCmd = &cobra.Command{
	Use:   "vpn",
	Short: "VPN client",
}
