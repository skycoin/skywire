package visorconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
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
	cc, err := NewCommon(log, path, "", nil)
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(raw, cc); err != nil {
		return nil, fmt.Errorf("failed to obtain config version: %w", err)
	}

	switch cc.Version {
	case V1Name: // Current version.
		return parseV1(cc, raw)
	case V0Name, V0NameOldFormat, "":
		return parseV0(cc, raw)
	default:
		return nil, ErrUnsupportedConfigVersion
	}
}

func parseV1(cc *Common, raw []byte) (*V1, error) {
	conf := MakeBaseConfig(cc)

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&conf); err != nil {
		return nil, err
	}

	if err := conf.ensureKeys(); err != nil {
		return nil, fmt.Errorf("%v: %w", ErrInvalidSK, err)
	}
	return conf, conf.flush(conf)
}

func parseV0(cc *Common, raw []byte) (*V1, error) {
	// Unmarshal old config.
	var old V0
	if err := json.Unmarshal(raw, &old); err != nil {
		return nil, fmt.Errorf("failed to unmarshal old config of version '%s': %w", cc.Version, err)
	}

	// Extract keys from old config and save it in Common.
	sk := old.KeyPair.SecKey
	if sk.Null() {
		return nil, fmt.Errorf("old config of version '%s' has no secret key defined", cc.Version)
	}

	pk, err := sk.PubKey()
	if err != nil {
		return nil, fmt.Errorf("old config of version '%s' has invalid secret key: %w", cc.Version, err)
	}

	cc.SK = sk
	cc.PK = pk

	// generate for all apps as a default
	genAppConfig := make(map[string]bool, len(appDefaultConfigs))
	for appName := range appDefaultConfigs {
		genAppConfig[appName] = true
	}

	// Start with default config as template.
	conf, err := defaultConfigFromCommon(cc, false, genAppConfig)
	if err != nil {
		return nil, err
	}

	// Fill config with old values.
	if old.Dmsg != nil {
		conf.Dmsg = old.Dmsg
	}

	if old.DmsgPty != nil {
		conf.Dmsgpty = old.DmsgPty
	}

	if old.STCP != nil {
		conf.STCP = old.STCP
	}

	if old.Transport != nil {
		conf.Transport.Discovery = old.Transport.Discovery
		conf.Transport.LogStore = old.Transport.LogStore
	}

	conf.Transport.TrustedVisors = old.TrustedVisors
	if old.Routing != nil {
		conf.Routing = old.Routing
	}

	if old.UptimeTracker != nil {
		conf.UptimeTracker = old.UptimeTracker
	}

	conf.Launcher.Apps = make([]launcher.AppConfig, len(old.Apps))
	for i, oa := range old.Apps {
		conf.Launcher.Apps[i] = launcher.AppConfig{
			Name:      oa.App,
			Args:      oa.Args,
			AutoStart: oa.AutoStart,
			Port:      oa.Port,
		}
	}

	vpnApps := []launcher.AppConfig{
		{
			Name:      skyenv.VPNServerName,
			AutoStart: false,
			Port:      routing.Port(skyenv.VPNServerPort),
		},
		{
			Name:      skyenv.VPNClientName,
			AutoStart: false,
			Port:      routing.Port(skyenv.VPNClientPort),
		},
	}

	conf.Launcher.Apps = append(conf.Launcher.Apps, vpnApps...)

	conf.Launcher.BinPath = old.AppsPath
	conf.Launcher.LocalPath = old.LocalPath
	conf.Launcher.ServerAddr = old.AppServerAddr

	for _, hv := range old.Hypervisors {
		conf.Hypervisors = append(conf.Hypervisors, hv.PubKey)
	}

	if old.Interfaces != nil {
		conf.CLIAddr = old.Interfaces.RPCAddress
	}

	conf.LogLevel = old.LogLevel
	conf.ShutdownTimeout = old.ShutdownTimeout
	conf.RestartCheckDelay = old.RestartCheckDelay

	return conf, conf.flush(conf)
}
