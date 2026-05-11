// Package cliserve cmd/skywire-cli/commands/serve/serve_port.go
//
// `skywire cli serve` — register a localhost port to be served on the
// network over .skynet / .dmsg. Replaces the older `skywire cli
// skynet port {add,ls,rm}` tree:
//
//	skynet port ls          →  serve
//	skynet port add <p> --proxy-addr <a>  →  serve add <p> --to <a>
//	skynet port add <p> --local-port <l>  →  serve add <p> --to <l>
//	skynet port rm <p>      →  serve rm <p>
//
// The single --to flag accepts either a full host:port (`127.0.0.1:9883`,
// `192.168.1.20:5432`) or just a port (`9883`, treated as `localhost:9883`).
// The visor side chooses HTTP reverse-proxy vs raw TCP forwarding based
// on the network port (port 80 → reverse proxy through the dmsghttp
// logserver; everything else → raw TCP via the skynet/dmsg forwarders).
// On non-port-80 targets, host:port targets that resolve elsewhere on the
// network forward there too — useful for exposing a LAN service via skynet
// without running it on the visor host.
//
// The previous static-file helper is retained at `cli util serve`.
package cliserve

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
	serveTo           string
	serveLabel        string
	serveDesc         string
	serveWhitelist    string
	serveSkynet       bool
	serveDmsg         bool
	serveLanding      bool
	servePreserveHost bool
	serveJSON         bool
)

func init() {
	servePortCmd.Flags().StringVar(&serveTo, "to", "", "local target — host:port (e.g. 127.0.0.1:9883) or just a port (9883 → localhost:9883)")
	servePortCmd.Flags().StringVarP(&serveLabel, "label", "l", "", "label shown on the visor landing page")
	servePortCmd.Flags().StringVarP(&serveDesc, "desc", "d", "", "description shown on the landing page")
	servePortCmd.Flags().BoolVar(&serveSkynet, "skynet", true, "expose over skynet (sky-forwarding server)")
	servePortCmd.Flags().BoolVar(&serveDmsg, "dmsg", true, "expose over DMSG")
	servePortCmd.Flags().BoolVar(&serveLanding, "landing", true, "show on the visor landing page")
	servePortCmd.Flags().BoolVar(&servePreserveHost, "preserve-host", false,
		"for port-80 HTTP reverse-proxy: keep the original Host header instead of rewriting "+
			"to --to target. Use when the backend (Caddy / nginx / traefik) dispatches "+
			"its virtual hosts by Host (e.g. paired with the resolver's subdomain rewrite)")
	servePortCmd.Flags().StringVar(&serveWhitelist, "whitelist", "", "comma-separated PKs allowed to access this port (empty = allow all)")

	servePortLsCmd.Flags().BoolVar(&serveJSON, "json", false, "emit raw JSON")
	RootCmd.Flags().BoolVar(&serveJSON, "json", false, "emit raw JSON")

	RootCmd.AddCommand(servePortCmd, servePortRmCmd, servePortLsCmd, serveWhitelistCmd)
}

// RootCmd is the `serve` command tree. The bare command (no
// subcommand) lists registered ports — same data as `serve ls`.
var RootCmd = &cobra.Command{
	Use:   "serve",
	Short: "Expose localhost ports over .skynet / .dmsg",
	Long: `Register a localhost port to be served on the visor's
.skynet and .dmsg network face. Without arguments, prints the table
of currently-registered ports.

Examples:
  skywire cli serve                                         # list (default)
  skywire cli serve add 8080 --to 8080                      # localhost:8080 → <pk>.skynet:8080
  skywire cli serve add 80 --to 127.0.0.1:9883 --label "site" # reverse-proxy port 80 to a local HTTP service
  skywire cli serve rm 8080                                 # unregister

Note:
  Port 80 is owned by the visor's dmsghttp landing-page server.
  --to N on port 80 wires up an HTTP reverse-proxy to localhost:N
  through the landing-page handler (visor's built-in routes still win).
  For other ports, --to may be a port (localhost target) OR a full
  host:port (e.g. 192.168.1.20:5432) to forward to a service elsewhere
  on the LAN.

The previous "skynet port ..." commands still work (deprecated).
The static-file helper that used to be at "cli serve <dir>" moved
to "cli util serve <dir>".`,
	Run: func(cmd *cobra.Command, _ []string) {
		listForwardedPorts(cmd)
	},
}

