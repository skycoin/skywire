//go:build darwin
// +build darwin

package skyenv

//OS detection at runtime
const OS = "mac" //nolint

// SkywirePath is the path to the installation folder.
//TODO implement this similarly for macOS
// skywireApplicationPath = "/Applications/Skywire.app"
// SkywirePath is the path to the installation folder
var SkywirePath = "/Library/Application Support/Skywire"

// DmsghttpPath is the path to dmsghttp-config.json
var DmsghttpPath = "dmsghttp-config.json"
/*
// PackageConfig contains specific installation paths
func PackageConfig() PkgConfig {
	var pkgconfig PkgConfig
	pkgconfig.Launcher.BinPath = "/opt/skywire/apps"
	pkgconfig.LocalPath = "/opt/skywire/local"
	pkgconfig.Hypervisor.DbPath = "/opt/skywire/users.db" //permissions errors if the process is not run as root!
	pkgconfig.Hypervisor.EnableAuth = true
	return pkgconfig
}
*/
