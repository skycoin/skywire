//go:build linux && !withoutsystray
// +build linux,!withoutsystray

package visor

import (
	"os"
	"os/user"
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

func isRoot() bool {
	userLvl, err := user.Current()
	if err == nil {
		if userLvl.Username == "root" {
			return true
		}
	}
	return false
}
