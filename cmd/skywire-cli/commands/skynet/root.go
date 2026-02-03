// Package skynet cmd/skywire-cli/commands/skynet/root.go
package skynet

import (
	"bytes"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
)

var (
	remotePort int
	remotePk   string
	localPort  int
	lsConns    bool
	disconnect string
)

func init() {
	RootCmd.Flags().IntVarP(&remotePort, "remote", "r", 0, "remote port to connect to")
	RootCmd.Flags().StringVarP(&remotePk, "pk", "k", "", "remote public key")
	RootCmd.Flags().IntVarP(&localPort, "port", "p", 0, "local port to listen on")
	RootCmd.Flags().BoolVarP(&lsConns, "ls", "l", false, "list active connections")
	RootCmd.Flags().StringVarP(&disconnect, "stop", "d", "", "disconnect connection by <id>")

	RootCmd.AddCommand(srvCmd)
}

// RootCmd contains skynet client commands
var RootCmd = &cobra.Command{
	Use:   "skynet <public-key> -r <remote-port> -p <local-port>",
	Short: "Skynet port forwarding client",
	Long: `Connect to remote services over Skywire routes

Examples:
  skywire cli skynet 02abc... -r 8080 -p 8080    Connect remote :8080 to local :8080
  skywire cli skynet --ls                         List active connections
  skywire cli skynet --stop <id>                  Disconnect by ID`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}

		// List connections
		if lsConns {
			forwardConns, err := rpcClient.List()
			internal.Catch(cmd.Flags(), err)

			var b bytes.Buffer
			w := tabwriter.NewWriter(&b, 0, 0, 3, ' ', tabwriter.TabIndent)
			_, err = fmt.Fprintln(w, "id\tlocal_port\tremote_port")
			internal.Catch(cmd.Flags(), err)

			for _, forwardConn := range forwardConns {
				_, err = fmt.Fprintf(w, "%s\t%d\t%d\n",
					forwardConn.ID,
					forwardConn.LocalPort,
					forwardConn.RemotePort)
				internal.Catch(cmd.Flags(), err)
			}
			internal.Catch(cmd.Flags(), w.Flush())
			internal.PrintOutput(cmd.Flags(), forwardConns, b.String())
			os.Exit(0)
		}

		// Disconnect connection
		if disconnect != "" {
			id, err := uuid.Parse(disconnect)
			internal.Catch(cmd.Flags(), err)
			err = rpcClient.Disconnect(id)
			internal.Catch(cmd.Flags(), err)
			internal.PrintOutput(cmd.Flags(), "OK", "OK\n")
			os.Exit(0)
		}

		// Connect to remote service
		if len(args) == 0 && remotePk == "" {
			internal.Catch(cmd.Flags(), cmd.Help())
			os.Exit(0)
		}

		var remotePK cipher.PubKey

		// Parse public key from args or flag
		if len(args) > 0 {
			internal.Catch(cmd.Flags(), remotePK.Set(args[0]))
		} else if remotePk != "" {
			internal.Catch(cmd.Flags(), remotePK.Set(remotePk))
		} else {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("remote public key required"))
		}

		// Validate ports
		if remotePort == 0 || localPort == 0 {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("both remote and local ports required"))
		}
		if remotePort > 65535 || localPort > 65535 {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("port cannot be greater than 65535"))
		}

		// Establish connection
		id, err := rpcClient.Connect(remotePK, remotePort, localPort)
		internal.Catch(cmd.Flags(), err)
		internal.PrintOutput(cmd.Flags(), id, fmt.Sprintf("Connected: %s\n", id))
	},
}
