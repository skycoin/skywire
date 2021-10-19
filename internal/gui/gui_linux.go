//go:build linux && systray
// +build linux,systray

package gui

import (
	"os"
)

// TODO (darkrengarius): change path
const (
	iconName        = "icons/icon.png"
	deinstallerPath = "/opt/skywire/deinstaller"
)

func checkIsPackage() bool {
	_, err := os.Stat(deinstallerPath)
	return err == nil
}
