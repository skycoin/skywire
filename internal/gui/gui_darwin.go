//go:build darwin && systray
// +build darwin,systray

package gui

import (
	"os"
)

const (
	iconName        = "icons/icon.tiff"
	deinstallerPath = "/Applications/Skywire.app/Contents/deinstaller"
	appPath         = "/Applications/Skywire.app"
)

func checkIsPackage() bool {
	_, err := os.Stat(deinstallerPath)
	return err == nil
}
