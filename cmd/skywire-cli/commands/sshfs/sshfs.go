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
// command surface (help text, PK parsing) is shared.
package clisshfs

import (
	"fmt"
	"os"
	"regexp"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// sshfsDestRE matches both `<pk>@<host>:<port>` and the more explicit
// `tcp://<pk>@<host>:<port>` form, identical to clissh's destination
// shape. Kept as a separate copy here so cli sshfs can stand alone if
// cli ssh is ever split out.
var sshfsDestRE = regexp.MustCompile(`^(?:tcp://)?([a-f0-9]{66})@(.+:[^:]+)$`)

// Flags shared across subcommands.
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

// parseSSHFSDestination splits `<pk>@<host>:<port>` (with optional
// `tcp://` prefix) into the remote PK and the dial-target address.
func parseSSHFSDestination(dest string) (cipher.PubKey, string, error) {
	m := sshfsDestRE.FindStringSubmatch(dest)
	if m == nil {
		return cipher.PubKey{}, "", fmt.Errorf("sshfs: destination %q must be <66-hex-pk>@<host:port> (optionally prefixed tcp://)", dest)
	}
	var pk cipher.PubKey
	if err := pk.Set(m[1]); err != nil {
		return cipher.PubKey{}, "", fmt.Errorf("sshfs: destination PK invalid: %w", err)
	}
	return pk, m[2], nil
}

// injectDefaultPort splices a port into the destination if the user
// gave only `<pk>@<host>`. IPv6 literals must already carry their own
// `:port`.
func injectDefaultPort(dest, defPort string) string {
	at := -1
	for i := 0; i < len(dest); i++ {
		if dest[i] == '@' {
			at = i
			break
		}
	}
	if at < 0 || at == len(dest)-1 {
		return dest
	}
	host := dest[at+1:]
	for i := 0; i < len(host); i++ {
		if host[i] == ':' {
			return dest
		}
	}
	return dest + ":" + defPort
}

// resolveSSHFSIdentity loads the client SK with the same precedence
// as resolveSSHIdentity in cmd/skywire-cli/commands/ssh: explicit
// --sk > DMSGPTY_SK env > visor SK (when useVisorKey) > random.
func resolveSSHFSIdentity(skFlag cipher.SecKey, useVisorKey bool) (cipher.PubKey, cipher.SecKey) {
	var zero cipher.SecKey
	sk := skFlag
	if sk == zero {
		if env := os.Getenv("DMSGPTY_SK"); env != "" {
			_ = sk.Set(env) //nolint:errcheck,gosec
		}
	}
	if sk == zero && useVisorKey {
		confPath := visorconfig.SkywireConfig()
		conf, err := visorconfig.ReadFile(confPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sshfs: --visor-key read %s: %v\n", confPath, err) //nolint:errcheck
			os.Exit(1)
		}
		if conf.SK == zero {
			fmt.Fprintf(os.Stderr, "sshfs: --visor-key: visor config %s has empty SK\n", confPath) //nolint:errcheck
			os.Exit(1)
		}
		sk = conf.SK
	}
	if sk == zero {
		pk, fresh := cipher.GenerateKeyPair()
		return pk, fresh
	}
	pk, err := sk.PubKey()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sshfs: failed to derive PK from --sk: %v\n", err) //nolint:errcheck
		os.Exit(1)
	}
	return pk, sk
}
