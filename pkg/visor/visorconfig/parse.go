package visorconfig

import (
	"errors"
	"io"
	"strings"

	"github.com/blang/semver/v4"
	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
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
	if conf.Version != "unknown" {
		log.WithField("config version: ", conf.Version).Info()
	}
	// we check if the version of the visor and config are the same
	if (conf.Version != "unknown") && (visorBuildInfo.Version != "unknown") {
		v1, err := semver.Make(strings.TrimPrefix(conf.Version, "v"))
		if err != nil {
			return conf, compat, err
		}
		v2, err := semver.Make(strings.TrimPrefix(visorBuildInfo.Version, "v"))
		if err != nil {
			return conf, compat, err
		}
		if v1.Major == v2.Major {
			compat = true
		}
	} else {
		compat = true
	}
	/*
			cver := strings.Split(visorBuildInfo.Version, "-")[0] //v0.6.0
			cver0 := strings.Split(cver, ".")[0]                  //v0
			cver1 := strings.Split(cver, ".")[1]                  //6
			vver := strings.Split(conf.Version, "-")[0]           //v0.6.0
			vver0 := strings.Split(vver, ".")[0]                  //v0
			vver1 := strings.Split(vver, ".")[1]                  //6
			compat = strings.Contains(vver0, cver0)
			if compat {
				compat = strings.Contains(vver1, cver1)
			}
		}
	*/
	return conf, compat, nil
}
