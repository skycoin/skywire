// Package clitp cmd/skywire-cli/commands/tp/tp-route-addr.go c4-vis-cli
package clitp

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cliout"
	"github.com/skycoin/skywire/pkg/cliout/clitp"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

var (
	routeAddrType   string
	routeAddrSuffix string
)

func init() {
	routeAddrCmd.Flags().StringVarP(&routeAddrType, "type", "t", "sudph", "transport type for the hops (dmsg, stcp, stcpr, sudph)")
	routeAddrCmd.Flags().StringVarP(&routeAddrSuffix, "suffix", "s", ".skynet", "address suffix")
	tpCmd.AddCommand(routeAddrCmd)
}

var routeAddrCmd = &cobra.Command{
	Use:   "route-addr <src-pk> <hop-pk>... <dest-pk>",
	Short: "Build a source-routed resolver address (<tpid>...<dest>.skynet) for a PK path",
	Long: `Build the resolving-proxy address that source-routes through an explicit PK
path, by computing the deterministic transport ID of each hop.

The arguments are the ordered visor public keys of the path, SOURCE first and
DESTINATION last (source is your own visor). For each consecutive pair the
transport ID is computed with transport.MakeTransportID(pair, --type), and the
result is assembled as:

	<tpid-1>.<tpid-2>...<tpid-N>.<dest-pk>.skynet

Paste it into the resolving proxy (skywire dmsg web / the visor skynet resolver)
to route to <dest-pk> through exactly those transports. This is a pure local
computation (no RPC, no discovery); the transports it names must already exist
(or be created) for the route to set up.

Valid transport types: dmsg, stcp, stcpr, sudph (default: sudph — dmsg transports
are not used for multi-hop routes).`,
	Args: cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		if !tptypes.Valid(tptypes.Type(routeAddrType)) {
			internal.Catch(cmd.Flags(), fmt.Errorf("invalid transport type %q (valid: %v)", routeAddrType, tptypes.Known()))
		}

		path := make([]cipher.PubKey, len(args))
		for i, a := range args {
			if err := path[i].Set(a); err != nil {
				internal.Catch(cmd.Flags(), fmt.Errorf("invalid pk #%d %q: %w", i+1, a, err))
			}
		}

		labels := make([]string, 0, len(path))
		for i := 0; i < len(path)-1; i++ {
			id := transport.MakeTransportID(path[i], path[i+1], tptypes.Type(routeAddrType))
			labels = append(labels, id.String())
		}
		labels = append(labels, path[len(path)-1].Hex())

		internal.Catch(cmd.Flags(), cliout.Print(cmd, clitp.RouteAddr{
			Address: strings.Join(labels, ".") + routeAddrSuffix,
			Labels:  labels,
		}))
	},
}
