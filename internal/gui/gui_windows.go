//go:build windows && systray
// +build windows,systray

package gui

import (
	"os"
	"path/filepath"

	"github.com/skycoin/skywire/pkg/util/osutil"
)

// TODO (darkrengarius): change path
const iconName = "icons/icon.ico"

func localDataPath() string {
	return os.Getenv("LOCALDATA")
}

func deinstallerPath() string {
	return filepath.Join(localDataPath(), "skywire", "deinstaller.ps1")
}

func platformExecUninstall() error {
	return osutil.Run("pwsh", "-c", deinstallerPath())
}

func checkIsPackage() bool {
	_, err := os.Stat(deinstallerPath())
	return err == nil
}
