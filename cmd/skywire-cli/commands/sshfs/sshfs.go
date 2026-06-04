// Package clisshfs cmd/skywire-cli/commands/sshfs/sshfs.go —
// `skywire cli sshfs`, the OpenSSH-sshfs-equivalent client over
// skywire identity.
//
// Rides the same direct-TCP dmsgpty entry point as `skywire cli ssh`
// (XK noise handshake pinning the server PK, dmsgpty_whitelist
// gating the client PK), but instead of dispatching the pty
// subsystem it dispatches the new sftp subsystem (SftpURI). The
// resulting noise-protected stream feeds github.com/pkg/sftp's
// client, which in turn backs a FUSE mount via
// github.com/hanwen/go-fuse/v2.
//
// Platform note: FUSE is Linux-only here. On other platforms the
// `mount` subcommand is registered as a stub that prints a clear
// "Linux only" error rather than failing to compile; the rest of the
// command surface (help text, PK parsing) is shared. Helpers that
// only the linux mount path uses live in identity_linux.go.
package clisshfs

import (
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// Flags shared across subcommands. These have to live in the
// all-platforms file because init() registers them on RootCmd
// regardless of OS, and the linux-only mount path consumes them.
var (
	sshfsSK      cipher.SecKey
	sshfsNoVisor bool
	sshfsDefPort string
)

// RootCmd is `skywire cli sshfs`. Subcommands are registered per
// platform: mount + umount on Linux, stubs elsewhere (see
// mount_linux.go / mount_other.go).
var RootCmd = &cobra.Command{
	Use:   "sshfs",
	Short: "Mount a peer visor's filesystem (sshfs-equivalent)",
	Long: `skywire cli sshfs — OpenSSH-sshfs-equivalent client over skywire identity.

Mounts a peer visor's filesystem locally over the dmsgpty sftp
subsystem. Same trust model as 'skywire cli ssh':

  ssh's host-key check  -> noise XK pins the server PK from the
                          destination URL
  authorized_keys       -> dmsgpty whitelist on the server side
  password / pubkey     -> client PK alone, derived from the local
                          visor's SK by default (override with --sk
                          or --no-visor-key)

Linux-only — FUSE is required, and the host process must be allowed
to mount via FUSE (typically by group membership or a passwordless
fusermount).

Examples:
  # Mount a peer's root over its direct-TCP dmsgpty endpoint
  skywire cli sshfs mount 0323272a60895f56aad82cb767fb5c413807adcf7c9fb0578b1b1c5807c7f29d4c@1.2.3.4:2022 ~/mnt/peer

  # Unmount (calls fusermount -u internally)
  skywire cli sshfs umount ~/mnt/peer`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
}

func init() {
	RootCmd.PersistentFlags().SortFlags = false
	RootCmd.PersistentFlags().VarP(&sshfsSK, "sk", "s",
		"local client SK for the noise handshake (random if unset; pin for stable whitelist authorization)")
	RootCmd.PersistentFlags().BoolVar(&sshfsNoVisor, "no-visor-key", false,
		"don't borrow the local visor's SK from "+visorconfig.SkywireConfig()+" — use --sk or a random one instead")
	RootCmd.PersistentFlags().StringVarP(&sshfsDefPort, "port", "p", "2022",
		"default port when the destination omits one (e.g. '<pk>@host' resolves to <pk>@host:<port>)")
}
