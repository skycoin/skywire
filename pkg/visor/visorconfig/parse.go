package visorconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/skycoin/src/util/logging"

	"github.com/SkycoinProject/skywire-mainnet/pkg/app/launcher"
	"github.com/SkycoinProject/skywire-mainnet/pkg/restart"
	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"
	"github.com/SkycoinProject/skywire-mainnet/pkg/skyenv"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet"
)

var (
	// ErrUnsupportedConfigVersion occurs when an unsupported config version is encountered.
	ErrUnsupportedConfigVersion = errors.New("unsupported config version")

	// ErrInvalidSK occurs when config file has an invalid secret key.
	ErrInvalidSK = errors.New("config has invalid secret key")
)

// Parse parses the visor config from a given reader.
// If the config file is not the most recent version, it is upgraded and written back to 'path'.
func Parse(log *logging.MasterLogger, path string, raw []byte) (*V1, error) {
	common, err := NewCommon(log, path, "", nil)
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(raw, common); err != nil {
		return nil, fmt.Errorf("failed to obtain config version: %w", err)
	}

	switch common.Version {
	case V0Name: // TODO
		return nil, ErrUnsupportedConfigVersion

	case V1Name: // Current version.
		conf := MakeBaseConfig(common)

		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&conf); err != nil {
			return nil, err
		}

		if _, err := conf.ensureKeys(); err != nil {
			return nil, fmt.Errorf("%v: %w", ErrInvalidSK, err)
		}
		return conf, conf.flush()

	default:
		return nil, ErrUnsupportedConfigVersion
	}
}

// MakeBaseConfig returns a visor config with 'enforced' fields only.
// This is used as default values if no config is given, or for missing *required* fields.
// This function always returns the latest config version.
func MakeBaseConfig(common *Common) *V1 {
	conf := new(V1)
	conf.Common = common
	conf.Dmsg = &snet.DmsgConfig{
		Discovery:     skyenv.DefaultDmsgDiscAddr,
		SessionsCount: 1,
	}
	conf.Transport = &V1Transport{
		Discovery: skyenv.DefaultTpDiscAddr,
		LogStore: &V1LogStore{
			Type: "memory",
		},
	}
	conf.Routing = &V1Routing{
		SetupNodes:         []cipher.PubKey{skyenv.MustPK(skyenv.DefaultSetupPK)},
		RouteFinder:        skyenv.DefaultRouteFinderAddr,
		RouteFinderTimeout: DefaultTimeout,
	}
	conf.Launcher = &V1Launcher{
		Discovery:  nil,
		Apps:       nil,
		ServerAddr: skyenv.DefaultAppSrvAddr,
		BinPath:    skyenv.DefaultAppBinPath,
		LocalPath:  skyenv.DefaultAppLocalPath,
	}
	conf.CLIAddr = skyenv.DefaultRPCAddr
	conf.LogLevel = skyenv.DefaultLogLevel
	conf.ShutdownTimeout = DefaultTimeout
	conf.RestartCheckDelay = restart.DefaultCheckDelay.String() // TODO: Use Duration type.
	return conf
}

// MakeDefaultConfig returns the default visor config from a given secret key (if specified).
// The config's 'sk' field will be nil if not specified.
// Generated config will be saved to 'confPath'.
// This function always returns the latest config version.
func MakeDefaultConfig(common *Common, sk *cipher.SecKey) (*V1, error) {
	conf := MakeBaseConfig(common)
	if sk != nil {
		conf.SK = *sk
	}
	if _, err := conf.ensureKeys(); err != nil {
		return nil, err
	}
	conf.Dmsgpty = &V1Dmsgpty{
		Port:     skyenv.DmsgPtyPort,
		AuthFile: skyenv.DefaultDmsgPtyWhitelist,
		CLINet:   skyenv.DefaultDmsgPtyCLINet,
		CLIAddr:  skyenv.DefaultDmsgPtyCLIAddr,
	}
	conf.STCP = &snet.STCPConfig{
		LocalAddr: skyenv.DefaultSTCPAddr,
		PKTable:   nil,
	}
	conf.Transport.LogStore = &V1LogStore{
		Type:     "file",
		Location: skyenv.DefaultTpLogStore,
	}
	conf.UptimeTracker = &V1UptimeTracker{
		Addr: skyenv.DefaultUptimeTrackerAddr,
	}
	conf.Launcher.Discovery = &V1AppDisc{
		UpdateInterval: Duration(skyenv.AppDiscUpdateInterval),
		ProxyDisc:      skyenv.DefaultProxyDiscAddr,
	}
	conf.Launcher.Apps = []launcher.AppConfig{
		{
			Name:      skyenv.SkychatName,
			AutoStart: true,
			Port:      routing.Port(skyenv.SkychatPort),
			Args:      []string{"-addr", skyenv.SkychatAddr},
		},
		{
			Name:      skyenv.SkysocksName,
			AutoStart: true,
			Port:      routing.Port(skyenv.SkysocksPort),
		},
		{
			Name:      skyenv.SkysocksClientName,
			AutoStart: false,
			Port:      routing.Port(skyenv.SkysocksClientPort),
		},
		{
			Name:      skyenv.VPNServerName,
			AutoStart: true,
			Port:      routing.Port(skyenv.VPNServerPort),
		},
		{
			Name:      skyenv.VPNClientName,
			AutoStart: false,
			Port:      routing.Port(skyenv.VPNClientPort),
		},
	}
	return conf, nil
}
