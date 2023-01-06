// Package connect root.go
package connect

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
	localPort  int
)

func init() {
	connectCmd.PersistentFlags().IntVarP(&remotePort, "remoteport", "r", 0, "remote port on visor to read from")
	connectCmd.PersistentFlags().IntVarP(&localPort, "localport", "l", 0, "local port for server to run on")
	registerCmd.PersistentFlags().IntVarP(&localPort, "localport", "l", 0, "local port of the external http app")
	deregisterCmd.PersistentFlags().IntVarP(&localPort, "localport", "l", 0, "local port of the external http app")
	RootCmd.AddCommand(
		registerCmd,
		deregisterCmd,
		lsPortsCmd,
		connectCmd,
		disconnectCmd,
		lsCmd,
	)
}

// RootCmd contains commands that interact with the skyproxy
var RootCmd = &cobra.Command{
	Use:   "skyproxy",
	Short: "Control skyproxy",
}

var registerCmd = &cobra.Command{
	Use:   "register",
	Short: "Register a local port to be accessed by remote visors",
	Args:  cobra.MinimumNArgs(0),
	Run: func(cmd *cobra.Command, args []string) {

		if localPort == 0 {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("required flag -localport not specified"))
		}

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}

		err = rpcClient.RegisterHTTPPort(localPort)
		internal.Catch(cmd.Flags(), err)
		internal.PrintOutput(cmd.Flags(), "OK", "OK\n")

	},
}

var deregisterCmd = &cobra.Command{
	Use:   "deregister",
	Short: "deregister a local port to be accessed by remote visors",
	Args:  cobra.MinimumNArgs(0),
	Run: func(cmd *cobra.Command, args []string) {

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}

		err = rpcClient.DeregisterHTTPPort(localPort)
		internal.Catch(cmd.Flags(), err)
		internal.PrintOutput(cmd.Flags(), "OK", "OK\n")
	},
}

var lsPortsCmd = &cobra.Command{
	Use:   "ls-ports",
	Short: "List all registered ports",
	Args:  cobra.MinimumNArgs(0),
	Run: func(cmd *cobra.Command, args []string) {

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}

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
	},
}

var connectCmd = &cobra.Command{
	Use:   "connect <pubkey>",
	Short: "Connect to a server running on a remote visor machine",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {

		var remotePK cipher.PubKey
		internal.Catch(cmd.Flags(), remotePK.Set(args[0]))

		if remotePort == 0 {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("required flag -remoteport not specified"))
		}

		if localPort == 0 {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("required flag -localport not specified"))
		}

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}

		id, err := rpcClient.Connect(remotePK, remotePort, localPort)
		internal.Catch(cmd.Flags(), err)
		internal.PrintOutput(cmd.Flags(), id, fmt.Sprintln(id))
	},
}

var disconnectCmd = &cobra.Command{
	Use:   "disconnect <id>",
	Short: "Disconnect from the server running on a remote visor machine",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		id, err := uuid.Parse(args[0])
		internal.Catch(cmd.Flags(), err)

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}

		err = rpcClient.Disconnect(id)
		internal.Catch(cmd.Flags(), err)
		internal.PrintOutput(cmd.Flags(), "OK", "OK\n")
	},
}

var lsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List all ongoing skyproxy connections",
	Args:  cobra.MinimumNArgs(0),
	Run: func(cmd *cobra.Command, args []string) {

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}

		proxies, err := rpcClient.List()
		internal.Catch(cmd.Flags(), err)

		var b bytes.Buffer
		w := tabwriter.NewWriter(&b, 0, 0, 3, ' ', tabwriter.TabIndent)
		_, err = fmt.Fprintln(w, "id\tlocal_port\tremote_port")
		internal.Catch(cmd.Flags(), err)

		for _, proxy := range proxies {
			_, err = fmt.Fprintf(w, "%s\t%s\t%s\n", proxy.ID, strconv.Itoa(int(proxy.LocalPort)),
				strconv.Itoa(int(proxy.RemotePort)))
			internal.Catch(cmd.Flags(), err)
		}
		internal.Catch(cmd.Flags(), w.Flush())
		internal.PrintOutput(cmd.Flags(), proxies, b.String())
	},
}
