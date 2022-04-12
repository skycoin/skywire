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

func checkRoot() bool {
thisUser, err := user.Current()
if err != nil {
	panic(err)
}
if thisUser.Username == "root" {
	return true
}
return false
}
