// Package net cmd/skywire-cli/commands/net/connect.go
package net

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/app/appnet"
)

var (
	localPort int
)

func init() {
	conCmd.Flags().IntVarP(&localPort, "port", "p", 0, "local port to serve the remote app on")
	conCmd.Flags().StringVarP(&netType, "type", "t", "http", "type of the remote app connection (http, tcp, udp)")

	conCmd.AddCommand(lsCmd)
	conCmd.AddCommand(stopCmd)
	RootCmd.AddCommand(conCmd)
}

// conCmd contains commands to connect to a published app on the skywire network
var conCmd = &cobra.Command{
	Use:   "con <remote_pk:remote_port> [flags]",
	Short: "Connect to a published app on the skywire network",
	Long:  "Connect to a published app on the skywire network.\n This will allow you to access the remote app via the skywire network.",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}

		if len(args) == 0 {
			cmd.Help() //nolint
			os.Exit(0)
		}

		var remotePK cipher.PubKey
		var remotePort int

		parts := strings.Split(args[0], ":")

		if len(parts) != 2 {
			cmd.Help() //nolint
			os.Exit(0)
		}

		internal.Catch(cmd.Flags(), remotePK.Set(parts[0]))

		if remotePort, err = strconv.Atoi(parts[1]); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid port: %s", parts[1]))
		}

		if remotePort == 0 || localPort == 0 {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("port cannot be 0"))
		}
		//65535 is the highest TCP port number
		if 65536 < remotePort || 65536 < localPort {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("port cannot be greater than 65535"))
		}

		var appType appnet.AppType

		switch netType {
		case "http":
			appType = appnet.HTTP
		case "tcp":
			appType = appnet.TCP
		case "udp":
			appType = appnet.UDP
		default:
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid type"))
		}

		connInfo, err := rpcClient.Connect(remotePK, remotePort, localPort, appType)
		internal.Catch(cmd.Flags(), err)

		internal.PrintOutput(cmd.Flags(), connInfo, fmt.Sprintf("Connected to %s with ID: %s\n", connInfo.RemoteAddr.String(), connInfo.ID.String()))
		internal.PrintOutput(cmd.Flags(), connInfo, fmt.Sprintf("%v available on localhost:%d\n", connInfo.AppType, connInfo.WebPort))
	},
}

// lsCmd contains commands to list connected apps on the skywire network
var lsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List connected apps on the skywire network",
	Long:  "List connected apps on the skywire network.\nThis will show you the ID, address, and web port of the connected apps.",
	Args:  cobra.MinimumNArgs(0),
	Run: func(cmd *cobra.Command, args []string) {

		if len(args) != 0 {
			cmd.Help() //nolint
			os.Exit(0)
		}

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}

		connectConns, err := rpcClient.ListConnected()
		internal.Catch(cmd.Flags(), err)

		var b bytes.Buffer
		w := tabwriter.NewWriter(&b, 0, 0, 3, ' ', tabwriter.TabIndent)
		_, err = fmt.Fprintln(w, "id\taddr\tweb_port\tapp_type")
		internal.Catch(cmd.Flags(), err)

		for _, connectConn := range connectConns {
			_, err = fmt.Fprintf(w, "%v\t%v\t%v\t%v\n", connectConn.ID, connectConn.RemoteAddr,
				connectConn.WebPort, connectConn.AppType)
			internal.Catch(cmd.Flags(), err)
		}
		internal.Catch(cmd.Flags(), w.Flush())
		internal.PrintOutput(cmd.Flags(), connectConns, b.String())
	},
}

// stopCmd contains commands to stop a connection to a published app on the skywire network
var stopCmd = &cobra.Command{
	Use:   "stop <id>",
	Short: "Stop a connection to a published app on the skywire network",
	Long:  "Stop a connection to a published app on the skywire network.\nThis will disconnect you from the remote app.",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {

		if len(args) == 0 {
			cmd.Help() //nolint
			os.Exit(0)
		}

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}

		id, err := uuid.Parse(args[0])
		internal.Catch(cmd.Flags(), err)
		err = rpcClient.Disconnect(id)
		internal.Catch(cmd.Flags(), err)
		internal.PrintOutput(cmd.Flags(), "OK", "OK\n")
	},
}
