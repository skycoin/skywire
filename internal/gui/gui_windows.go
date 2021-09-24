//go:build windows && systray
// +build windows,systray

package gui

import (
	"os"
	"path/filepath"

	"github.com/skycoin/skywire/pkg/util/osutil"
)

// TODO (darkrengarius): change path
const iconPath = "%LOCALDATA\\skywire\\icon.png"

func deinstallerPath() string {
	return filepath.Join(localDataPath, "skywire", "deinstaller.ps1")
}

func platformExecUninstall() error {
	localDataPath := os.Getenv("LOCALDATA")
	return osutil.Run("pwsh", "-c", deinstallerPath)
}

func checkIsPackage() bool {
	_, err := os.Stat(deinstallerPath())
	return err == nil
}
