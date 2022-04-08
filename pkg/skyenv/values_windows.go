//go:build windows
// +build windows

package skyenv

//OS detection at runtime
const (
	OS = "win"
	// SkywirePath is the path to the installation folder
	SkywirePath = "C:/Program Files/Skywire"
)

//TODO implement this similarly for windows

// PackageConfig contains installation paths (for windows)
func PackageConfig() PkgConfig {
	var pkgconfig PkgConfig
	pkgconfig.Launcher.BinPath = "C:/Program Files/Skywire/apps"
	pkgconfig.LocalPath = "C:/Program Files/Skywire/local"
	pkgconfig.Hypervisor.DbPath = "C:/Program Files/Skywire/users.db"
	pkgconfig.Hypervisor.EnableAuth = true
	return pkgconfig
}
