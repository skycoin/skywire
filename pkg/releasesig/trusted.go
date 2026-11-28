// Package releasesig pkg/releasesig/trusted.go c4-vis-cli
package releasesig

import (
	"embed"
	"path"
)

// trustedFiles holds the public keys release signatures are checked against.
// ci signs every build in GitHub Actions; offline signs tagged releases.
//
//go:embed keys/*.pub
var trustedFiles embed.FS

// TrustedKeys returns the embedded release signing keys.
func TrustedKeys() ([]*PublicKey, error) {
	entries, err := trustedFiles.ReadDir("keys")
	if err != nil {
		return nil, err
	}
	keys := make([]*PublicKey, 0, len(entries))
	for _, entry := range entries {
		text, err := trustedFiles.ReadFile(path.Join("keys", entry.Name()))
		if err != nil {
			return nil, err
		}
		key, err := PublicKeyParse(text)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, nil
}
