//go:build darwin
// +build darwin

package skyenv

import (
	"os"
	"path/filepath"
)

const (
	packageSkywirePath     = "/Library/Application Support/Skywire"
	skywireApplicationPath = "/Applications/Skywire.app"
)

// PackageSkywirePath gets Skywire installation folder.
func PackageSkywirePath() string {
	return filepath.Join(os.Getenv("HOME"), packageSkywirePath)
}

// PackageAppBinPath gets the Skywire application directory folder.
func appBinPath() string {
	return filepath.Join(skywireApplicationPath, "Contents", "MacOS")
}
