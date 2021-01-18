//+build linux

package skyenv

const (
	packageSkywirePath = "/opt/skywire"
)

func PackageSkywirePath() string {
	return packageSkywirePath
}
