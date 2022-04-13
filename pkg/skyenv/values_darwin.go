//go:build darwin
// +build darwin

package skyenv

const (
	//OS detection at runtime
	OS = "mac"
	// SkywirePath is the path to the installation folder.
	SkywirePath = "/Library/Application Support/Skywire"
)

// PackageConfig contains installation paths (for mac)
func PackageConfig() PkgConfig {
	var pkgconfig PkgConfig
	pkgconfig.Launcher.BinPath = "/Library/Application Support/Skywire/apps"
	pkgconfig.LocalPath = "/Library/Application Support/Skywire/local"
	pkgconfig.Hypervisor.DbPath = "/Library/Application Support/Skywire/users.db"
	pkgconfig.Hypervisor.EnableAuth = true
	return pkgconfig
}

// UserConfig contains installation paths (for mac)
func UserConfig() PkgConfig {
	var usrconfig PkgConfig
	usrconfig.Launcher.BinPath = "/Library/Application Support/Skywire/apps"
	usrconfig.LocalPath = HomePath() + "/.skywire/local"
	usrconfig.Hypervisor.DbPath = HomePath() + "/.skywire/users.db" //permissions errors if the process is not run as root.
	usrconfig.Hypervisor.EnableAuth = true
	return usrconfig
}
