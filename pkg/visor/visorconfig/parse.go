package visorconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/skycoin/skycoin/src/util/logging"

	utilenv "github.com/skycoin/skywire-utilities/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skyenv"
)

var (
	// ErrInvalidSK occurs when config file has an invalid secret key.
	ErrInvalidSK = errors.New("config has invalid secret key")
)

// Parse parses the visor config from a given reader.
// If the config file is not the most recent version, it is upgraded and written back to 'path'.
func Parse(log *logging.MasterLogger, raw []byte, options *ParseOptions) (*V1, error) {

	cc, err := NewCommon(log, nil)
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(raw, cc); err != nil {
		return nil, fmt.Errorf("failed to obtain config version: %w", err)
	}
	return parseV1(cc, raw, options)
}

func parseV1(cc *Common, raw []byte, options *ParseOptions) (*V1, error) {
	conf := MakeBaseConfig(cc, options.TestEnv, options.DmsgHTTP, options.Services)
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&conf); err != nil {
		return nil, err
	}

	if err := conf.ensureKeys(); err != nil {
		return nil, fmt.Errorf("%v: %w", ErrInvalidSK, err)
	}
	conf = ensureAppDisc(conf)
	conf.Version = skyenv.Version()
	return conf, conf.flush(conf, options.Path)
}

func ensureAppDisc(conf *V1) *V1 {
	if conf.Launcher.ServiceDisc == "" {
		conf.Launcher.ServiceDisc = utilenv.ServiceDiscAddr
	}
	return conf
}

// ParseOptions is passed to Parse
type ParseOptions struct {
	Path     string
	TestEnv  bool
	DmsgHTTP bool
	Services *Services
}
