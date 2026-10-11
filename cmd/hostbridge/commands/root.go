// Package commands cmd/hostbridge/commands/root.go c3-vis-wasm
package commands

import (
	"log"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/hostbridge"
)

var (
	addr   string
	origin string
	token  string
)

func init() {
	RootCmd.Flags().StringVar(&addr, "addr", hostbridge.DefaultAddr, "address the tab connects to")
	RootCmd.Flags().StringVar(&origin, "origin", hostbridge.DefaultOrigin, "comma separated pages allowed to connect, * for any")
	RootCmd.Flags().StringVar(&token, "token", "", "secret the tab must present, random when empty")
}

// RootCmd is `skywire hostbridge`.
var RootCmd = &cobra.Command{
	Use:   "hostbridge",
	Short: "Lend a browser visor this host's sockets",
	Long: `Lend a browser visor this host's sockets.

A visor running in a browser tab cannot listen on a port or open a raw
socket. The bridge does that for it. The tab connects to the bridge over a
WebSocket on loopback, then asks it to dial, listen on TCP or bind UDP.

Only pages in --origin may connect, and only with the token in the printed
URL. The bridge holds no keys.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		cfg := hostbridge.Config{Token: token, Origins: hostbridge.SplitOrigins(origin), Logf: log.Printf}
		return hostbridge.Run(addr, cfg, func(url string) {
			cmd.PrintErrln("connect the visor to " + url)
		})
	},
}
