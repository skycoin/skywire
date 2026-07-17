//go:build linux

// Package cliptyfs cmd/skywire-cli/commands/ptyfs/identity_linux.go c4-vis-cli
// destination parsing + identity loading helpers used by the linux
// mount path. Gated to linux because the only consumer is
// mount_linux.go; non-linux builds get stub subcommands that don't
// need this surface, so leaving these here unguarded would flag as
// unused on darwin/windows.
package cliptyfs

import (
	"fmt"
	"os"
	"regexp"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// ptyfsDestRE matches both `<pk>@<host>:<port>` and the more explicit
// `tcp://<pk>@<host>:<port>` form, identical to clissh's destination
// shape. Kept as a separate copy here so cli ptyfs can stand alone if
// cli pty shell is ever split out.
var ptyfsDestRE = regexp.MustCompile(`^(?:tcp://)?([a-f0-9]{66})@(.+:[^:]+)$`)

// parsePTYFSDestination splits `<pk>@<host>:<port>` (with optional
// `tcp://` prefix) into the remote PK and the dial-target address.
func parsePTYFSDestination(dest string) (cipher.PubKey, string, error) {
	m := ptyfsDestRE.FindStringSubmatch(dest)
	if m == nil {
		return cipher.PubKey{}, "", fmt.Errorf("ptyfs: destination %q must be <66-hex-pk>@<host:port> (optionally prefixed tcp://)", dest)
	}
	var pk cipher.PubKey
	if err := pk.Set(m[1]); err != nil {
		return cipher.PubKey{}, "", fmt.Errorf("ptyfs: destination PK invalid: %w", err)
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

// resolvePTYFSIdentity loads the client SK with the same precedence
// as resolveSSHIdentity in cmd/skywire-cli/commands/ssh: explicit
// --sk > DMSGPTY_SK env > visor SK (when useVisorKey) > random.
func resolvePTYFSIdentity(skFlag cipher.SecKey, useVisorKey bool) (cipher.PubKey, cipher.SecKey) {
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
			fmt.Fprintf(os.Stderr, "ptyfs: --visor-key read %s: %v\n", confPath, err) //nolint:errcheck
			os.Exit(1)
		}
		if conf.SK == zero {
			fmt.Fprintf(os.Stderr, "ptyfs: --visor-key: visor config %s has empty SK\n", confPath) //nolint:errcheck
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
		fmt.Fprintf(os.Stderr, "ptyfs: failed to derive PK from --sk: %v\n", err) //nolint:errcheck
		os.Exit(1)
	}
	return pk, sk
}
