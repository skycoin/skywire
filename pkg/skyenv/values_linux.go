//go:build linux
// +build linux

package skyenv

//OS detection at runtime
const OS = "linux"

// SkywirePath is the path to the installation folder for the linux packages.
var SkywirePath = "/opt/skywire"

// DmsghttpPath is the path to dmsghttp-config.json in the packages
var DmsghttpPath = "/opt/skywire/dmsghttp-config.json"

//The following files are created by the autoconfig script in the linux packages
//also referenced in the skywire systemd service

// Skywirejson is the Hypervisor config
var Skywirejson = "skywire.json"

// Skywirevisorjson is the visor config
var Skywirevisorjson = "skywire-visor.json"

// PackageConfig contains installation paths (for linux)
func PackageConfig() PkgConfig {
	var pkgconfig PkgConfig
	pkgconfig.Launcher.BinPath = "/opt/skywire/apps"
	pkgconfig.LocalPath = "/opt/skywire/local"
	pkgconfig.Hypervisor.DbPath = "/opt/skywire/users.db" //permissions errors if the process is not run as root.
	pkgconfig.Hypervisor.EnableAuth = true
	return pkgconfig
}
