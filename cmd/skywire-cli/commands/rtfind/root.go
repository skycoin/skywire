// Package clirtfind subcommand for skywire-cli
package clirtfind

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/net/context"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	utilenv "github.com/skycoin/skywire-utilities/pkg/skyenv"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/routefinder/rfclient"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var frAddr string
var frMinHops, frMaxHops uint16
var timeout time.Duration
var skywireconfig string

func init() {
	RootCmd.Flags().SortFlags = false
	RootCmd.Flags().Uint16VarP(&frMinHops, "min", "n", 1, "minimum hops")
	RootCmd.Flags().Uint16VarP(&frMaxHops, "max", "x", 1000, "maximum hops")
	RootCmd.Flags().DurationVarP(&timeout, "timeout", "t", 10*time.Second, "request timeout")
	RootCmd.Flags().StringVarP(&frAddr, "addr", "a", "", "route finder service address\n"+utilenv.RouteFinderAddr)
	var helpflag bool
	RootCmd.Flags().BoolVarP(&helpflag, "help", "h", false, "help for "+RootCmd.Use)
	RootCmd.Flags().MarkHidden("help") //nolint
}

// RootCmd is the command that queries the route finder.
var RootCmd = &cobra.Command{
	Use:   "rtfind <public-key> | <public-key-visor-1> <public-key-visor-2>",
	Short: "Query the Route Finder",
	Long: `Query the Route Finder
Assumes the local visor public key as an argument if only one argument is given`,
	// Args: cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		var srcPK, dstPK cipher.PubKey
		var pk string
		//print the help menu if no arguments
		if len(args) == 0 {
			cmd.Help() //nolint
			os.Exit(0)
		}
		//set the routefinder address. It's not used as the default value to fix the display of the help command
		if frAddr == "" {
			frAddr = utilenv.RouteFinderAddr
		}
		//assume the local public key as the first argument if only 1 argument is given ; resize args array to 2 and move the first argument to the second one
		if len(args) == 1 {
			rpcClient, err := clirpc.Client(cmd.Flags())
			if err == nil {
				overview, err := rpcClient.Overview()
				if err == nil {
					pk = overview.PubKey.String()
				}
			}
			if err != nil {
				//visor is not running, try to get pk from config
				_, err := os.Stat(skyenv.SkywirePath + "/" + skyenv.ConfigJSON)
				if err == nil {
					//default to using the package config
					skywireconfig = skyenv.SkywirePath + "/" + skyenv.ConfigJSON
				} else {
					//check for default config in current dir
					_, err := os.Stat(skyenv.ConfigName)
					if err == nil {
						//use skywire-config.json in current dir
						skywireconfig = skyenv.ConfigName
					}
				}
				conf, err := visorconfig.ReadFile(skywireconfig)
				if err != nil {
					internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Failed to read config: %v", err))
				}
				pk = conf.PK.Hex()
			}
			args = append(args[:1], args[0:]...)
			copy(args, []string{pk})
		}
		rfc := rfclient.NewHTTP(frAddr, timeout, &http.Client{}, nil)
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
