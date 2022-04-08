//go:build darwin
// +build darwin

package skyenv

const (
	//OS detection at runtime
	OS = "mac"

	// SkywirePath is the path to the installation folder.
	SkywirePath = "/Library/Application Support/Skywire"
)

//TODO implement this similarly for macOS

// PackageConfig contains installation paths (for mac)
func PackageConfig() PkgConfig {
	var pkgconfig PkgConfig
	pkgconfig.Launcher.BinPath = "/opt/skywire/apps"
	pkgconfig.LocalPath = "/opt/skywire/local"
	pkgconfig.Hypervisor.DbPath = "/opt/skywire/users.db" //permissions errors if the process is not run as root!
	pkgconfig.Hypervisor.EnableAuth = true
	return pkgconfig
}
