// Package skynet port.go — CLI commands for managing forwarded ports
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
	portLabel       string
	portDesc        string
	portSkynet      bool
	portDmsg        bool
	portShowLanding bool
	portProxyAddr   string
	portLocalPort   int
	portWhitelist   string
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

	portCmd.AddCommand(portAddCmd)
	portCmd.AddCommand(portRmCmd)
	portCmd.AddCommand(portLsCmd)
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
