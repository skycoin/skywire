package rtfind

import (
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/net/context"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/routefinder/rfclient"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
)

var frAddr string
var frMinHops, frMaxHops uint16
var timeout time.Duration

func init() {
	RootCmd.Flags().StringVarP(&frAddr, "addr", "a", skyenv.DefaultRouteFinderAddr, "route finder service address")
	RootCmd.Flags().Uint16VarP(&frMinHops, "min-hops", "n", 1, "minimum hops")
	RootCmd.Flags().Uint16VarP(&frMaxHops, "max-hops", "x", 1000, "maximum hops")
	RootCmd.Flags().DurationVarP(&timeout, "timeout", "t", 10*time.Second, "request timeout")
}

// RootCmd is the command that queries the route finder.
var RootCmd = &cobra.Command{
	Use:   "rtfind <public-key-visor-1> <public-key-visor-2>",
	Short: "Query the Route Finder",
	Args:  cobra.MinimumNArgs(2),
	Run: func(_ *cobra.Command, args []string) {
		rfc := rfclient.NewHTTP(frAddr, timeout, &http.Client{}, nil)

		var srcPK, dstPK cipher.PubKey
		internal.Catch(srcPK.Set(args[0]))
		internal.Catch(dstPK.Set(args[1]))
		forward := [2]cipher.PubKey{srcPK, dstPK}
		backward := [2]cipher.PubKey{dstPK, srcPK}
		ctx := context.Background()
		routes, err := rfc.FindRoutes(ctx, []routing.PathEdges{forward, backward},
			&rfclient.RouteOptions{MinHops: frMinHops, MaxHops: frMaxHops})
		internal.Catch(err)

		fmt.Println("forward: ", routes[forward][0])
		fmt.Println("reverse: ", routes[backward][0])
	},
}
