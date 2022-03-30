//go:build linux
// +build linux

package skyenv

const (
	skywirePath = "/opt/skywire"
	OS          = "linux" // nolint
)

// SkywirePath gets Skywire installation folder.
func SkywirePath() string {
	return skywirePath
}

// PackageConfig is the path to local directory
func PackageConfig() PkgConfig {
	var pkgconfig PkgConfig
	pkgconfig.Launcher.BinPath = "/opt/skywire/apps"
	pkgconfig.LocalPath = "/opt/skywire/local"
	pkgconfig.DmsghttpPath = "/opt/skywire/dmsghttp-config.json"
	pkgconfig.Hypervisor.DbPath = "/opt/skywire/users.db" //permissions errors if the process is not run as root.
	pkgconfig.Hypervisor.EnableAuth = true
	return pkgconfig
}
