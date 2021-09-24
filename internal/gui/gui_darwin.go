//go:build darwin && systray
// +build darwin,systray

package gui

import (
	"os"
)

const (
	iconPath        = "/Applications/Skywire.app/Contents/Resources/icon.tiff"
	deinstallerPath = "/Applications/Skywire.app/Contents/deinstaller"
	appPath         = "/Applications/Skywire.app"
)

func checkIsPackage() bool {
	_, err := os.Stat(appPath)
	return err == nil
}
