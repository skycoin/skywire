//go:build darwin
// +build darwin

// Package skyenv defines variables and constants
package skyenv

const (
	//OS detection at runtime
	OS = "mac"
	// SkywirePath is the path to the installation folder.
	SkywirePath = "/Library/Application Support/Skywire"
	// ConfigJSON is the config name generated by the script included with the installation on mac
	ConfigJSON = "skywire-config.json"
)

// PackageConfig contains installation paths (for mac)
func PackageConfig() PkgConfig {
	pkgConfig := PkgConfig{
		LauncherBinPath: "/Applications/Skywire.app/Contents/MacOS/bin",   //apps are now subcommands of the skywire binary "/Applications/Skywire.app/Contents/MacOS/apps",
		LocalPath:       "/Library/Application Support/Skywire/local",
		Hypervisor: Hypervisor{
			DbPath:     "/Library/Application Support/Skywire/users.db",
			EnableAuth: true,
		},
	}
	return pkgConfig
}
