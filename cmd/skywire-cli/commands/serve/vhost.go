// Package cliserve cmd/skywire-cli/commands/serve/vhost.go c4-vis-cli
package cliserve

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

var (
	vhostTo           string
	vhostAliases      []string
	vhostWhitelist    string
	vhostPreserveHost bool
	vhostVisorPages   bool
	vhostSites        []string
)

func init() {
	vhostAddCmd.Flags().StringVar(&vhostTo, "to", "", "local service: host:port, or a port on 127.0.0.1")
	vhostAddCmd.Flags().StringSliceVar(&vhostAliases, "alias", nil, "another Host name for the site, e.g. status.skywire.dev (repeatable)")
	vhostAddCmd.Flags().StringVar(&vhostWhitelist, "whitelist", "", "comma-separated PKs allowed to reach the site (empty = everyone)")
	vhostAddCmd.Flags().BoolVar(&vhostPreserveHost, "preserve-host", false, "send the name the site was asked by (e.g. status.skywire.dev) as the Host, not the service's address")
	vhostAddCmd.Flags().BoolVar(&vhostVisorPages, "visor-pages", false, "serve the visor's own pages, its landing page at /, under this name instead of --to")
	vhostAddCmd.Flags().StringSliceVar(&vhostSites, "site", nil, "a name the service serves, linked from the landing page (repeatable; a * vhost to a local Caddy lists its own)")
	vhostCmd.AddCommand(vhostAddCmd, vhostRmCmd, vhostLsCmd)
	RootCmd.AddCommand(vhostCmd)
}

var vhostCmd = &cobra.Command{
	Use:   "vhost",
	Short: "Serve local sites on port 80 by the request's Host",
	Long: "A vhost is a local service the visor serves on its port 80 for a Host name,\n" +
		"without Caddy: http://status.<pk>.skynet/ reaches the vhost named status, and\n" +
		"http://status.skywire.dev.<pk>.skynet/ the one with that alias, as the resolving\n" +
		"proxy rewrites the Host. The service can listen on localhost only. A site gets\n" +
		"every path; the visor's own pages stay on its bare name. The vhost named * takes\n" +
		"every name no other vhost has, so a web server can choose the site itself. The\n" +
		"bare name's own website is `serve add 80 --to ...`. Bare `vhost` lists them.",
	Example: `  serve vhost add status --to 8092
  serve vhost add '*' --to 80 --preserve-host   # every other name to Caddy
  serve add 80 --to 8080                         # the bare <pk>.skynet's own site
  serve vhost add landing --visor-pages          # this visor's own page at landing.<pk>.skynet`,
	Run: func(cmd *cobra.Command, _ []string) { listVhosts(cmd) },
}

var vhostLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List the vhosts",
	Run:   func(cmd *cobra.Command, _ []string) { listVhosts(cmd) },
}

var vhostAddCmd = &cobra.Command{
	Use:   "add <name|*>",
	Short: "Serve a local service for a Host name, replacing one of that name",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := strings.ToLower(args[0])
		if (vhostTo == "") == !vhostVisorPages {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("give one of --to and --visor-pages"))
		}
		if vhostVisorPages && name == "*" {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("--visor-pages needs a name, not *"))
		}
		var proxyAddr string
		if !vhostVisorPages {
			host, p, err := splitTo(vhostTo)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("--to: %w", err))
			}
			if host == "" {
				host = "127.0.0.1"
			}
			proxyAddr = fmt.Sprintf("%s:%d", host, p)
		}
		wl, err := parsePKs(vhostWhitelist)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		vh := visorapi.Vhost{
			Name:         name,
			Aliases:      vhostAliases,
			ProxyAddr:    proxyAddr,
			Whitelist:    wl,
			PreserveHost: vhostPreserveHost,
			VisorPages:   vhostVisorPages,
			Sites:        vhostSites,
		}
		updateVhosts(cmd, func(vs []visorapi.Vhost) ([]visorapi.Vhost, error) {
			var out []visorapi.Vhost
			for _, v := range vs {
				if v.Name == name {
					continue
				}
				for _, n := range v.Names() {
					for _, mine := range vh.Names() {
						if n == mine {
							return nil, fmt.Errorf("%s is already a name of vhost %s", n, v.Name)
						}
					}
				}
				out = append(out, v)
			}
			return append(out, vh), nil
		})
		internal.PrintOutput(cmd.Flags(), vh, fmt.Sprintf("vhost %s -> %s\n", name, vhostTarget(vh)))
	},
}

