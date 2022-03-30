package visorconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire-utilities/pkg/skyenv"
)

var (
	// ErrUnsupportedConfigVersion occurs when an unsupported config version is encountered.
	ErrUnsupportedConfigVersion = errors.New("unsupported config version")

	// ErrInvalidSK occurs when config file has an invalid secret key.
	ErrInvalidSK = errors.New("config has invalid secret key")
)

// Parse parses the visor config from a given reader.
// If the config file is not the most recent version, it is upgraded and written back to 'path'.
func Parse(log *logging.MasterLogger, path string, raw []byte, testEnv bool, dmsgHTTP bool, services *Services) (*V1, error) {
	cc, err := NewCommon(log, path, "", nil)
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(raw, cc); err != nil {
		return nil, fmt.Errorf("failed to obtain config version: %w", err)
	}
	return parseV1(cc, raw, testEnv, dmsgHTTP, services)
}

func parseV1(cc *Common, raw []byte, testEnv bool, dmsgHTTP bool, services *Services) (*V1, error) {
	conf := MakeBaseConfig(cc, testEnv, dmsgHTTP, services)
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&conf); err != nil {
		return nil, err
	}

	if err := conf.ensureKeys(); err != nil {
		return nil, fmt.Errorf("%v: %w", ErrInvalidSK, err)
	}
	conf = ensureAppDisc(conf)
	conf = updateUrls(conf, services)
	conf.Version = V1Name
	return conf, conf.flush(conf)
}

func ensureAppDisc(conf *V1) *V1 {
	if conf.Launcher.ServiceDisc == "" {
		conf.Launcher.ServiceDisc = skyenv.DefaultServiceDiscAddr
	}
	return conf
}

func updateUrls(conf *V1, services *Services) *V1 {
	//	if conf.Dmsg.Discovery == skyenv.OldDefaultDmsgDiscAddr {
	conf.Dmsg.Discovery = services.DmsgDiscovery
	//	}
	//	if conf.Transport.Discovery == skyenv.OldDefaultTpDiscAddr {
	conf.Transport.Discovery = services.TransportDiscovery
	//	}
	//	if conf.Transport.AddressResolver == skyenv.OldDefaultAddressResolverAddr {
	conf.Transport.AddressResolver = services.AddressResolver
	//	}
	//	if conf.Routing.RouteFinder == skyenv.OldDefaultRouteFinderAddr {
	conf.Routing.RouteFinder = services.RouteFinder
	//	}
	//	if conf.UptimeTracker.Addr == skyenv.OldDefaultUptimeTrackerAddr {
	conf.UptimeTracker.Addr = services.UptimeTracker
	//	}
	//	if conf.Launcher.ServiceDisc == skyenv.OldDefaultServiceDiscAddr {
	conf.Launcher.ServiceDisc = services.ServiceDiscovery
	//	}
	return conf
}
