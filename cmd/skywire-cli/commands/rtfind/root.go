// Package clirtfind subcommand for skywire-cli
package clirtfind

import (
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/net/context"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	utilenv "github.com/skycoin/skywire-utilities/pkg/skyenv"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/routefinder/rfclient"
	"github.com/skycoin/skywire/pkg/routing"
)

var frAddr string
var frMinHops, frMaxHops uint16
var timeout time.Duration

func init() {
	RootCmd.Flags().StringVarP(&frAddr, "addr", "a", utilenv.RouteFinderAddr, "route finder service address")
	RootCmd.Flags().Uint16VarP(&frMinHops, "min-hops", "n", 1, "minimum hops")
	RootCmd.Flags().Uint16VarP(&frMaxHops, "max-hops", "x", 1000, "maximum hops")
	RootCmd.Flags().DurationVarP(&timeout, "timeout", "t", 10*time.Second, "request timeout")
	var helpflag bool
	RootCmd.Flags().BoolVarP(&helpflag, "help", "h", false, "help for "+RootCmd.Use)
	RootCmd.Flags().MarkHidden("help") //nolint
}

// RootCmd is the command that queries the route finder.
var RootCmd = &cobra.Command{
	Use:   "rtfind <public-key-visor-1> <public-key-visor-2>",
	Short: "Query the Route Finder",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		rfc := rfclient.NewHTTP(frAddr, timeout, &http.Client{}, nil)

		var srcPK, dstPK cipher.PubKey
		internal.Catch(cmd.Flags(), srcPK.Set(args[0]))
		internal.Catch(cmd.Flags(), dstPK.Set(args[1]))
		forward := [2]cipher.PubKey{srcPK, dstPK}
		backward := [2]cipher.PubKey{dstPK, srcPK}
		ctx := context.Background()
		routes, err := rfc.FindRoutes(ctx, []routing.PathEdges{forward, backward},
			&rfclient.RouteOptions{MinHops: frMinHops, MaxHops: frMaxHops})
		internal.Catch(cmd.Flags(), err)

		output := fmt.Sprintf("forward: %v\n reverse: %v", routes[forward][0], routes[backward][0])
		outputJSON := struct {
			Forward []routing.Hop `json:"forward"`
			Reverse []routing.Hop `json:"reverse"`
		}{
			Forward: routes[forward][0],
			Reverse: routes[backward][0],
		}
		internal.PrintOutput(cmd.Flags(), outputJSON, output)
	},
}
