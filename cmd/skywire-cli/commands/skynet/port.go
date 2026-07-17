// Package skynet cmd/skywire-cli/commands/skynet/port.go c4-vis-cli
package skynet

import (
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor"
)

var (
	portLabel        string
	portDesc         string
	portSkynet       bool
	portDmsg         bool
	portShowLanding  bool
	portProxyAddr    string
	portLocalPort    int
	portWhitelist    string
	portPreserveHost bool
	portInjectPK     bool
	portUDP          bool
)

func init() {
	portAddCmd.Flags().StringVarP(&portLabel, "label", "l", "", "label shown on landing page")
	portAddCmd.Flags().StringVarP(&portDesc, "desc", "d", "", "description shown on landing page")
	portAddCmd.Flags().BoolVar(&portSkynet, "skynet", true, "forward over skynet")
	portAddCmd.Flags().BoolVar(&portDmsg, "dmsg", true, "forward over DMSG")
	portAddCmd.Flags().BoolVar(&portShowLanding, "landing", true, "show link on visor landing page")
	portAddCmd.Flags().StringVar(&portProxyAddr, "proxy-addr", "", "forward to this address (host:port) instead of localhost:<local-port>; on port 80 it also replaces the landing page via reverse proxy")
	portAddCmd.Flags().IntVar(&portLocalPort, "local-port", 0, "local TCP port to forward (default: same as skynet/dmsg port)")
	portAddCmd.Flags().StringVar(&portWhitelist, "whitelist", "", "comma-separated PKs allowed to access this port (empty = allow all)")
	portAddCmd.Flags().BoolVar(&portPreserveHost, "preserve-host", false, "pass the incoming Host header through to the backend (required for vhost backends like Caddy/nginx)")
	portAddCmd.Flags().BoolVar(&portInjectPK, "inject-pk", false, "HTTP mode: inject the authenticated caller's PK as X-Skywire-Remote-PK (strips any client-supplied copy); bind the backend to loopback only")
	portAddCmd.Flags().BoolVar(&portUDP, "udp", false, "faithful-UDP mode: serve the local port as a UDP datagram service over skynet (loss-tolerant, unordered) instead of TCP")

	udpDialCmd.Flags().IntVar(&portLocalPort, "local-port", 0, "local UDP port to bind (default: same as the remote port)")

	portCmd.AddCommand(portAddCmd)
	portCmd.AddCommand(portRmCmd)
	portCmd.AddCommand(portLsCmd)
	portCmd.AddCommand(udpDialCmd)
	portCmd.AddCommand(udpStopCmd)
	portCmd.AddCommand(udpLsCmd)
	RootCmd.AddCommand(portCmd)
}

var portCmd = &cobra.Command{
	Use:        "port",
	Short:      "Manage forwarded ports (deprecated; use `cli serve`)",
	Long:       "Deprecated. Use `skywire cli serve` instead.\n\nList, add, and remove ports forwarded over skynet and/or DMSG.",
	Deprecated: "use `skywire cli serve` instead",
}

var portLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List forwarded ports",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		ports, err := rpcClient.ListForwardedPorts()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if len(ports) == 0 {
			internal.PrintOutput(cmd.Flags(), []visor.ForwardedPort{}, "No forwarded ports.\n")
			return
		}
		var buf strings.Builder
		tw := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "PORT\tLOCAL ADDR\tLABEL\tSKYNET\tDMSG\tLANDING\tDESCRIPTION") //nolint:errcheck
		for _, p := range ports {
			localAddr := p.ProxyAddr
			if localAddr == "" {
				localAddr = fmt.Sprintf("localhost:%d", p.EffectiveLocalPort())
			}
			fmt.Fprintf(tw, "%d\t%s\t%s\t%v\t%v\t%v\t%s\n", //nolint:errcheck
				p.Port,
				localAddr,
				dashIfEmpty(p.Label),
				p.Skynet,
				p.DMSG,
				p.ShowOnLanding,
				dashIfEmpty(p.Description))
		}
		tw.Flush() //nolint:errcheck,gosec
		internal.PrintOutput(cmd.Flags(), ports, buf.String())
	},
}

