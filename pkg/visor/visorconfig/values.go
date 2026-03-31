// Package visorconfig defines variables and constants for different operating systems
package visorconfig

import (
	"os"
	"os/exec"
	"strings"

	"github.com/bitfield/script"
	"github.com/skycoin/dmsg/pkg/dmsg"

	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
)

var (
	// DmsgHTTPPort Listening port for dmsghttp logserver.
	DmsgHTTPPort = dmsg.DefaultDmsgHTTPPort

	// PublicVisorMaxTransports is the max transport count before deregistering
	PublicVisorMaxTransports = 1000
)

// SkywireConfig returns the full path to the package config
func SkywireConfig() string {
	return skyenv.SkywirePath + "/" + skyenv.ConfigJSON
}

// Version gets the version of the installation for the config
func Version() string {
	u := buildinfo.Version()
	v := u
	if u == "unknown" {
		//check for .git folder for versioning
		if _, err := os.Stat(".git"); err == nil {
			//attempt to version from git sources
			if _, err = exec.LookPath("git"); err == nil {
				if v, err = script.Exec(`git describe`).String(); err == nil {
					v = strings.ReplaceAll(v, "\n", "")
					v = strings.Split(v, "-")[0]
				}
			}
		}
	}
	return v
}

// HomePath gets the current user's home folder
func HomePath() string {
	dir, _ := os.UserHomeDir() //nolint:errcheck
	return dir
}

var (
	// VisorConfigFile will contain the path to the visor's config or `stdin` to denote that the config was read from STDIN
	VisorConfigFile string
)

// PackageConfig returns the package-specific config paths
func PackageConfig() skyenv.PkgConfig {
	return skyenv.PackageConfig()
}

// UpdateCommand returns the commands which are run when the update button is clicked in the ui
func UpdateCommand() []string {
	return []string{`echo "not implemented"`}
}
