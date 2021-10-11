//go:build darwin && systray
// +build darwin,systray

package gui

import (
	"os"
)

const (
	deinstallerPath = "/Applications/Skywire.app/Contents/deinstaller"
	appPath         = "/Applications/Skywire.app"
	iconName        = "icons/icon.tiff"
)

func checkIsPackage() bool {
	_, err := os.Stat(appPath)
	return err == nil
}