var portAddCmd = &cobra.Command{
	Use:   "add <port>",
	Short: "Forward a local port over skynet/DMSG",
	Long: `Forward a local TCP port over skynet and/or DMSG.

Examples:
  skywire cli skynet port add 8080
  skywire cli skynet port add 8080 --label "My App" --desc "Web dashboard"
  skywire cli skynet port add 3000 --skynet --dmsg=false --landing=false`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		port, err := strconv.Atoi(args[0])
		if err != nil || port < 1 || port > 65535 {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid port: %s", args[0]))
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		var wl []cipher.PubKey
		if portWhitelist != "" {
			for _, pkStr := range strings.Split(portWhitelist, ",") {
				pkStr = strings.TrimSpace(pkStr)
				if pkStr == "" {
					continue
				}
				var pk cipher.PubKey
				if err := pk.Set(pkStr); err != nil {
					internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid whitelist PK %q: %w", pkStr, err))
				}
				wl = append(wl, pk)
			}
		}
		fp := visor.ForwardedPort{
			Port:          port,
			LocalPort:     portLocalPort,
			Label:         portLabel,
			Description:   portDesc,
			Skynet:        portSkynet,
			DMSG:          portDmsg,
			ShowOnLanding: portShowLanding,
			ProxyAddr:     portProxyAddr,
			Whitelist:     wl,
			PreserveHost:  portPreserveHost,
			InjectPK:      portInjectPK,
			UDP:           portUDP,
		}
		if err := rpcClient.RegisterForwardedPort(fp); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		localP := fp.EffectiveLocalPort()
		fmt.Printf("Port %d forwarded", port)
		if localP != port {
			fmt.Printf(" (local TCP %d)", localP)
		}
		if portLabel != "" {
			fmt.Printf(" (%s)", portLabel)
		}
		fmt.Println()
	},
}

var portRmCmd = &cobra.Command{
	Use:   "rm <port>",
	Short: "Remove a forwarded port",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		port, err := strconv.Atoi(args[0])
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid port: %s", args[0]))
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if err := rpcClient.DeregisterTCPPort(port); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		fmt.Printf("Port %d removed\n", port)
	},
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// udpDialCmd opens a client-side faithful-UDP forward (#2607): bridge a
// local UDP socket to a remote forwarded_ports.udp service over skynet.
var udpDialCmd = &cobra.Command{
	Use:   "udp-dial <remote-pk> <remote-port>",
	Short: "Forward a local UDP port to a remote forwarded_ports.udp service over skynet",
	Long: `Open a faithful-UDP (datagram) forward to a remote visor's UDP service.

The remote visor must serve the port with 'skynet port add <port> --udp'.
This binds 127.0.0.1:<local-port> locally; your UDP app sends there and the
datagrams reach the remote service (and replies come back) — loss-tolerant
and unordered, like real UDP.`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		var pk cipher.PubKey
		if err := pk.Set(args[0]); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid remote PK: %w", err))
		}
		rPort, err := strconv.Atoi(args[1])
		if err != nil || rPort < 1 || rPort > 65535 {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid remote port: %s", args[1]))
		}
		lPort := portLocalPort
		if lPort == 0 {
			lPort = rPort
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if err := rpcClient.DialUDPForward(pk, rPort, lPort); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		fmt.Printf("UDP forward up: 127.0.0.1:%d -> %s:%d\n", lPort, pk, rPort)
	},
}

// udpStopCmd tears down a client-side faithful-UDP forward by local port.
var udpStopCmd = &cobra.Command{
	Use:   "udp-stop <local-port>",
	Short: "Stop a client-side UDP forward",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		lPort, err := strconv.Atoi(args[0])
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid local port: %s", args[0]))
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if err := rpcClient.StopUDPForward(lPort); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		fmt.Printf("UDP forward on local port %d stopped\n", lPort)
	},
}

// udpLsCmd lists active client-side faithful-UDP forwards.
var udpLsCmd = &cobra.Command{
	Use:   "udp-ls",
	Short: "List active client-side UDP forwards",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		ports, err := rpcClient.ListUDPForwards()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if len(ports) == 0 {
			fmt.Println("No active UDP forwards.")
			return
		}
		for _, p := range ports {
			fmt.Printf("127.0.0.1:%d\n", p)
		}
	},
}
