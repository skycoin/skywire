//+build windows

package skyenv

const (
	// TODO (darkrengarius): change path
	packageSkywirePath = "/opt/skywire"
)

func PackageSkywirePath() string {
	return packageSkywirePath
}
