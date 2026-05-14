// Package climail cmd/skywire-cli/commands/mail/mail.go
//
// `skywire cli mail` — runtime control surface for the visor's
// in-process SMTP→skywire bridge (pkg/visor/embedded_skymail_bridge.go).
//
// SMTP-side analog of `skywire cli resolver`. Bare `mail` prints
// status; `up`/`down` toggle the bridge via the same
// SetEmbeddedProxyEnabled RPC the resolver tree uses, with kind
// "bridge".
package climail

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/visor"
)

const proxyKind = "bridge"

func init() {
	RootCmd.AddCommand(upCmd)
	RootCmd.AddCommand(downCmd)
}

// RootCmd is the cobra entry for `skywire cli mail`. Print-status
// when invoked bare.
var RootCmd = &cobra.Command{
	Use:   "mail",
	Short: "Embedded SMTP→skywire bridge (skymail-bridge)",
	Long: `Status, enable, and disable the visor's embedded SMTP bridge.

The bridge accepts SMTP from a co-located Postfix's transport_map
and dials peer visors over the visor's existing dmsg client. With
the bridge running, a Postfix transport_map line

  .skynet  relay:[127.0.0.1]:1025

routes envelopes addressed to user@<host>.<base32-pk>.skynet (or
user@<base32-pk>.skynet) over skywire to the peer's Postfix smtpd.

Without arguments, prints current state.

Examples:
  skywire cli mail                                  # status
  skywire cli mail up                               # turn on
  skywire cli mail down                             # turn off
`,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		status, err := rpcClient.EmbeddedProxies()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("EmbeddedProxies RPC failed: %w", err))
		}
		internal.PrintOutput(cmd.Flags(), status, renderMailStatus(status))
	},
}

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Enable the embedded SMTP bridge",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if err := rpcClient.SetEmbeddedProxyEnabled(proxyKind, true); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("enable skymail-bridge: %w", err))
		}
		internal.PrintOutput(cmd.Flags(), map[string]any{"up": true},
			"skymail-bridge enabled. Remember the Postfix transport_map line:\n"+
				"  .skynet  relay:[127.0.0.1]:1025\n")
	},
}

var downCmd = &cobra.Command{
	Use:   "down",
	Short: "Disable the embedded SMTP bridge",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if err := rpcClient.SetEmbeddedProxyEnabled(proxyKind, false); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("disable skymail-bridge: %w", err))
		}
		internal.PrintOutput(cmd.Flags(), map[string]any{"down": true}, "skymail-bridge disabled.\n")
	},
}

// renderMailStatus is the human-readable status table. Returns "(no
// skymail_bridge configured)" rather than an empty table so the
// operator can distinguish absent from disabled.
func renderMailStatus(s *visor.EmbeddedProxiesStatus) string {
	if s == nil || s.SkymailBridge == nil {
		return "(no skymail_bridge configured)\n"
	}
	b := s.SkymailBridge
	var buf strings.Builder
	w := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "enabled\trunning\taddr\tmode\tsuffix")     //nolint:errcheck,gosec
	fmt.Fprintln(w, "-------\t-------\t----\t----\t------")     //nolint:errcheck,gosec
	fmt.Fprintf(w, "%v\t%v\t%s\t%s\t%s\n",                       //nolint:errcheck,gosec
		b.Enabled, b.Running,
		dashIfEmpty(b.Addr),
		dashIfEmpty(b.Mode),
		dashIfEmpty(b.Suffix))
	_ = w.Flush() //nolint:errcheck,gosec
	return buf.String()
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
