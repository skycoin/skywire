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

// PkgConfig struct contains paths specific to the linux packages
type PkgConfig struct {
	LauncherBinPath string `json:"launcher"`
	LocalPath       string `json:"local_path"`
	Hypervisor      `json:"hypervisor"`
	//		TLSCertFile string `json:"tls_cert_file"`
	//		TLSKeyFile  string `json:"tls_key_file"`
}

// LauncherBinPath struct contains the BinPath specific to the installation
type LauncherBinPath struct {
	BinPath string `json:"bin_path"`
}

// Hypervisor struct contains Hypervisor paths specific to the linux packages
type Hypervisor struct {
	DbPath     string `json:"db_path"`
	EnableAuth bool   `json:"enable_auth"`
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
