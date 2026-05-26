// Package visorconfig defines variables and constants for different operating systems
package visorconfig

import (
	"os"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skyenv"
)

var (
	// DmsgHTTPPort Listening port for dmsghttp logserver. Set to
	// pkg/dmsg/dmsg.DefaultDmsgHTTPPort's value (80). Hardcoded here
	// rather than imported so values.go stays WASM-clean — the dmsg
	// client package pulls in unrelated heavy deps that the
	// in-browser config-generator doesn't need.
	DmsgHTTPPort = uint16(80)

	// PublicVisorMaxTransports is the max transport count before deregistering
	PublicVisorMaxTransports = 1000
)

// SkywireConfig returns the full path to the package config
func SkywireConfig() string {
	return skyenv.SkywirePath + "/" + skyenv.ConfigJSON
}

// Version gets the version of the installation for the config.
//
// The runtime version-discovery path (git-describe fallback when
// buildinfo.Version() returns "unknown") lives in values_native.go
// behind a !js build tag — it shells out to `git`, which is
// nonsensical in a browser WASM context. Under js the returned value
// is just buildinfo.Version() with no fallback.
func Version() string {
	return resolveVersion(buildinfo.Version())
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
