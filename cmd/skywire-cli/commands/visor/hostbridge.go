// Package clivisor cmd/skywire-cli/commands/visor/hostbridge.go c4-vis-cli
package clivisor

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

func init() {
	RootCmd.AddCommand(hostBridgeCmd)
	hostBridgeCmd.AddCommand(hbConnectCmd, hbDisconnectCmd, hbStatusCmd, hbForwardCmd, hbUnforwardCmd)
}

var hostBridgeCmd = &cobra.Command{
	Use:   "hostbridge",
	Short: "Use the sockets of a host bridge",
	Long: `Use the sockets of a host bridge.

A visor in a browser tab cannot listen on a port of its host. A host bridge,
started on that host with 'skywire hostbridge', listens for it. Connect the
visor to the URL the bridge prints, then forward host ports to addresses
the visor can dial, such as the local port of skysocks-client.

A computer without skywire can get the bridge from a browser visor: open
/hostbridge/ on its dashboard (the "host bridge" app in the desk). It offers
the build for the computer and shows the command to run it. The guide is
docs/guides/host-bridge.md.`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, _ []string) {
		printHostBridgeStatus(cmd)
	},
}

var hbConnectCmd = &cobra.Command{
	Use:   "connect <ws url>",
	Short: "Connect to the host bridge at the URL it printed",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}
		if err := rpcClient.HostBridgeConnect(args[0]); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		internal.PrintOutput(cmd.Flags(), "OK", "connected\n")
	},
}

var hbDisconnectCmd = &cobra.Command{
	Use:   "disconnect",
	Short: "End the session to the host bridge and its forwards",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}
		if err := rpcClient.HostBridgeDisconnect(); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		internal.PrintOutput(cmd.Flags(), "OK", "disconnected\n")
	},
}

var hbStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the session to the host bridge and its forwards",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, _ []string) {
		printHostBridgeStatus(cmd)
	},
}

var hbForwardCmd = &cobra.Command{
	Use:   "forward <host addr> <local addr>",
	Short: "Listen on a port of the bridge's host and pass connections to a local address",
	Long: `Listen on <host addr> on the bridge's host and pass each connection to
<local addr>, an address the visor can dial. For example, to use the tab's
proxy client from the host:

  skywire cli visor hostbridge forward 127.0.0.1:1080 127.0.0.1:1080

The forward is reopened when the visor reconnects to the bridge.`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}
		f, err := rpcClient.HostBridgeForward(args[0], args[1])
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		internal.PrintOutput(cmd.Flags(), f, fmt.Sprintf("forwarding %s on the host to %s\n", f.Bound, f.Local))
	},
}

var hbUnforwardCmd = &cobra.Command{
	Use:   "unforward <host addr>",
	Short: "Stop a forward, named by the host address it was asked for",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}
		if err := rpcClient.HostBridgeUnforward(args[0]); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		internal.PrintOutput(cmd.Flags(), "OK", "stopped\n")
	},
}

func printHostBridgeStatus(cmd *cobra.Command) {
	rpcClient, err := clirpc.Client(cmd.Flags())
	if err != nil {
		internal.PrintFatalRPCError(cmd.Flags(), err)
	}
	st, err := rpcClient.HostBridgeStatus()
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}
	internal.PrintOutput(cmd.Flags(), st, formatHostBridgeStatus(st))
}

func formatHostBridgeStatus(st visorapi.HostBridgeStatus) string {
	var b strings.Builder
	switch {
	case st.URL == "":
		b.WriteString("not connected to a host bridge\n")
	case st.Connected:
		fmt.Fprintf(&b, "connected to %s\n", st.URL)
	default:
		fmt.Fprintf(&b, "reconnecting to %s", st.URL)
		if st.Error != "" {
			fmt.Fprintf(&b, ": %s", st.Error)
		}
		b.WriteString("\n")
	}
	for _, f := range st.Forwards {
		host := f.Bound
		if host == "" {
			host = f.Host
		}
		fmt.Fprintf(&b, "  %s -> %s", host, f.Local)
		if f.Error != "" {
			fmt.Fprintf(&b, "  error: %s", f.Error)
		}
		b.WriteString("\n")
	}
	return b.String()
}
