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
	pkgconfig.Hypervisor.DbPath = "/opt/skywire/users.db" //permissions errors if the process is not run as root.
	pkgconfig.Hypervisor.EnableAuth = true
	return pkgconfig
}
