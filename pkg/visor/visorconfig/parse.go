package visorconfig

import (
	"errors"
	"io"
	"strings"

	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/pkg/skyenv"
)

var (
	// ErrInvalidSK occurs when config file has an invalid secret key.
	ErrInvalidSK = errors.New("config has invalid secret key")
)

// Parse parses the visor config from a given reader.
// The config version is checked against the visor's version and if not the same we send back the
// error as well as compat(compatibility) as false.
func Parse(log *logging.Logger, r io.Reader) (conf *V1, compat bool, err error) {

	conf, err = Reader(r)
	if err != nil {
		return nil, compat, err
	}
	log.WithField("config version: ", conf.Version).Info()

	// we check if the version of the visor and config are the same
	if (conf.Version != "unknown") && (skyenv.BuildInfo.Version != "unknown") {
		compat = strings.Contains(strings.Split(skyenv.BuildInfo.Version, "-")[0], strings.Split(conf.Version, "-")[0])
	}
	return conf, compat, nil
}
