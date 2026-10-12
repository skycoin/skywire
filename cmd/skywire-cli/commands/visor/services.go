// Package clivisor cmd/skywire-cli/commands/visor/services.go c4-vis-cli
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
	RootCmd.AddCommand(servicesCmd)
	for _, action := range []string{"stop", "start", "restart"} {
		servicesCmd.AddCommand(serviceActionCmd(action))
	}
}

var servicesCmd = &cobra.Command{
	Use:   "services",
	Short: "List the deployment services this visor embeds",
	Long: `List the deployment services this visor runs in its own process, from
config.embedded_services. A service under its own key can be stopped,
started and restarted on its own; a mounted one runs with the visor.`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}
		snap, err := rpcClient.StateSnapshotProjected([]string{visorapi.SelectServices})
		if err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}
		var b strings.Builder
		if len(snap.Services) == 0 {
			b.WriteString("no embedded services\n")
		}
		for _, s := range snap.Services {
			state := "running"
			switch {
			case s.Stopped:
				state = "stopped"
			case !s.Running:
				state = "down"
			}
			fmt.Fprintf(&b, "%-20s %-20s %-8s restarts=%d %s", s.Name, s.Type, state, s.Restarts, s.URL)
			if s.Error != "" {
				fmt.Fprintf(&b, "  error: %s", s.Error)
			}
			b.WriteString("\n")
		}
		internal.PrintOutput(cmd.Flags(), snap.Services, b.String())
	},
}

func serviceActionCmd(action string) *cobra.Command {
	return &cobra.Command{
		Use:   action + " <name>",
		Short: strings.ToUpper(action[:1]) + action[1:] + " an embedded service that runs under its own key",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			rpcClient, err := clirpc.Client(cmd.Flags())
			if err != nil {
				internal.PrintFatalRPCError(cmd.Flags(), err)
			}
			if err := rpcClient.EmbeddedServiceControl(args[0], action); err != nil {
				internal.PrintFatalError(cmd.Flags(), err)
			}
			internal.PrintOutput(cmd.Flags(), "OK", fmt.Sprintf("%s: %s requested\n", args[0], action))
		},
	}
}
