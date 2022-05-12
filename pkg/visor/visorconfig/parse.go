package visorconfig

import (
	"errors"
	"io"

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
	// we check if the version of the config is the latest
	if conf.Version == V1Name {
		compat = true
	}
	conf.path = confPath
	return conf, compat, nil
}
