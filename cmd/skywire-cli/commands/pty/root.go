// Package clipty cmd/skywire-cli/commands/pty/root.go — `skywire cli pty`,
// the unified namespace for the skywire pty subsystem: a remote shell and a
// remote-filesystem mount, plus the host (server) side, all keyed by public
// key over a noise-XK connection (no SSH involved).
//
//	pty host    — serve the pty subsystem on a TCP port (was `cli sshd`)
//	pty shell   — open a remote shell to a peer    (was `cli ssh`)
//	pty fs      — mount a peer's filesystem         (was `cli sshfs`)
//
// The dmsg-overlay forms of the shell — `cli dmsg pty exec` / `start` — remain
// under `cli dmsg pty` for now; folding them in here (so `cli pty exec`) is a
// follow-up.
package clipty

import (
	"github.com/spf13/cobra"

	cliptyfs "github.com/skycoin/skywire/cmd/skywire-cli/commands/ptyfs"
	clissh "github.com/skycoin/skywire/cmd/skywire-cli/commands/ssh"
	clisshd "github.com/skycoin/skywire/cmd/skywire-cli/commands/sshd"
)

// RootCmd is `skywire cli pty`.
var RootCmd = &cobra.Command{
	Use:   "pty",
	Short: "Remote shell & filesystem over the skywire pty subsystem (key-addressed, noise-XK)",
	Long: `skywire cli pty — remote shell and remote-filesystem access over the
skywire pty subsystem. Every form is keyed by public key over a noise-XK
connection; there is no SSH involved (the names parallel ssh/sshd/sshfs only
by analogy).

  pty host   serve the pty subsystem on a TCP port (peers connect by PK)
  pty shell  open a remote shell to a peer
  pty fs     mount a peer's filesystem (sftp + FUSE)`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
}

func init() {
	// Re-parent the previously top-level ssh/sshd/sshfs commands as pty
	// subcommands, renamed away from the OpenSSH-shaped names.
	clisshd.RootCmd.Use = "host"
	clissh.RootCmd.Use = "shell <pk>@<host>[:<port>] [-- <command> [args...]]"
	cliptyfs.RootCmd.Use = "fs"

	RootCmd.AddCommand(
		clisshd.RootCmd,
		clissh.RootCmd,
		cliptyfs.RootCmd,
	)
}
