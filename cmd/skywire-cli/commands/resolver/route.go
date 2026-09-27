// Package cliresolver cmd/skywire-cli/commands/resolver/route.go c4-vis-cli
//
// `skywire cli resolver route` — per-domain upstreams for the resolving
// proxies. Both primaries get the same rules, so the answer does not depend on
// which layer of the dmsgweb → skynetweb chain a request leaves from.
package cliresolver

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/proxyroute"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

func init() {
	routeCmd.AddCommand(routeAddCmd, routeRmCmd)
	RootCmd.AddCommand(routeCmd)
}

var routeCmd = &cobra.Command{
	Use:   "route",
	Short: "Per-domain upstreams: send chosen domains through another exit",
	Long: `List the per-domain upstream rules of the resolving proxies.

A rule sends a domain and its subdomains to its own upstream SOCKS5 proxy, or
"direct", instead of the default upstream. The longest matching suffix wins.
Rules apply live and are saved to the config.

A second exit is a second skysocks-client instance on its own port:

  skywire cli visor app add skysocks-client-2 skysocks-client
  skywire cli visor app args skysocks-client-2 "app skysocks-client --addr 127.0.0.1:1081"
  skywire cli visor app pk skysocks-client-2 <exit-pk>
  skywire cli visor app start skysocks-client-2
  skywire cli resolver route add example.com 127.0.0.1:1081
  skywire cli resolver route add lan.example direct
  skywire cli resolver route rm example.com

http://status.skywire lists each skysocks-client instance and its route.`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, _ []string) {
		rules := currentRules(cmd)
		internal.PrintOutput(cmd.Flags(), rules, renderRules(rules))
	},
}

var routeAddCmd = &cobra.Command{
	Use:   "add <domain> <socks5-addr|direct>",
	Short: "Send a domain and its subdomains to an upstream",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		r, err := proxyroute.ParseRule(args[0] + "=" + args[1])
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		rules := append(currentRules(cmd), r)
		rules = writeRules(cmd, rules)
		internal.PrintOutput(cmd.Flags(), rules, renderRules(rules))
	},
}

var routeRmCmd = &cobra.Command{
	Use:   "rm <domain>",
	Short: "Remove a domain's rule",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		suffix := strings.ToLower(strings.Trim(args[0], "."))
		var kept []proxyroute.Rule
		found := false
		for _, r := range currentRules(cmd) {
			if r.Suffix == suffix {
				found = true
				continue
			}
			kept = append(kept, r)
		}
		if !found {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("no rule for %q", suffix))
		}
		kept = writeRules(cmd, kept)
		internal.PrintOutput(cmd.Flags(), kept, renderRules(kept))
	},
}

func rpcClientFor(cmd *cobra.Command) visorapi.API {
	rpcClient, err := clirpc.Client(cmd.Flags())
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}
	return rpcClient
}

// currentRules reads the rules from the .dmsg resolver, falling back to the
// .skynet one when dmsg_web is absent.
func currentRules(cmd *cobra.Command) []proxyroute.Rule {
	status, err := rpcClientFor(cmd).EmbeddedProxies()
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), fmt.Errorf("EmbeddedProxies RPC failed: %w", err))
	}
	if status.DmsgWeb != nil && len(status.DmsgWeb.UpstreamRules) > 0 {
		return status.DmsgWeb.UpstreamRules
	}
	if status.SkynetWeb != nil {
		return status.SkynetWeb.UpstreamRules
	}
	return nil
}

// writeRules sets the same rules on both resolvers in one call.
func writeRules(cmd *cobra.Command, rules []proxyroute.Rule) []proxyroute.Rule {
	rules, err := proxyroute.Normalize(rules)
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}
	raw, err := json.Marshal(rules)
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}
	if len(rules) == 0 {
		raw = []byte("null")
	}
	if _, err := rpcClientFor(cmd).SetConfigFields(map[string]json.RawMessage{
		"dmsg_web.upstream_rules":   raw,
		"skynet_web.upstream_rules": raw,
	}); err != nil {
		internal.PrintFatalError(cmd.Flags(), fmt.Errorf("SetConfigFields failed: %w", err))
	}
	return rules
}

func renderRules(rules []proxyroute.Rule) string {
	if len(rules) == 0 {
		return "no per-domain rules; everything uses the default upstream\n"
	}
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "DOMAIN\tUPSTREAM") //nolint:errcheck
	for _, r := range rules {
		_, _ = fmt.Fprintf(w, "%s\t%s\n", r.Suffix, r.Upstream) //nolint:errcheck
	}
	_ = w.Flush() //nolint:errcheck
	return b.String()
}
