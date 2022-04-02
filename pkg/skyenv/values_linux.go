//go:build linux
// +build linux

package skyenv

const OS = "linux"

// SkywirePath is the path to the installation folder for the linux packages.
var	SkywirePath = "/opt/skywire"
// Skywirejson is the Hypervisor config
//created by the autoconfig script in the linux packages
//also referenced in the skywire systemd service
var Skywirejson = "skywire.json"
// Skywirevisorjson is the visor config
//created by the autoconfig script in the linux packages
//also referenced in the skywire-visor systemd service
var Skywirevisorjson = "skywire-visor.json"
var DmsghttpPath = "/opt/skywire/dmsghttp-config.json"


// PackageConfig config defaults for the linux packages
func PackageConfig() PkgConfig {
	var pkgconfig PkgConfig
	pkgconfig.Launcher.BinPath = "/opt/skywire/apps"
	pkgconfig.LocalPath = "/opt/skywire/local"
	pkgconfig.Hypervisor.DbPath = "/opt/skywire/users.db" //permissions errors if the process is not run as root.
	pkgconfig.Hypervisor.EnableAuth = true
	return pkgconfig
}
