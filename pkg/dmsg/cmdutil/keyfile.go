// Package cmdutil pkg/cmdutil/keyfile.go
package cmdutil

import (
	"fmt"
	"os"
	"strings"

	coinCipher "github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
)

// LoadOrGenerateKey loads a secret key from a file, or generates a new keypair
// and writes it to the file if it doesn't exist. This allows systemd services
// to auto-generate their identity on first run without bash workarounds.
func LoadOrGenerateKey(path string, sk *cipher.SecKey) error {
	data, err := os.ReadFile(path) //nolint:gosec
	if err == nil {
		trimmed := strings.TrimSpace(string(data))
		if trimmed == "" {
			return fmt.Errorf("keyfile %s is empty", path)
		}
		return sk.Set(trimmed)
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("failed to read keyfile %s: %w", path, err)
	}
	// Auto-generate new keypair
	pk, newSK := coinCipher.GenerateKeyPair()
	content := newSK.Hex() + "\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		return fmt.Errorf("failed to write keyfile %s: %w", path, err)
	}
	*sk = cipher.SecKey(newSK)
	fmt.Printf("Generated new keypair in %s\nPublic key: %s\n", path, pk.Hex())
	return nil
}
