//+build darwin

package skyenv

import "os"

const (
	packageSkywirePath = "/Skywire"
)

func PackageSkywirePath() string {
	return os.Getenv("HOME") + packageSkywirePath
}
