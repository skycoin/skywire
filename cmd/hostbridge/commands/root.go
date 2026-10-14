// Package commands cmd/hostbridge/commands/root.go c3-vis-wasm
package commands

import (
	"log"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/hostbridge"
)

var (
	addr      string
	origin    string
	token     string
	tokenFile string
	unpair    bool
)

func init() {
	RootCmd.Flags().StringVar(&addr, "addr", hostbridge.DefaultAddr, "address the tab connects to")
	RootCmd.Flags().StringVar(&origin, "origin", hostbridge.DefaultOrigin, "comma separated pages allowed to connect, * for any")
	RootCmd.Flags().StringVar(&token, "token", "", "secret the tab must present, read from --token-file when empty")
	RootCmd.Flags().StringVar(&tokenFile, "token-file", "", "file that keeps the token across restarts, default <user config dir>/skywire/hostbridge-token, none for a new token each run")
	RootCmd.Flags().BoolVar(&unpair, "unpair", false, "forget the paired visor, so the next one to connect with the token pairs")
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
URL. The token is kept in --token-file, so the URL stays the same when the
bridge restarts. The first visor to connect pairs with the bridge, which
then serves only that visor until it is run with --unpair. The bridge
holds no keys of its own.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		t, err := hostbridge.ResolveToken(token, tokenFile)
		if err != nil {
			return err
		}
		pairFile := ""
		if token == "" {
			pairFile = hostbridge.PairFile(tokenFile)
		}
		if unpair {
			if err := hostbridge.Unpair(pairFile); err != nil {
				return err
			}
		}
		cfg := hostbridge.Config{Token: t, Origins: hostbridge.SplitOrigins(origin), PairFile: pairFile, Logf: log.Printf}
		return hostbridge.Run(addr, cfg, func(url string) {
			cmd.PrintErrln("connect the visor to " + url)
		})
	},
}
