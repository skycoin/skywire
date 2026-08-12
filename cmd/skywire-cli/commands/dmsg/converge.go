// Package clidmsg cmd/skywire-cli/commands/dmsg/converge.go c4-vis-cli
package clidmsg

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/visor"
)

var convergeCarriers string

func init() {
	convergeCmd.Flags().SortFlags = false
	convergeCmd.Flags().StringVarP(&convergeCarriers, "carriers", "c", "",
		"set the ordered carrier preference first, comma-separated (e.g. wt,ws,quic,tcp)")
	convergeCmd.Flags().StringVar(&clirpc.Addr, "rpc", clirpc.DefaultRPCAddr,
		"RPC server address (env: SKYWIRE_RPC)")
	convergeCmd.Flags().Bool(internal.JSONString, false, "print output in json")
	RootCmd.AddCommand(convergeCmd)
}

var convergeCmd = &cobra.Command{
	Use:   "converge",
	Short: "Converge each dmsg-server session onto its most-preferred carrier",
	Long: `Runs one carrier-convergence pass on the visor's main dmsg client: for
each dmsg-server session, look up the server's CURRENT discovery entry and
re-dial onto the most-preferred carrier it advertises, per the client's
ordered carrier preference (dmsg.carriers / --carriers).

The re-dial replaces the session in place (the session count never grows).
A server whose preferred endpoint is unreachable stays on its working
carrier and is backed off, so repeated runs don't thrash.

Examples:
  skywire cli dmsg converge --carriers wt,ws     # prefer WebTransport, fall back to WebSocket
  skywire cli dmsg converge                      # converge using the already-set preference`,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		var carriers []string
		if strings.TrimSpace(convergeCarriers) != "" {
			for _, c := range strings.Split(convergeCarriers, ",") {
				if c = strings.TrimSpace(c); c != "" {
					carriers = append(carriers, c)
				}
			}
		}
		result, err := rpcClient.DmsgConverge(carriers)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if isJSON, _ := cmd.Flags().GetBool(internal.JSONString); isJSON { //nolint:errcheck
			internal.PrintOutput(cmd.Flags(), result, "")
			return
		}
		printConverge(result)
	},
}

func printConverge(r *visor.DmsgConvergeResult) {
	if r == nil {
		return
	}
	fmt.Printf("carrier preference: %s\n", strings.Join(r.Carriers, " > "))
	fmt.Printf("converged this pass: %d session(s)\n", r.Converged)
	if len(r.Sessions) == 0 {
		fmt.Println("no dmsg-server sessions")
		return
	}
	fmt.Println("sessions:")
	for _, s := range r.Sessions {
		mark := ""
		if s.Preferred != "" && s.Preferred != s.Carrier {
			mark = fmt.Sprintf("  (prefers %s)", s.Preferred)
		}
		fmt.Printf("  %s  %-4s  %s%s\n", s.ServerPK.Hex()[:8], s.Carrier, s.Addr, mark)
		if s.Note != "" {
			fmt.Printf("        ↳ %s\n", s.Note)
		}
	}
}