// servePortLsCmd is the explicit `serve ls`. Bare `serve` already
// lists; this is an explicit alias for muscle memory and scripts.
var servePortLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List registered ports (same as bare `serve`)",
	Run: func(cmd *cobra.Command, _ []string) {
		listForwardedPorts(cmd)
	},
}

var servePortCmd = &cobra.Command{
	Use:   "add <port>",
	Short: "Register a forwarded port",
	Long: `Expose a target over the visor's .skynet / .dmsg
network face on <port>. Target may be on this host (localhost) or
elsewhere on the LAN.

Examples:
  serve add 8080 --to 8080                        # localhost:8080
  serve add 80 --to 127.0.0.1:9883                # reverse-proxy port 80 to local HTTP service
  serve add 3000 --to 9000 --label "blog"
  serve add 5432 --to 192.168.1.20:5432           # forward to a LAN service
  serve add 8443 --to nas.local:443 --landing=false`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		port, err := strconv.Atoi(args[0])
		if err != nil || port < 1 || port > 65535 {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid port: %s", args[0]))
		}

		fp := visor.ForwardedPort{
			Port:          port,
			Label:         serveLabel,
			Description:   serveDesc,
			Skynet:        serveSkynet,
			DMSG:          serveDmsg,
			ShowOnLanding: serveLanding,
			PreserveHost:  servePreserveHost,
		}

		// Resolve --to into the right field. The visor schema has two
		// targets: ProxyAddr (an arbitrary host:port — port 80 uses the
		// HTTP reverse-proxy on the logserver, every other port uses
		// raw TCP) and LocalPort (legacy localhost-only short form).
		// --to accepts either "host:port" or just a port. We use
		// ProxyAddr whenever the user specifies a host, and LocalPort
		// when only a port is given.
		if serveTo != "" {
			host, p, err := splitTo(serveTo)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("--to: %w", err))
			}
			switch {
			case port == 80:
				if host == "" {
					host = "127.0.0.1"
				}
				fp.ProxyAddr = fmt.Sprintf("%s:%d", host, p)
			case host == "" || host == "localhost" || host == "127.0.0.1":
				fp.LocalPort = p
			default:
				fp.ProxyAddr = fmt.Sprintf("%s:%d", host, p)
			}
		}

		if serveWhitelist != "" {
			var wl []cipher.PubKey
			for _, pkStr := range strings.Split(serveWhitelist, ",") {
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
			fp.Whitelist = wl
		}

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if err := rpcClient.RegisterForwardedPort(fp); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		var msg strings.Builder
		fmt.Fprintf(&msg, "Serving port %d", port)
		if serveTo != "" {
			fmt.Fprintf(&msg, " → %s", serveTo)
		}
		if serveLabel != "" {
			fmt.Fprintf(&msg, " (%s)", serveLabel)
		}
		msg.WriteString("\n")
		internal.PrintOutput(cmd.Flags(), fp, msg.String())
	},
}

