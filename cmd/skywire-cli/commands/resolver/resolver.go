// Package cliresolver cmd/skywire-cli/commands/resolver/resolver.go
//
// `skywire cli resolver` — surface for the embedded `.dmsg` and
// `.skynet` resolving SOCKS5 proxies that live inside the visor.
// Replaces the older `skywire cli visor proxies` tree:
//
//	visor proxies set dmsg on        →  resolver up      (or: resolver up --dmsg-only)
//	visor proxies set skynet off     →  resolver down --skynet-only
//	visor proxies upstream <k> <a>   →  resolver up --upstream <a>
//	visor proxies                    →  resolver
//
// Behavior conventions:
//   - bare `resolver` prints status (same table as before).
//   - `up` and `down` are subcommands, not "on/off" arguments.
//   - by default both .dmsg and .skynet are toggled together; the
//     auto-chain (dmsgweb → skynetweb → upstream) is wired up
//     client-side from the single `--upstream` flag.
//   - --dmsg-only / --skynet-only narrow the scope when needed.
package cliresolver

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/visor"
)

// Listener ports of the embedded resolvers. Match
// pkg/visor/embedded_dmsgweb.go and embedded_skynetweb.go defaults.
const (
	dmsgwebSOCKSPort   = 4445
	skynetwebSOCKSPort = 4446
)

var (
	resolverJSON       bool
	resolverDmsgOnly   bool
	resolverSkynetOnly bool
	resolverUpstream   string
	resolverNoChain    bool
)

func init() {
	RootCmd.Flags().BoolVar(&resolverJSON, "json", false, "emit raw JSON")

	upCmd.Flags().BoolVar(&resolverDmsgOnly, "dmsg-only", false, "only enable the .dmsg resolver")
	upCmd.Flags().BoolVar(&resolverSkynetOnly, "skynet-only", false, "only enable the .skynet resolver")
	upCmd.Flags().StringVar(&resolverUpstream, "upstream", "", "upstream SOCKS5 for non-matching traffic (e.g. 127.0.0.1:1080)")
	upCmd.Flags().BoolVar(&resolverNoChain, "no-chain", false, "skip the dmsgweb→skynetweb auto-chain even when both resolvers are up")
	upCmd.MarkFlagsMutuallyExclusive("dmsg-only", "skynet-only")

	downCmd.Flags().BoolVar(&resolverDmsgOnly, "dmsg-only", false, "only disable the .dmsg resolver")
	downCmd.Flags().BoolVar(&resolverSkynetOnly, "skynet-only", false, "only disable the .skynet resolver")
	downCmd.MarkFlagsMutuallyExclusive("dmsg-only", "skynet-only")

	RootCmd.AddCommand(upCmd)
	RootCmd.AddCommand(downCmd)
}

