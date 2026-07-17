// Package visorconfig pkg/visor/visorconfig/values.go c3-vis-core
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

	// PublicVisorMaxTransports is the DISTINCT-PEER count at which a public visor
	// drain-pauses its service-discovery registration (load-shedding). It counts
	// unique remote visors, not raw transports — several transport types to the
	// same peer (stcpr + sudph + squicr, for route diversity) count as one — so
	// the cap describes real peer load, not transport-type multiplication. It
	// must sit comfortably above the fleet size, because a public visor that also
	// runs public_autoconnect peers with ~every other public visor. At 1000 (≈
	// the fleet size) the busiest visors oscillated across the cap and flapped
	// out of the registry, starving browser/wasm clients of a reachable target.
	// 2048 gives ~2× headroom over the current fleet.
	PublicVisorMaxTransports = 2048
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