// serveWhitelistCmd updates a registered port's PK whitelist in
// place via UpdateForwardedPort, preserving every other field
// (label, description, skynet/dmsg toggles, landing-page visibility).
// Without this, changing a whitelist would require remove + re-add,
// which interrupts active connections and loses metadata.
var serveWhitelistCmd = &cobra.Command{
	Use:   "whitelist <port> <pks-or-clear>",
	Short: "Set/replace/clear the PK whitelist on an already-registered port",
	Long: `Replace the PK whitelist for a registered forwarded port.

The second argument is either a comma-separated list of public keys
(replaces the whitelist with exactly those keys) or the literal word
"clear" / "none" (removes the whitelist, returning the port to "open
to all authenticated peers").

Examples:
  serve whitelist 8080 02abc...,03def...     # restrict to two PKs
  serve whitelist 8080 clear                 # remove the whitelist
  serve whitelist 80 02abc...                # restrict the website on port 80`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		port, err := strconv.Atoi(args[0])
		if err != nil || port < 1 || port > 65535 {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid port: %s", args[0]))
		}

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		// Fetch the current entry so we update in place rather than
		// reset every other field. Update with a partial would clobber
		// label/desc/skynet/dmsg/landing-page metadata.
		ports, err := rpcClient.ListForwardedPorts()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		var fp *visor.ForwardedPort
		for i := range ports {
			if ports[i].Port == port {
				fp = &ports[i]
				break
			}
		}
		if fp == nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("port %d is not registered", port))
		}

		spec := strings.TrimSpace(args[1])
		switch strings.ToLower(spec) {
		case "clear", "none", "":
			fp.Whitelist = nil
		default:
			var wl []cipher.PubKey
			for _, pkStr := range strings.Split(spec, ",") {
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
			fp.Whitelist = wl
		}

		if err := rpcClient.UpdateForwardedPort(*fp); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		var msg strings.Builder
		if len(fp.Whitelist) == 0 {
			fmt.Fprintf(&msg, "Port %d whitelist cleared (open to all peers)\n", port)
		} else {
			fmt.Fprintf(&msg, "Port %d whitelist set to %d PK(s):\n", port, len(fp.Whitelist))
			for _, pk := range fp.Whitelist {
				fmt.Fprintf(&msg, "  %s\n", pk.Hex())
			}
		}
		internal.PrintOutput(cmd.Flags(), fp, msg.String())
	},
}

var servePortRmCmd = &cobra.Command{
	Use:   "rm <port>",
	Short: "Unregister a forwarded port",
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
		internal.PrintOutput(cmd.Flags(), map[string]any{"port": port, "removed": true},
			fmt.Sprintf("Port %d removed\n", port))
	},
}

// splitTo accepts either "host:port" or "port" (where the bare port
// resolves to localhost). Returns the parsed host (may be empty when
// the input was port-only) and port.
func splitTo(s string) (string, int, error) {
	if !strings.Contains(s, ":") {
		p, err := strconv.Atoi(s)
		if err != nil || p < 1 || p > 65535 {
			return "", 0, fmt.Errorf("expected host:port or a numeric port, got %q", s)
		}
		return "", p, nil
	}
	host, portStr, err := splitHostPort(s)
	if err != nil {
		return "", 0, err
	}
	p, err := strconv.Atoi(portStr)
	if err != nil || p < 1 || p > 65535 {
		return "", 0, fmt.Errorf("invalid port in %q", s)
	}
	return host, p, nil
}

func splitHostPort(s string) (string, string, error) {
	idx := strings.LastIndex(s, ":")
	if idx < 0 {
		return "", "", fmt.Errorf("expected host:port, got %q", s)
	}
	return s[:idx], s[idx+1:], nil
}

func listForwardedPorts(cmd *cobra.Command) {
	rpcClient, err := clirpc.Client(cmd.Flags())
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}
	ports, err := rpcClient.ListForwardedPorts()
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}
	if len(ports) == 0 {
		internal.PrintOutput(cmd.Flags(), []visor.ForwardedPort{}, "No registered ports.\n")
		return
	}
	var buf strings.Builder
	tw := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PORT\tTO\tLABEL\tSKYNET\tDMSG\tLANDING\tWHITELIST\tDESCRIPTION") //nolint:errcheck,gosec
	for _, p := range ports {
		to := p.ProxyAddr
		if to == "" {
			to = fmt.Sprintf("localhost:%d", p.EffectiveLocalPort())
		}
		wl := "open"
		if n := len(p.Whitelist); n > 0 {
			wl = fmt.Sprintf("%d PK(s)", n)
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%v\t%v\t%v\t%s\t%s\n", //nolint:errcheck,gosec
			p.Port, to,
			dashIfEmpty(p.Label),
			p.Skynet, p.DMSG, p.ShowOnLanding,
			wl,
			dashIfEmpty(p.Description))
	}
	tw.Flush() //nolint:errcheck,gosec
	internal.PrintOutput(cmd.Flags(), ports, buf.String())
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
