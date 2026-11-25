// Package cipher pkg/cipher/keyfile.go c0-com-crypto
package cipher

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// LoadOrGenerateKey loads a secret key from a file, or generates a new keypair
// and writes it to the file if it doesn't exist. This allows systemd services
// to auto-generate their identity on first run without bash workarounds.
func LoadOrGenerateKey(path string, sk *SecKey) error {
	data, err := os.ReadFile(path) //nolint:gosec
	if err == nil {
		trimmed := strings.TrimSpace(string(data))
		if trimmed == "" {
			return fmt.Errorf("keyfile %s is empty", path)
		}
		return sk.Set(trimmed)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("failed to read keyfile %s: %w", path, err)
	}
	pk, newSK := GenerateKeyPair()
	content := newSK.Hex() + "\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		return fmt.Errorf("failed to write keyfile %s: %w", path, err)
	}
	*sk = newSK
	fmt.Printf("Generated new keypair in %s\nPublic key: %s\n", path, pk.Hex())
	return nil
}
