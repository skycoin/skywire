// Package deployment keyfile.go
package deployment

import (
	"fmt"
	"os"
	"strings"

	"github.com/skycoin/skywire/pkg/cipher"
)

// ReadKeyfile reads a secret key from a file. The file should contain
// just the hex-encoded secret key, optionally with whitespace/newlines.
func ReadKeyfile(path string) (cipher.SecKey, error) {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return cipher.SecKey{}, fmt.Errorf("read keyfile %s: %w", path, err)
	}
	skHex := strings.TrimSpace(string(data))
	if skHex == "" {
		return cipher.SecKey{}, fmt.Errorf("keyfile %s is empty", path)
	}
	var sk cipher.SecKey
	if err := sk.Set(skHex); err != nil {
		return cipher.SecKey{}, fmt.Errorf("parse keyfile %s: %w", path, err)
	}
	return sk, nil
}

// ResolveSecKey returns a secret key from either a keyfile path or a
// direct hex value. Keyfile takes precedence if both are provided.
// Returns a zero key if neither is provided.
func ResolveSecKey(keyfile string, skHex string) (cipher.SecKey, error) {
	if keyfile != "" {
		return ReadKeyfile(keyfile)
	}
	if skHex != "" {
		var sk cipher.SecKey
		if err := sk.Set(skHex); err != nil {
			return cipher.SecKey{}, fmt.Errorf("parse secret key: %w", err)
		}
		return sk, nil
	}
	return cipher.SecKey{}, nil
}
