//go:build darwin && systray
// +build darwin,systray

package gui

import (
	"os"
)

const (
	deinstallerPath = "/Applications/Skywire.app/Contents/MacOS/deinstaller"
	appPath         = "/Applications/Skywire.app"
	iconName        = "icons/icon.tiff"
)

func checkIsPackage() bool {
	_, err := os.Stat(deinstallerPath)
	return err == nil
}
