package rtfind

import (
	"fmt"
	"net/http"
	"time"

	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/dmsg/dmsghttp"
	"github.com/spf13/cobra"
	"golang.org/x/net/context"

	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/routefinder/rfclient"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
)

var frAddr string
var frMinHops, frMaxHops uint16
var timeout time.Duration

func init() {
	RootCmd.Flags().StringVar(&frAddr, "addr", skyenv.DefaultRouteFinderAddr, "address in which to contact route finder service")
	RootCmd.Flags().Uint16Var(&frMinHops, "min-hops", 1, "min hops for the returning routeFinderRoutesCmd")
	RootCmd.Flags().Uint16Var(&frMaxHops, "max-hops", 1000, "max hops for the returning routeFinderRoutesCmd")
	RootCmd.Flags().DurationVar(&timeout, "timeout", 10*time.Second, "timeout for remote server requests")
}

// RootCmd is the command that queries the route finder.
var RootCmd = &cobra.Command{
	Use:   "rtfind <public-key-visor-1> <public-key-visor-2>",
	Short: "Queries the Route Finder for available routes between two visors",
	Args:  cobra.MinimumNArgs(2),
	Run: func(_ *cobra.Command, args []string) {
		rfc := rfclient.NewHTTP(frAddr, timeout, &http.Client{}, &dmsghttp.StreamCloser{}, nil)

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
