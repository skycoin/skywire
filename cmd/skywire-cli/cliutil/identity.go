// Package cliutil cmd/skywire-cli/cliutil/identity.go c4-vis-cli
package cliutil

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/skycoin/skywire/pkg/cipher"
)

// CLIKeyFileEnv names the environment variable that relocates the CLI's
// own dmsg identity file. Unset, the identity lives at
// <user home>/.skywire/cli.key.
const CLIKeyFileEnv = "SKYWIRE_CLI_KEYFILE"

// cliKeyFileName is the basename of the CLI identity under ~/.skywire —
// the same user-space directory the visor's own userspace config and the
// skysocks-client MITM root already use.
const cliKeyFileName = "cli.key"

// CLIKeyFilePath returns where the CLI's persistent dmsg identity lives.
func CLIKeyFilePath() (string, error) {
	if p := os.Getenv(CLIKeyFileEnv); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate user home dir: %w", err)
	}
	return filepath.Join(home, ".skywire", cliKeyFileName), nil
}

// CLIIdentity returns the keypair the CLI runs as when it has to hold a
// dmsg client of its own, generating and persisting one on first use.
//
// A CLI invocation that talks dmsg directly needs *an* identity, and the
// only wrong answer is a fresh one per run: every invocation then costs a
// new keypair, a new Noise handshake against every production dmsg server,
// and — on the code paths that publish — a new abandoned dmsg-discovery
// entry (the shape removed from `hv serve` in #4501 and from the reward
// server in #4502). One key per user, written once and reused, keeps the
// CLI's footprint on the network constant no matter how often it is polled.
//
// It is deliberately NOT the local visor's key. dmsg-discovery holds one
// entry per public key and a dmsg server one session per key, so a second
// client under the visor's identity would fight the visor for both.
//
// Returns (pk, sk, path). Callers that can proceed without dmsg should
// treat an error as non-fatal — an unwritable home is a reason to warn,
// not to fail the command.
func CLIIdentity() (cipher.PubKey, cipher.SecKey, string, error) {
	path, err := CLIKeyFilePath()
	if err != nil {
		return cipher.PubKey{}, cipher.SecKey{}, "", err
	}

	if pk, sk, ok, rerr := readCLIKeyFile(path); ok {
		return pk, sk, path, nil
	} else if rerr != nil {
		return cipher.PubKey{}, cipher.SecKey{}, path, rerr
	}

	// First use. Create with O_EXCL so two CLI invocations racing on a
	// cold home directory converge on one key instead of clobbering
	// each other; the loser re-reads what the winner wrote.
	pk, sk := cipher.GenerateKeyPair()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return cipher.PubKey{}, cipher.SecKey{}, path, fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // operator-chosen path (SKYWIRE_CLI_KEYFILE) or the default under $HOME
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			if rpk, rsk, ok, rerr := readCLIKeyFile(path); ok {
				return rpk, rsk, path, nil
			} else if rerr != nil {
				return cipher.PubKey{}, cipher.SecKey{}, path, rerr
			}
		}
		return cipher.PubKey{}, cipher.SecKey{}, path, fmt.Errorf("write keyfile %s: %w", path, err)
	}
	if _, err := f.WriteString(sk.Hex() + "\n"); err != nil {
		f.Close() //nolint:errcheck,gosec
		return cipher.PubKey{}, cipher.SecKey{}, path, fmt.Errorf("write keyfile %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return cipher.PubKey{}, cipher.SecKey{}, path, fmt.Errorf("write keyfile %s: %w", path, err)
	}
	// stderr, never stdout: --json output has to stay parseable.
	fmt.Fprintf(os.Stderr, "skywire-cli: generated a persistent dmsg identity at %s\nskywire-cli: public key: %s\n", path, pk.Hex()) //nolint:errcheck
	return pk, sk, path, nil
}

// readCLIKeyFile reads an existing identity file. ok reports whether a
// usable keypair was read; a missing file is (false, nil) so the caller
// can create one, while an unreadable or malformed file is an error —
// silently minting a replacement would defeat the point of persisting.
func readCLIKeyFile(path string) (cipher.PubKey, cipher.SecKey, bool, error) {
	raw, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			return cipher.PubKey{}, cipher.SecKey{}, false, nil
		}
		return cipher.PubKey{}, cipher.SecKey{}, false, fmt.Errorf("read keyfile %s: %w", path, err)
	}
	var sk cipher.SecKey
	if err := sk.Set(strings.TrimSpace(string(raw))); err != nil {
		return cipher.PubKey{}, cipher.SecKey{}, false, fmt.Errorf("keyfile %s: %w", path, err)
	}
	pk, err := sk.PubKey()
	if err != nil {
		return cipher.PubKey{}, cipher.SecKey{}, false, fmt.Errorf("keyfile %s: derive pubkey: %w", path, err)
	}
	return pk, sk, true, nil
}
