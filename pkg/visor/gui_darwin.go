//go:build darwin && !withoutsystray
// +build darwin,!withoutsystray

// Package visor pkg/visor/gui_darwin.go c3-vis-core
package visor

import (
	"os"
)

const (
	deinstallerPath = "/Applications/Skywire.app/Contents/MacOS/deinstaller"
	appPath         = "/Applications/Skywire.app"
	iconName        = "icon.tiff"
)

func checkIsPackage() bool {
	_, err := os.Stat(deinstallerPath)
	return err == nil
}

func isRoot() bool {
	return false
}
