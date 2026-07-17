//go:build linux && !withoutsystray
// +build linux,!withoutsystray

// Package visor pkg/visor/gui_linux.go c3-vis-core
package visor

import (
	"os"
	"os/user"
)

// TODO (darkrengarius): change path
const (
	iconName        = "icon.png"
	deinstallerPath = "/opt/skywire/deinstaller"
)

func checkIsPackage() bool {
	_, err := os.Stat(deinstallerPath)
	return err == nil
}

func isRoot() bool {
	userLvl, err := user.Current()
	if err == nil {
		if userLvl.Username == "root" {
			return true
		}
	}
	return false
}
