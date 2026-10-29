// Package clivisor cmd/skywire-cli/commands/visor/network.go c4-vis-cli
package clivisor

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

func init() {
	RootCmd.AddCommand(networkCmd)
	networkCmd.AddCommand(networkShowCmd, networkMovedCmd, networkSetCmd)
	networkSetCmd.Flags().StringSliceVar(&networkAllow, "allow", nil, "interface globs or CIDRs to advertise; empty allows every interface")
	networkSetCmd.Flags().StringSliceVar(&networkDeny, "deny", nil, "interface globs or CIDRs never to advertise; wins over --allow")
	networkMovedCmd.Flags().StringVar(&networkMovedReason, "reason", "manual", "reason recorded in the visor log and state")
}

var (
	networkMovedReason string
	networkAllow       []string
	networkDeny        []string
)

var networkCmd = &cobra.Command{
	Use:   "network",
	Short: "The network the visor is on",
	Long: `The public IP, NAT class and LAN addresses the visor advertises, and its
handling of a move to another network. The visor watches its addresses and
rechecks its public IP on its own; "moved" asks for a refresh by hand.`,
}

var networkShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show the public IP, NAT class, advertised addresses and last move",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}
		snap, err := rpcClient.StateSnapshotProjected([]string{visorapi.SelectNetwork})
		if err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}
		if snap.Network == nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("this visor does not report its network state"))
		}
		internal.PrintOutput(cmd.Flags(), snap.Network, formatNetworkState(snap.Network))
	},
}

var networkMovedCmd = &cobra.Command{
	Use:   "moved",
	Short: "Refresh the public IP, NAT class, addresses and registrations now",
	Long: `Refresh what the visor advertises as if it had moved to another network.
It reconnects dmsg, probes STUN, looks up the public IP, and registers every
transport type with the address resolver again. The refresh runs in the
background; "network show" reports it once done.`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}
		if err := rpcClient.NetworkMoved(networkMovedReason); err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}
		internal.PrintOutput(cmd.Flags(), "OK", "network refresh queued\n")
	},
}

var networkSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Change the network policy live",
	Long: `Change which interfaces and addresses the visor offers to peers. An entry
is an interface name glob ("wlan*") or an address range ("10.0.0.0/8").
Deny wins over allow, and an empty allow list allows every interface. An
allow glob naming a virtual interface (wg0, tailscale0) brings it back.
The change is saved to transport.network and the visor registers its new
address set at once. Pass a flag with an empty value to clear that list.

  skywire cli visor network set --deny "docker*,10.8.0.0/16"
  skywire cli visor network set --deny ""`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, _ []string) {
		var in visorapi.NetworkSettings
		if cmd.Flags().Changed("allow") {
			in.Allow = &networkAllow
		}
		if cmd.Flags().Changed("deny") {
			in.Deny = &networkDeny
		}
		if in == (visorapi.NetworkSettings{}) {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("nothing to set: pass --allow or --deny"))
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}
		if err := rpcClient.NetworkSet(in); err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}
		internal.PrintOutput(cmd.Flags(), "OK", "network settings applied\n")
	},
}

func formatNetworkState(n *visorapi.NetworkState) string {
	var b strings.Builder
	at := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return " (" + t.Format(time.RFC3339) + ")"
	}
	orDash := func(s string) string {
		if s == "" {
			return "-"
		}
		return s
	}
	src := ""
	if n.PublicIPSource != "" {
		src = " via " + n.PublicIPSource
	}
	fmt.Fprintf(&b, "public ip:      %s%s%s\n", orDash(n.PublicIP), src, at(n.PublicIPAt))
	if n.PublicIPv6 != "" {
		fmt.Fprintf(&b, "public ipv6:    %s\n", n.PublicIPv6)
	}
	fmt.Fprintf(&b, "nat:            %s%s\n", orDash(n.NAT), at(n.NATAt))
	fmt.Fprintf(&b, "lan addresses:  %s\n", orDash(strings.Join(n.LocalAddresses, ", ")))
	if len(n.Allow) > 0 || len(n.Deny) > 0 {
		fmt.Fprintf(&b, "policy:         allow %s, deny %s\n", orDash(strings.Join(n.Allow, ",")), orDash(strings.Join(n.Deny, ",")))
	}
	if len(n.PolicyExcluded) > 0 {
		fmt.Fprintf(&b, "not advertised: %s\n", strings.Join(n.PolicyExcluded, ", "))
	}
	types := make([]string, 0, len(n.Advertised))
	for t := range n.Advertised {
		types = append(types, t)
	}
	sort.Strings(types)
	for _, t := range types {
		fmt.Fprintf(&b, "  %-12s %s\n", t+":", orDash(strings.Join(n.Advertised[t], ", ")))
	}
	fmt.Fprintf(&b, "moves:          %d", n.Moves)
	if n.Moves > 0 {
		fmt.Fprintf(&b, ", last%s: %s", at(n.LastMoveAt), n.LastMoveReason)
	}
	if n.Pending {
		b.WriteString(" (refresh pending)")
	}
	b.WriteString("\n")
	if n.LastMoveChange != "" {
		fmt.Fprintf(&b, "last change:    %s\n", n.LastMoveChange)
	}
	fmt.Fprintf(&b, "watch every:    %s, public ip check every %s\n", n.WatchInterval, n.PublicIPCheckInterval)
	return b.String()
}
