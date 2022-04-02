//go:build darwin
// +build darwin

package skyenv

import (
	"os"
	"path/filepath"
)

const OS = "mac" //nolint

// SkywirePath is the path to the installation folder.
// skywireApplicationPath = "/Applications/Skywire.app"
var SkywirePath = "/Library/Application Support/Skywire"


// SkywirePath gets Skywire installation folder.
//func SkywirePath() string {
//	return filepath.Join(os.Getenv("HOME"), skywirePath)
//}

/*
//TODO implement this similarly for macOS
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
