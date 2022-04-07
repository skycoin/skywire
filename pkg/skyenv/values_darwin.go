//go:build darwin
// +build darwin

package skyenv

const (
	//OS detection at runtime
	OS = "mac"

	// SkywirePath is the path to the installation folder.
	// skywireApplicationPath = "/Applications/Skywire.app"
	SkywirePath = "/Library/Application Support/Skywire"

	// DmsghttpPath is the path to dmsghttp-config.json
	DmsghttpPath = "dmsghttp-config.json"

	// Skywirejson is the Hypervisor config
	Skywirejson = "skywire.json"

	// Skywirevisorjson is the visor config
	Skywirevisorjson = "skywire-visor.json"
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
