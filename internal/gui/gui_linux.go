//+build linux,systray

package gui

// TODO (darkrengarius): change path
const (
	iconPath        = "/opt/skywire/icon.png"
	deinstallerPath = "/opt/skywire/deinstaller"
)

func preReadIcon() error {
	return nil
}

func checkIsPackage() bool {
	_, err := os.Stat(deinstallerPath)
	return err == nil
}
