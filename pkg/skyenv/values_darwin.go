//+build darwin

package skyenv

import "os"

const (
	packageSkywirePath = "/skywire"
)

func PackageSkywirePath() string {
	return os.Getenv("HOME") + packageSkywirePath
}