var vhostRmCmd = &cobra.Command{
	Use:   "rm <name>",
	Short: "Stop serving a vhost",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := strings.ToLower(args[0])
		updateVhosts(cmd, func(vs []visorapi.Vhost) ([]visorapi.Vhost, error) {
			var out []visorapi.Vhost
			for _, v := range vs {
				if v.Name != name {
					out = append(out, v)
				}
			}
			if len(out) == len(vs) {
				return nil, fmt.Errorf("no vhost named %s", name)
			}
			return out, nil
		})
		internal.PrintOutput(cmd.Flags(), name, "vhost "+name+" removed\n")
	},
}

// updateVhosts applies change to the vhosts of the port 80 entry, creating
// an entry that only holds vhosts when there is none.
func updateVhosts(cmd *cobra.Command, change func([]visorapi.Vhost) ([]visorapi.Vhost, error)) {
	rpcClient, err := clirpc.Client(cmd.Flags())
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}
	ports, err := rpcClient.ListForwardedPorts()
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}
	var fp *visorapi.ForwardedPort
	for i := range ports {
		if ports[i].Port == 80 {
			fp = &ports[i]
		}
	}
	isNew := fp == nil
	if isNew {
		fp = &visorapi.ForwardedPort{Port: 80}
	}
	if fp.Vhosts, err = change(fp.Vhosts); err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}
	if isNew {
		err = rpcClient.RegisterForwardedPort(*fp)
	} else {
		err = rpcClient.UpdateForwardedPort(*fp)
	}
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}
}

func listVhosts(cmd *cobra.Command) {
	rpcClient, err := clirpc.Client(cmd.Flags())
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}
	ports, err := rpcClient.ListForwardedPorts()
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}
	vs := []visorapi.Vhost{}
	for _, p := range ports {
		if p.Port == 80 {
			vs = append(vs, p.Vhosts...)
		}
	}
	if len(vs) == 0 {
		internal.PrintOutput(cmd.Flags(), vs, "No vhosts.\n")
		return
	}
	var buf strings.Builder
	tw := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tALIASES\tTO\tWHITELIST") //nolint:errcheck,gosec
	for _, v := range vs {
		wl := "open"
		if n := len(v.Whitelist); n > 0 {
			wl = fmt.Sprintf("%d PK(s)", n)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", v.Name, dashIfEmpty(strings.Join(v.Aliases, ",")), vhostTarget(v), wl) //nolint:errcheck,gosec
	}
	tw.Flush() //nolint:errcheck,gosec
	internal.PrintOutput(cmd.Flags(), vs, buf.String())
}

// parsePKs reads a comma-separated list of public keys; empty is none.
func parsePKs(s string) ([]cipher.PubKey, error) {
	var out []cipher.PubKey
	for _, f := range strings.Split(s, ",") {
		if f = strings.TrimSpace(f); f == "" {
			continue
		}
		var pk cipher.PubKey
		if err := pk.Set(f); err != nil {
			return nil, fmt.Errorf("invalid whitelist PK %q: %w", f, err)
		}
		out = append(out, pk)
	}
	return out, nil
}

// kept80 is what the port 80 entry keeps across `serve add 80` and `serve rm
// 80`, its vhosts and HideSites, or nothing for another port.
func kept80(cmd *cobra.Command, rpcClient visorapi.API, port int) visorapi.ForwardedPort {
	if port != 80 {
		return visorapi.ForwardedPort{}
	}
	ports, err := rpcClient.ListForwardedPorts()
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}
	for _, p := range ports {
		if p.Port == 80 {
			return visorapi.ForwardedPort{Vhosts: p.Vhosts, HideSites: p.HideSites}
		}
	}
	return visorapi.ForwardedPort{}
}

// vhostTarget is where a vhost's requests go, as the listing shows it.
func vhostTarget(v visorapi.Vhost) string {
	if v.VisorPages {
		return "landing page"
	}
	return v.ProxyAddr
}
