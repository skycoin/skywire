//go:build !windows && systray
// +build !windows,systray

package gui

import "github.com/skycoin/skywire/pkg/util/osutil"

// TODO (darkrengarius): change path
const (
	iconName        = "icons/icon.png"
	deinstallerPath = "/opt/skywire/deinstaller"
)

func platformExecUninstall() error {
	return osutil.Run("/bin/bash", "-c", deinstallerPath)
}
