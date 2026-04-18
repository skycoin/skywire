// Package clivisor cmd/skywire-cli/commands/visor/proxies.go
//
// `skywire cli visor proxies` — status command for the visor-hosted
// resolving proxies (dmsgweb, skynetweb). Two subcommands:
//
//	proxies          status + stats table (or --json)
//	proxies set      toggle a resolver on/off at runtime
//
// No enable/disable is persisted to disk — runtime state only. That
// keeps the CLI useful for "try this setting" experiments without
// touching the on-disk config.
package clivisor

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

var proxiesJSON bool

func init() {
	proxiesCmd.Flags().BoolVar(&proxiesJSON, "json", false, "emit raw JSON")
	proxiesCmd.AddCommand(proxiesSetCmd)
	proxiesCmd.AddCommand(proxiesUpstreamCmd)
	RootCmd.AddCommand(proxiesCmd)
}

var proxiesCmd = &cobra.Command{
	Use:   "proxies",
	Short: "Show embedded resolving-proxy status (dmsgweb, skynetweb)",
	Long: `Print the runtime state + cumulative stats of the visor-hosted
.dmsg / .skynet resolving proxies. Copy SocksAddr into a browser's
SOCKS5 setting to browse the matched domain; paste WebAddr into curl
for a direct smoke test.

Use "proxies set <kind> on|off" to toggle a resolver at runtime
without editing the config file.`,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		status, err := rpcClient.EmbeddedProxies()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("EmbeddedProxies RPC failed: %w", err))
		}
		if proxiesJSON {
			_ = json.NewEncoder(os.Stdout).Encode(status) //nolint:errcheck,gosec
			return
		}
		printProxies(status)
	},
}

var proxiesSetCmd = &cobra.Command{
	Use:   "set <dmsg|skynet> <on|off>",
	Short: "Toggle a resolving proxy on or off at runtime",
	Long: `Toggle the runtime state of an embedded resolving proxy.

Runtime-only: the on-disk config is unchanged, so a visor restart
reverts to the config's 'enable' flag. Use this to experiment with
"what if dmsgweb were on?" without committing to a config edit.`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		kind := args[0]
		var enable bool
		switch args[1] {
		case "on", "true", "enable", "enabled", "1":
			enable = true
		case "off", "false", "disable", "disabled", "0":
			enable = false
		default:
			internal.PrintFatalError(cmd.Flags(),
				fmt.Errorf("second argument must be on|off, got %q", args[1]))
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if err := rpcClient.SetEmbeddedProxyEnabled(kind, enable); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("SetEmbeddedProxyEnabled failed: %w", err))
		}
		state := "disabled"
		if enable {
			state = "enabled"
		}
		fmt.Printf("%s resolver %s\n", kind, state)
	},
}

var proxiesUpstreamCmd = &cobra.Command{
	Use:   "upstream <dmsg|skynet> <socks5-addr|clear>",
	Short: "Set the upstream SOCKS5 proxy for a resolver",
	Long: `Change the upstream SOCKS5 address at runtime. Non-matching
domains are forwarded to this upstream instead of connecting direct.

Use "clear" or "" to remove the upstream (direct connect).

Example chain: browser → dmsgweb (.dmsg) → skynetweb (.skynet) → skysocks (everything else)

  skywire cli visor proxies upstream skynet 127.0.0.1:1080
  skywire cli visor proxies upstream dmsg 127.0.0.1:4446`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		kind := args[0]
		addr := args[1]
		if addr == "clear" || addr == "none" || addr == "direct" {
			addr = ""
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if err := rpcClient.SetEmbeddedProxyUpstream(kind, addr); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("SetEmbeddedProxyUpstream failed: %w", err))
		}
		if addr == "" {
			fmt.Printf("%s resolver upstream cleared (direct connect)\n", kind)
		} else {
			fmt.Printf("%s resolver upstream set to %s\n", kind, addr)
		}
	},
}

func printProxies(s *visor.EmbeddedProxiesStatus) {
	if s == nil || (s.DmsgWeb == nil && s.SkynetWeb == nil) {
		fmt.Println("(no embedded proxies configured)")
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

	// Last-error blurb below the table when any resolver has one —
	// helpful for quick triage without going to --json.
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
