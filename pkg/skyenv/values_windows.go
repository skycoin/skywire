//go:build windows
// +build windows

package skyenv

//OS detection at runtime
const OS = "win" // nolint

// SkywirePath is the path to the installation folder.
// TODO (darkrengarius): change path

// SkywirePath is the path to the installation folder
var SkywirePath = ""

// DmsghttpPath is the path to dmsghttp-config.json in the packages
var DmsghttpPath = "dmsghttp-config.json"

// Skywirejson is the Hypervisor config
var Skywirejson = "skywire.json"

// Skywirevisorjson is the visor config
var Skywirevisorjson = "skywire-visor.json"

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
