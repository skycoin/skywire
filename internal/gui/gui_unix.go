//go:build !windows && systray
// +build !windows,systray

package gui

import (
	"os/user"

	"github.com/skycoin/skywire/pkg/util/osutil"
)

func platformExecUninstall() error {
	return osutil.Run("/bin/bash", "-c", deinstallerPath)
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
