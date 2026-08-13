//go:build !js

// Package visorconfig pkg/visor/visorconfig/parse.go c3-vis-core
//
// Parse wraps Reader (from read.go, also !js-tagged) and adds
// version-compat checking via blang/semver. Both deps (encoding/
// json transitively, and blang/semver which imports json itself)
// stay out of the WASM build. Browser-side WASM never parses an
// on-disk config; it constructs V1 in memory via genvisor.Generate.
package visorconfig

import (
	"errors"
	"io"
	"strings"

	"github.com/blang/semver/v4"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/logging"
)

var (
	// ErrInvalidSK occurs when config file has an invalid secret key.
	ErrInvalidSK = errors.New("config has invalid secret key")
)

// Parse parses the visor config from a given reader.
// The config version is checked against the visor's version and if not the same we send back the
// error as well as compat(compatibility) as false.
func Parse(log *logging.Logger, r io.Reader, confPath string, visorBuildInfo *buildinfo.Info) (conf *V1, compat bool, err error) {

	conf, err = Reader(r, confPath)
	if err != nil {
		log.Debug(`error on Reader(r, confPath)`)
		return nil, compat, err
	}
	// A visor built without version stamping reports a Go VCS pseudo-version
	// (v0.0.0-<timestamp>-<commit>), "(devel)", or "unknown" — a dev/CI/container
	// build with no real release version. The config-vs-binary version check is
	// meaningless for those: it would reject every valid config against an
	// unstamped binary (shallow CI checkouts and docker images both build
	// v0.0.0), so skip it and only enforce for properly released binaries.
	visorUnversioned := visorBuildInfo.Version == "unknown" ||
		strings.HasPrefix(visorBuildInfo.Version, "v0.0.0")
	// we check if the version of the visor and config are the same
	if (conf.Version != "unknown") && !visorUnversioned {
		cVer, cErr := semver.Make(strings.TrimPrefix(conf.Version, "v"))
		vVer, vErr := semver.Make(strings.TrimPrefix(visorBuildInfo.Version, "v"))
		if cErr != nil || vErr != nil {
			// An unparseable version on either side means the comparison cannot
			// be made — which is not the same thing as the config being wrong,
			// and is not worth refusing to start over. A build stamped with a
			// non-semver version puts that string in BOTH places, because
			// `config gen` writes the binary's version into the config it
			// writes: Android APKs built from a `mobile-vX.Y.Z` tag did exactly
			// that. Failing here left those installs with no way forward but
			// deleting the config — on a phone, the file holding the identity.
			// So warn, and treat it like a config that says "unknown".
			log.WithField("config_version", conf.Version).
				WithField("visor_version", visorBuildInfo.Version).
				Warn("Skipping the config version check: not a valid version. Regenerate the config with `skywire autoconfig` to clear this.")
			compat = true
			conf.path = confPath
			return conf, compat, nil
		}
		// Surface the config version alongside the visor version (logged
		// separately at startup) so a mismatch — the usual cause of a
		// stale-config failure — is visible at a glance. A correctly upgraded
		// install regenerates the config (skywire autoconfig) on every update,
		// so config and visor versions should match; any gap means the config
		// was not regenerated and may be missing settings this release expects.
		log.WithField("config_version", conf.Version).
			WithField("visor_version", visorBuildInfo.Version).
			Info("Loaded config.")
		switch {
		case cVer.Major != vVer.Major:
			log.Warnf("config major version %d differs from the visor's %d — the config is incompatible; regenerate it with `skywire autoconfig`", cVer.Major, vVer.Major)
		case cVer.LT(vVer):
			log.Warnf("config version %s trails the visor %s — it may be missing settings from this release; regenerate it with `skywire autoconfig`", conf.Version, visorBuildInfo.Version)
		case cVer.GT(vVer):
			log.Warnf("config version %s is newer than the visor %s (downgrade?) — regenerate it with `skywire autoconfig` if the visor misbehaves", conf.Version, visorBuildInfo.Version)
		}
		if cVer.Major == vVer.Major {
			compat = true
		}
	} else {
		compat = true
	}
	conf.path = confPath
	return conf, compat, nil
}
