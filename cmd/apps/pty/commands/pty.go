// Package commands cmd/apps/pty/commands/pty.go — the unified
// `skywire app pty <mode>` command tree.
//
// The pty (pseudoterminal) subsystem has four operational modes:
//
//   - visor   visor-hosted Internal app. Started by the launcher;
//             not exposed here. (See RFC #2775 Phase 3.3.)
//   - dmsg    standalone process with a dmsg listener (own keys,
//             own dmsg client). Can also enable a TCP listener
//             via --tcp-listen; --no-dmsg makes it TCP-only.
//   - tcp     standalone TCP-only (ssh equivalent). Convenience
//             wrapper that forces --no-dmsg on `dmsg`; --addr is
//             required and seeds --tcp-listen.
//   - http    HTTP/WebSocket bridge — serves a real PTY in a
//             browser via the dmsgpty.UI machinery. Bridges to a
//             running mode-1/2/3 pty server via --hnet + --haddr.
//
// Plus an `exec` subcommand that dials a remote pty server and
// runs a command (the old `dmsgpty-cli` flow), with `whitelist`
// management subcommands underneath it.
//
// Code layout: each mode mounts the existing dmsgpty-host /
// dmsgpty-ui / dmsgpty-cli cobra command from cmd/dmsg/ as a
// subcommand under this tree, with the Use field renamed. The
// dmsg subcommand tree (`skywire dmsg pty *`) no longer mounts
// these — that path moved to `skywire app pty *`. The package-
// level code identifiers (`pkg/dmsg/dmsgpty/`, `conf.Dmsgpty`,
// etc.) are unchanged; that deeper rename is a follow-up PR.
package commands

import (
	"fmt"
	"log"

	"github.com/spf13/cobra"

	dpc "github.com/skycoin/skywire/cmd/dmsg/dmsgpty-cli/commands"
	dph "github.com/skycoin/skywire/cmd/dmsg/dmsgpty-host/commands"
	dpu "github.com/skycoin/skywire/cmd/dmsg/dmsgpty-ui/commands"
)

// RootCmd is the `app pty` command. Mounted onto the top-level
// appsCmd by cmd/skywire/commands/root.go.
var RootCmd = &cobra.Command{
	Use:   "pty",
	Short: "Pseudoterminal (interactive shell) server and client",
	Long: `Pseudoterminal (pty) — interactive shell over a network.

The same pty subsystem runs in four modes:

  visor     Visor-hosted Internal app (managed by the launcher;
            invoke via 'cli visor app start pty' once Phase 3.3
            lands, not via this command).
  dmsg      Standalone server with a dmsg listener. Pair with
            --no-dmsg + --tcp-listen for a tcp-only deployment.
  tcp       Standalone tcp-only listener (ssh equivalent). Same
            crypto as dmsg's noise layer, transported over raw
            TCP. Requires --addr.
  http      HTTP/WebSocket bridge serving a real pty in a browser.
            Connects to a running mode-1/2/3 pty server.

Auth model inherits the underlying transport's noise-XK + PK
whitelist. See 'skywire app pty <mode> --help' for per-mode flags.`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
}

func init() {
	// Mount each existing implementation under the unified tree
	// with its operator-facing mode name. The dmsg subcommand tree
	// (cmd/dmsg/dmsg/commands/root.go) no longer mounts these, so
	// each *.RootCmd has a single parent (this tree).
	dph.RootCmd.Use = "dmsg"
	dph.RootCmd.Short = "Run a standalone pty server (dmsg listener; +TCP via --tcp-listen)"

	dpu.RootCmd.Use = "http"
	dpu.RootCmd.Short = "Serve a pty over HTTP/WebSocket for browser-rendered terminal"

	dpc.RootCmd.Use = "exec"
	dpc.RootCmd.Short = "Dial a remote pty server and run a command (whitelist subcommands underneath)"

	RootCmd.AddCommand(
		dph.RootCmd, // dmsg
		newTCPCmd(), // tcp — wraps dmsg with --no-dmsg forced
		dpu.RootCmd, // http
		dpc.RootCmd, // exec
	)
}

// newTCPCmd builds the `tcp` mode wrapper. It's a thin shim over
// the dmsg-host RootCmd that forces --no-dmsg and seeds --tcp-listen
// from a required --addr. Operators get a discoverable subcommand
// for the ssh-equivalent deployment shape without having to know
// the underlying flag combination.
func newTCPCmd() *cobra.Command {
	var addr string
	cmd := &cobra.Command{
		Use:   "tcp",
		Short: "Run a standalone pty server with a TCP listener only (ssh equivalent, --no-dmsg)",
		Long: `Run the pty server in tcp-standalone mode — no dmsg client,
no dmsg-discovery entry. TCP listener with mutual-PK Noise XK
handshake gates inbound connections. The same crypto as dmsg's
noise layer, transported over raw TCP.

Equivalent to:
  skywire app pty dmsg --no-dmsg --tcp-listen <addr>

Other dmsg-host flags (--sk, --whitelist, --sk-from-visor, etc.)
are accepted by the underlying server but expressed here only
through --addr; pass them via the dmsg subcommand if you need
the full surface.`,
		SilenceErrors:         true,
		SilenceUsage:          true,
		DisableSuggestions:    true,
		DisableFlagsInUseLine: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			if addr == "" {
				return fmt.Errorf("--addr is required for tcp mode (e.g. --addr :2022)")
			}
			// Inject the equivalent flag combo on the underlying
			// dmsg-host RootCmd and re-enter its parsing. This avoids
			// duplicating the host's RunE; the trade-off is that the
			// tcp subcommand only forwards --addr, not the rest of
			// the host's flag surface.
			dph.RootCmd.SetArgs([]string{"--no-dmsg", "--tcp-listen", addr})
			return dph.RootCmd.Execute()
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "",
		"TCP bind address (required, e.g. ':2022' or '0.0.0.0:2022')")
	return cmd
}

// Execute is the standalone-binary entry point. Used by
// cmd/apps/pty/pty.go when the app is invoked as a separate
// binary; the main `skywire app pty` path goes through the
// top-level Execute in cmd/skywire/commands/root.go and doesn't
// call this.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}
