//go:build windows
// +build windows

package skyenv

const (
	// TODO (darkrengarius): change path
	packageSkywirePath = "/opt/skywire"
)

// PackageSkywirePath gets Skywire installation folder.
func PackageSkywirePath() string {
	return packageSkywirePath
}

func appBinPath() string {
	return packageSkywirePath
}
