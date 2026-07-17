// Package cliptyfs cmd/skywire-cli/commands/ptyfs/ptyfs.go c4-vis-cli
// `skywire cli pty fs`: mount a peer visor's filesystem over the
// skywire pty subsystem. Like the sshfs tool, but there is no SSH —
// the transport is a noise-XK connection keyed to public keys.
//
// Rides the same direct-TCP pty-host entry point as `skywire cli pty
// shell` (XK noise handshake pinning the server PK, dmsgpty_whitelist
// gating the client PK), but instead of dispatching the pty subsystem
// it dispatches the sftp subsystem (SftpURI). The resulting noise-
// protected stream feeds github.com/pkg/sftp's client, which in turn
// backs a FUSE mount via github.com/hanwen/go-fuse/v2.
//
// Platform note: FUSE is Linux-only here. On other platforms the
// `mount` subcommand is registered as a stub that prints a clear
// "Linux only" error rather than failing to compile; the rest of the
// command surface (help text, PK parsing) is shared. Helpers that
// only the linux mount path uses live in identity_linux.go.
package cliptyfs

import (
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// Flags shared across subcommands. These have to live in the
// all-platforms file because init() registers them on RootCmd
// regardless of OS, and the linux-only mount path consumes them.
var (
	ptyfsSK      cipher.SecKey
	ptyfsNoVisor bool
	ptyfsDefPort string
)

// RootCmd is `skywire cli pty fs`. Subcommands are registered per
// platform: mount + umount on Linux, stubs elsewhere (see
// mount_linux.go / mount_other.go).
var RootCmd = &cobra.Command{
	Use:   "fs",
	Short: "Mount a peer visor's filesystem over the pty subsystem (sftp + FUSE)",
	Long: `skywire cli pty fs — mount a peer visor's filesystem over the skywire
pty subsystem's sftp channel. Like the sshfs tool, but the transport
is a noise-XK connection keyed to public keys (no SSH).

Same trust model as 'skywire cli pty shell':

  host-key check  -> noise XK pins the server PK from the
                     destination URL
  authorized keys -> dmsgpty whitelist on the server side
  client auth     -> client PK alone, derived from the local visor's
                     SK by default (override with --sk or
                     --no-visor-key)

Linux-only — FUSE is required, and the host process must be allowed
to mount via FUSE (typically by group membership or a passwordless
fusermount).

Examples:
  # Mount a peer's root over its direct-TCP pty-host endpoint
  skywire cli pty fs mount 0323272a60895f56aad82cb767fb5c413807adcf7c9fb0578b1b1c5807c7f29d4c@1.2.3.4:2022 ~/mnt/peer

  # Unmount (calls fusermount -u internally)
  skywire cli pty fs umount ~/mnt/peer`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
}

func init() {
	RootCmd.PersistentFlags().SortFlags = false
	RootCmd.PersistentFlags().VarP(&ptyfsSK, "sk", "s",
		"local client SK for the noise handshake (random if unset; pin for stable whitelist authorization)")
	RootCmd.PersistentFlags().BoolVar(&ptyfsNoVisor, "no-visor-key", false,
		"don't borrow the local visor's SK from "+visorconfig.SkywireConfig()+" — use --sk or a random one instead")
	RootCmd.PersistentFlags().StringVarP(&ptyfsDefPort, "port", "p", "2022",
		"default port when the destination omits one (e.g. '<pk>@host' resolves to <pk>@host:<port>)")
}
