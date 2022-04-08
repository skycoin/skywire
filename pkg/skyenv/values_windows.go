//go:build windows
// +build windows

package skyenv

//OS detection at runtime
const (
	OS = "win"
	// SkywirePath is the path to the installation folder
	SkywirePath = ""
)

//TODO implement this similarly for windows

// PackageConfig contains installation paths (for windows)
func PackageConfig() PkgConfig {
	var pkgconfig PkgConfig
	pkgconfig.Launcher.BinPath = "/opt/skywire/apps"
	pkgconfig.LocalPath = "/opt/skywire/local"
	pkgconfig.Hypervisor.DbPath = "/opt/skywire/users.db" //permissions errors if the process is not run as root!
	pkgconfig.Hypervisor.EnableAuth = true
	return pkgconfig
}
