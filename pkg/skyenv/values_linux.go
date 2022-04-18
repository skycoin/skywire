//go:build linux
// +build linux

package skyenv

const (
	//OS detection at runtime
	OS = "linux"
	// SkywirePath is the path to the installation folder for the linux packages.
	SkywirePath = "/opt/skywire"
)

// PackageConfig contains installation paths (for linux)
func PackageConfig() PkgConfig {
	var pkgconfig PkgConfig
	pkgconfig.Launcher.BinPath = "/opt/skywire/apps"
	pkgconfig.LocalPath = "/opt/skywire/local"
	pkgconfig.Hypervisor.DbPath = "/opt/skywire/users.db"
	pkgconfig.Hypervisor.EnableAuth = true
	return pkgconfig
}

// UserConfig contains installation paths (for linux)
func UserConfig() PkgConfig {
	var usrconfig PkgConfig
	usrconfig.Launcher.BinPath = "/opt/skywire/apps"
	usrconfig.LocalPath = HomePath() + "/.skywire/local"
	usrconfig.Hypervisor.DbPath = HomePath() + "/.skywire/users.db"
	usrconfig.Hypervisor.EnableAuth = true
	return usrconfig
}
