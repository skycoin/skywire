// Package skyrev cmd/skywire-cli/commands/skyfwd/root.go
package skyrev

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
)

var (
	remotePort int
	remotePk   string
	localPort  int
	lsPorts    bool
	disconnect string
)

func init() {
	RootCmd.Flags().IntVarP(&remotePort, "remote", "r", 0, "remote port to read from")
	RootCmd.Flags().StringVarP(&remotePk, "pk", "k", "", "remote public key to connect to")
	RootCmd.Flags().IntVarP(&localPort, "port", "p", 0, "local port to reverse proxy")
	RootCmd.Flags().BoolVarP(&lsPorts, "ls", "l", false, "list configured connections")
	RootCmd.Flags().StringVarP(&disconnect, "stop", "d", "", "disconnect from specified <id>")
}

// RootCmd contains commands that interact with the skyforwarding
var RootCmd = &cobra.Command{
	Use:   "skyrev",
	Short: "reverse proxy skyfwd",
	Long:  "connect or disconnect from remote ports",
	Args:  cobra.MinimumNArgs(0),
	Run: func(cmd *cobra.Command, args []string) {

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}

		if lsPorts {
			forwardConns, err := rpcClient.List()
			internal.Catch(cmd.Flags(), err)

			var b bytes.Buffer
			w := tabwriter.NewWriter(&b, 0, 0, 3, ' ', tabwriter.TabIndent)
			_, err = fmt.Fprintln(w, "id\tlocal_port\tremote_port")
			internal.Catch(cmd.Flags(), err)

			for _, forwardConn := range forwardConns {
				_, err = fmt.Fprintf(w, "%s\t%s\t%s\n", forwardConn.ID, strconv.Itoa(int(forwardConn.LocalPort)),
					strconv.Itoa(int(forwardConn.RemotePort)))
				internal.Catch(cmd.Flags(), err)
			}
			internal.Catch(cmd.Flags(), w.Flush())
			internal.PrintOutput(cmd.Flags(), forwardConns, b.String())
			os.Exit(0)
		}

		if disconnect != "" {
			id, err := uuid.Parse(disconnect)
			internal.Catch(cmd.Flags(), err)
			err = rpcClient.Disconnect(id)
			internal.Catch(cmd.Flags(), err)
			internal.PrintOutput(cmd.Flags(), "OK", "OK\n")
			os.Exit(0)
		}

		if len(args) == 0 && remotePk == "" {
			cmd.Help() //nolint
			os.Exit(0)
		}

		var remotePK cipher.PubKey

		//if pk is specified via flag, argument will override
		if len(args) > 0 {
			internal.Catch(cmd.Flags(), remotePK.Set(args[0]))
		} else {
			if remotePk != "" {
				internal.Catch(cmd.Flags(), remotePK.Set(remotePk))
			}
		}

		if remotePort == 0 || localPort == 0 {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("port cannot be 0"))
		}
		//65535 is the highest TCP port number
		if 65536 < remotePort || 65536 < localPort {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("port cannot be greater than 65535"))
		}

		id, err := rpcClient.Connect(remotePK, remotePort, localPort)
		internal.Catch(cmd.Flags(), err)
		internal.PrintOutput(cmd.Flags(), id, fmt.Sprintln(id))
	},
}
