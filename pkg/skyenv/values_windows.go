//go:build windows
// +build windows

package skyenv

const (
	// TODO (darkrengarius): change path
	skywirePath = "/opt/skywire"
	OS          = "win" // nolint

)

// SkywirePath gets Skywire installation folder.
func SkywirePath() string {
	return skywirePath
}

/*
//TODO implement this similarly for windows
// PackageConfig contains specific installation paths
func PackageConfig() PkgConfig {
	var pkgconfig PkgConfig
	pkgconfig.Launcher.BinPath = "/opt/skywire/apps"
	pkgconfig.LocalPath = "/opt/skywire/local"
	pkgconfig.DmsghttpPath = "/opt/skywire/dmsghttp-config.json"
	pkgconfig.Hypervisor.DbPath = "/opt/skywire/users.db" //permissions errors if the process is not run as root!
	pkgconfig.Hypervisor.EnableAuth = true
	return pkgconfig
}
*/
