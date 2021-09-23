//go:build darwin
// +build darwin

package skyenv

import (
	"os"
	"path/filepath"
)

const (
	packageSkywirePath = "/Library/Application Support/Skywire"
)

// PackageSkywirePath gets Skywire installation folder.
func PackageSkywirePath() string {
	return filepath.Join(os.Getenv("HOME"), packageSkywirePath)
}
