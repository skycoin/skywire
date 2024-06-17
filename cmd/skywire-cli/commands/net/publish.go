// Package net cmd/skywire-cli/commands/net/publish.go
package net

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"

	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
)

var (
	portNo     int
	deregister bool
)

func init() {
	pubCmd.PersistentFlags().IntVarP(&portNo, "port", "p", 0, "local port of the external (http) app")
	pubCmd.PersistentFlags().BoolVarP(&deregister, "deregister", "d", false, "deregister local port of the external (http) app")
	pubCmd.PersistentFlags().BoolVarP(&lsPorts, "ls", "l", false, "list published local ports")
	RootCmd.AddCommand(pubCmd)
}

// pubCmd contains commands to publish over the skywire network
var pubCmd = &cobra.Command{
	Use:   "pub",
	Short: "Publish over skywire network",
	Long:  "Publish over skywire network\nPublish a local port over the skywire network. This will allow other nodes to access the local port via the skywire network.",
	Args:  cobra.MinimumNArgs(0),
	Run: func(cmd *cobra.Command, args []string) {

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}

		if lsPorts {
			ports, err := rpcClient.ListHTTPPorts()
			internal.Catch(cmd.Flags(), err)
			var b bytes.Buffer
			w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', tabwriter.TabIndent)
			_, err = fmt.Fprintln(w, "id\tlocal_port")
			internal.Catch(cmd.Flags(), err)
			for id, port := range ports {
				_, err = fmt.Fprintf(w, "%v\t%v\n", id, port)
				internal.Catch(cmd.Flags(), err)
			}
			internal.Catch(cmd.Flags(), w.Flush())
			internal.PrintOutput(cmd.Flags(), ports, b.String())
			os.Exit(0)
		}

		if len(args) == 0 && portNo == 0 {
			cmd.Help() //nolint
			os.Exit(0)
		}

		//if port is specified via flag, argument will override
		if len(args) > 0 {
			portNo, err = strconv.Atoi(args[0])
			internal.Catch(cmd.Flags(), err)
		}

		//port 0 is reserved / not usable
		if portNo == 0 {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("port cannot be 0"))
		}

		//65535 is the highest TCP port number
		if 65536 < portNo {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("port cannot be greater than 65535"))
		}

		if deregister {
			err = rpcClient.DeregisterHTTPPort(portNo)
			internal.Catch(cmd.Flags(), err)

		} else {
			err = rpcClient.RegisterHTTPPort(portNo)
			internal.Catch(cmd.Flags(), err)
			id, err := rpcClient.Publish(portNo)
			internal.Catch(cmd.Flags(), err)
			internal.PrintOutput(cmd.Flags(), "id: %v\n", fmt.Sprintln(id))
		}

	},
}
