// Package visorconfig pkg/visor/visorconfig/parse.go
package visorconfig

import (
	"errors"
	"io"
	"strings"

	"github.com/blang/semver/v4"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/logging"
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
		return nil, compat, err
	}
	// we check if the version of the visor and config are the same
	if (conf.Version != "unknown") && (visorBuildInfo.Version != "unknown") {
		cVer, err := semver.Make(strings.TrimPrefix(conf.Version, "v"))
		if err != nil {
			return conf, compat, err
		}
		vVer, err := semver.Make(strings.TrimPrefix(visorBuildInfo.Version, "v"))
		if err != nil {
			return conf, compat, err
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
