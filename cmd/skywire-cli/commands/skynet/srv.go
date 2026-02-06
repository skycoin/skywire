// Package skynet srv.go
package skynet

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
	srvPort       int
	srvDeregister bool
	srvList       bool
)

func init() {
	srvCmd.Flags().IntVarP(&srvPort, "port", "p", 0, "local port to expose")
	srvCmd.Flags().BoolVarP(&srvDeregister, "stop", "d", false, "stop exposing port")
	srvCmd.Flags().BoolVarP(&srvList, "ls", "l", false, "list exposed ports")
}

var srvCmd = &cobra.Command{
	Use:   "srv <port>",
	Short: "Skynet port forwarding server",
	Long: `Expose local services over Skywire routes

Examples:
  skywire cli skynet srv 8080           Expose local port 8080
  skywire cli skynet srv 8080 --stop    Stop exposing port 8080
  skywire cli skynet srv --ls           List exposed ports`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}

		// List exposed ports
		if srvList {
			ports, err := rpcClient.ListHTTPPorts()
			internal.Catch(cmd.Flags(), err)

			var b bytes.Buffer
			w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', tabwriter.TabIndent)
			_, err = fmt.Fprintln(w, "id\tlocal_port")
			internal.Catch(cmd.Flags(), err)

			for id, port := range ports {
				_, err = fmt.Fprintf(w, "%d\t%d\n", id, port)
				internal.Catch(cmd.Flags(), err)
			}
			internal.Catch(cmd.Flags(), w.Flush())
			internal.PrintOutput(cmd.Flags(), ports, b.String())
			os.Exit(0)
		}

		// Parse port from args or flag
		if len(args) == 0 && srvPort == 0 {
			internal.Catch(cmd.Flags(), cmd.Help())
			os.Exit(0)
		}

		if len(args) > 0 {
			srvPort, err = strconv.Atoi(args[0])
			internal.Catch(cmd.Flags(), err)
		}

		// Validate port
		if srvPort == 0 {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("port cannot be 0"))
		}
		if srvPort > 65535 {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("port cannot be greater than 65535"))
		}

		// Register or deregister port
		if srvDeregister {
			err = rpcClient.DeregisterHTTPPort(srvPort)
			internal.Catch(cmd.Flags(), err)
			internal.PrintOutput(cmd.Flags(), "OK", fmt.Sprintf("Stopped exposing port %d\n", srvPort))
		} else {
			err = rpcClient.RegisterHTTPPort(srvPort)
			internal.Catch(cmd.Flags(), err)
			internal.PrintOutput(cmd.Flags(), "OK", fmt.Sprintf("Exposing port %d\n", srvPort))
		}
	},
}
