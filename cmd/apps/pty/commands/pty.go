// Package commands cmd/apps/pty/commands/pty.go — the unified
// `skywire app pty <mode>` command tree.
//
// The pty (pseudoterminal) subsystem has four operational modes:
//
//   - visor   visor-hosted Internal app. Started by the launcher;
//     not exposed here. (See RFC #2775 Phase 3.3.)
//   - dmsg    standalone process with a dmsg listener (own keys,
//     own dmsg client). Can also enable a TCP listener via
//     --tcp-listen; --no-dmsg makes it TCP-only.
//   - tcp     standalone TCP-only (ssh equivalent). Convenience
//     wrapper that forces --no-dmsg on `dmsg`; --addr is required
//     and seeds --tcp-listen.
//   - http    HTTP/WebSocket bridge — serves a real PTY in a
//     browser via the pty.UI machinery. Bridges to a running
//     mode-1/2/3 pty server via --hnet + --haddr.
//
// Plus an `exec` subcommand that dials a remote pty server and
// runs a command (the old `dmsgpty-cli` flow), with `whitelist`
// management subcommands underneath it.
//
// Code layout: each mode here is a **thin delegating wrapper** —
// disables flag parsing and forwards all args (including --help)
// to the underlying `cmd/dmsg/dmsgpty-{host,ui,cli}/` RootCmd via
// SetArgs + Execute. The dmsg subcommand tree continues to mount
// those RootCmds as `dmsg pty <cli|host|ui>` (the standalone dmsg
// binary needs that surface intact); the skywire-binary import
// hides the dmsg-pty group so help output funnels operators here.
// The package-level code identifiers (`pkg/dmsg/dmsgpty/`,
// `conf.Pty`, etc.) are unchanged — deeper rename is a
// follow-up PR.
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
	RootCmd.AddCommand(
		newDelegateCmd(
			"dmsg",
			"Run a standalone pty server (dmsg listener; +TCP via --tcp-listen)",
			dph.RootCmd,
		),
		newTCPCmd(),
		newDelegateCmd(
			"http",
			"Serve a pty over HTTP/WebSocket for browser-rendered terminal",
			dpu.RootCmd,
		),
		newDelegateCmd(
			"exec",
			"Dial a remote pty server and run a command (whitelist subcommands underneath)",
			dpc.RootCmd,
		),
	)
}

// newDelegateCmd builds a thin wrapper cobra.Command whose RunE
// parses flags onto the supplied target's flagset and invokes the
// target's Run/RunE directly. The wrapper avoids re-entering
// cobra's dispatcher (which would resolve os.Args back through
// itself and recurse to a stack overflow — the original shape used
// target.SetArgs+Execute and hit this bug as soon as both wrapper
// and target shared an ancestor in the cobra tree).
//
// --help intercept fires before ParseFlags so the target's own
// help text is what operators see, not cobra's flag-help shortcut
// error path.
func newDelegateCmd(use, short string, target *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:                   use,
		Short:                 short,
		DisableFlagParsing:    true,
		SilenceErrors:         true,
		SilenceUsage:          true,
		DisableSuggestions:    true,
		DisableFlagsInUseLine: true,
		RunE: func(_ *cobra.Command, args []string) error {
			for _, a := range args {
				if a == "--help" || a == "-h" {
					return target.Help()
				}
			}
			if err := target.ParseFlags(args); err != nil {
				return err
			}
			remaining := target.Flags().Args()
			if target.RunE != nil {
				return target.RunE(target, remaining)
			}
			if target.Run != nil {
				target.Run(target, remaining)
			}
			return nil
		},
	}
}

// newTCPCmd builds the `tcp` mode wrapper. Unlike newDelegateCmd
// it parses its own flags so we can require --addr; the resulting
// flag combo (--no-dmsg + --tcp-listen <addr>) is injected onto
// the dmsg-host RootCmd. Operators get a discoverable subcommand
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