// RootCmd is the resolver command tree.
var RootCmd = &cobra.Command{
	Use:   "resolver",
	Short: "Embedded .dmsg / .skynet resolving SOCKS5 proxies",
	Long: `Status, enable, and disable the visor's embedded resolving
SOCKS5 proxies. Point a browser at 127.0.0.1:4445 (dmsgweb) and the
.dmsg / .skynet domain suffixes are tunneled directly through the
visor; everything else hits the optional upstream SOCKS5.

Without arguments, prints the current state of both resolvers.

Examples:
  skywire cli resolver                                  # status
  skywire cli resolver up                               # both, no upstream
  skywire cli resolver up --upstream 127.0.0.1:1080     # both, chained → skysocks
  skywire cli resolver up --dmsg-only                   # only .dmsg
  skywire cli resolver down                             # both off
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
		if resolverJSON {
			_ = json.NewEncoder(os.Stdout).Encode(status) //nolint:errcheck,gosec
			return
		}
		printResolverStatus(status)
	},
}

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Enable the embedded resolving proxies",
	Long: `Enable the visor's embedded .dmsg / .skynet resolving SOCKS5
proxies. By default both come up and the dmsgweb resolver is auto-
chained to skynetweb so a single browser proxy entry (127.0.0.1:4445)
covers .dmsg and .skynet plus everything routed through --upstream.

  --upstream <addr>      where non-matching traffic goes (after the chain)
  --dmsg-only            only enable the .dmsg resolver
  --skynet-only          only enable the .skynet resolver
  --no-chain             keep the resolvers independent (skip auto-chain)

The bare command turns ON both resolvers; subsequent calls with
different flags update the upstream.`,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		wantDmsg := !resolverSkynetOnly
		wantSkynet := !resolverDmsgOnly

		if wantDmsg {
			if err := rpcClient.SetEmbeddedProxyEnabled("dmsg", true); err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("enable dmsg resolver: %w", err))
			}
		}
		if wantSkynet {
			if err := rpcClient.SetEmbeddedProxyEnabled("skynet", true); err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("enable skynet resolver: %w", err))
			}
		}

		// Wire upstream and the auto-chain. If both resolvers are up
		// and the user didn't explicitly opt out, chain dmsgweb to
		// skynetweb (so .skynet still resolves through the same
		// browser proxy entry) and skynetweb to --upstream. With
		// --no-chain or only-one, point that resolver directly at
		// --upstream (or clear it if the flag's empty).
		switch {
		case wantDmsg && wantSkynet && !resolverNoChain:
			if err := rpcClient.SetEmbeddedProxyUpstream("dmsg", fmt.Sprintf("127.0.0.1:%d", skynetwebSOCKSPort)); err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("chain dmsg → skynet: %w", err))
			}
			if err := rpcClient.SetEmbeddedProxyUpstream("skynet", resolverUpstream); err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("set skynet upstream: %w", err))
			}
		case wantDmsg:
			if err := rpcClient.SetEmbeddedProxyUpstream("dmsg", resolverUpstream); err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("set dmsg upstream: %w", err))
			}
		case wantSkynet:
			if err := rpcClient.SetEmbeddedProxyUpstream("skynet", resolverUpstream); err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("set skynet upstream: %w", err))
			}
		}

		fmt.Println("Resolver up.")
		if resolverUpstream != "" {
			fmt.Printf("  upstream: %s\n", resolverUpstream)
		}
		if wantDmsg && wantSkynet && !resolverNoChain {
			fmt.Printf("  chain:    127.0.0.1:%d (dmsg) → 127.0.0.1:%d (skynet) → %s\n",
				dmsgwebSOCKSPort, skynetwebSOCKSPort, dashIfEmpty(resolverUpstream))
		}
	},
}

var downCmd = &cobra.Command{
	Use:   "down",
	Short: "Disable the embedded resolving proxies",
	Long: `Disable both resolvers. Use --dmsg-only or --skynet-only to
turn off just one.`,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		wantDmsg := !resolverSkynetOnly
		wantSkynet := !resolverDmsgOnly
		if wantDmsg {
			if err := rpcClient.SetEmbeddedProxyEnabled("dmsg", false); err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("disable dmsg resolver: %w", err))
			}
		}
		if wantSkynet {
			if err := rpcClient.SetEmbeddedProxyEnabled("skynet", false); err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("disable skynet resolver: %w", err))
			}
		}
		fmt.Println("Resolver down.")
	},
}

func printResolverStatus(s *visor.EmbeddedProxiesStatus) {
	if s == nil || (s.DmsgWeb == nil && s.SkynetWeb == nil) {
		fmt.Println("(no embedded resolvers configured)")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "name\tenabled\trunning\tdomain\tsocks\tweb\tupstream\trequests\tactive\tfailures") //nolint:errcheck,gosec
	fmt.Fprintln(w, "----\t-------\t-------\t------\t-----\t---\t--------\t--------\t------\t--------") //nolint:errcheck,gosec
	row := func(name string, p *visor.EmbeddedProxyInfo) {
		if p == nil {
			return
		}
		var total, active uint64
		var failed uint64
		if p.Stats != nil {
			total = p.Stats.TotalRequests
			active = uint64(p.Stats.Active) //nolint:gosec
			failed = p.Stats.Failed
		}
		fmt.Fprintf(w, "%s\t%v\t%v\t%s\t%s\t%s\t%s\t%d\t%d\t%d\n", //nolint:errcheck,gosec
			name, p.Enabled, p.Running,
			dashIfEmpty(p.DomainSuffix),
			dashIfEmpty(p.SocksAddr),
			dashIfEmpty(p.WebAddr),
			dashIfEmpty(p.UpstreamSOCKS),
			total, active, failed)
	}
	row("dmsgweb", s.DmsgWeb)
	row("skynetweb", s.SkynetWeb)
	_ = w.Flush() //nolint:errcheck,gosec

	if s.DmsgWeb != nil && s.DmsgWeb.Stats != nil && s.DmsgWeb.Stats.LastError != "" {
		fmt.Printf("\ndmsgweb last error: %s\n", s.DmsgWeb.Stats.LastError)
	}
	if s.SkynetWeb != nil && s.SkynetWeb.Stats != nil && s.SkynetWeb.Stats.LastError != "" {
		fmt.Printf("\nskynetweb last error: %s\n", s.SkynetWeb.Stats.LastError)
	}
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
