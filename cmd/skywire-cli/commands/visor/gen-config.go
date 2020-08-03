package visor

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"path"
	"path/filepath"

	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/restart"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"

	"github.com/skycoin/dmsg/cipher"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/util/pathutil"
	"github.com/skycoin/skywire/pkg/visor"
)

func init() {
	RootCmd.AddCommand(genConfigCmd)
}

var (
	sk            cipher.SecKey
	output        string
	replace       bool
	retainKeys    bool
	configLocType = pathutil.WorkingDirLoc
	testenv       bool
)

func init() {
	genConfigCmd.Flags().VarP(&sk, "secret-key", "s", "if unspecified, a random key pair will be generated.")
	genConfigCmd.Flags().StringVarP(&output, "output", "o", "", "path of output config file. Uses default of 'type' flag if unspecified.")
	genConfigCmd.Flags().BoolVarP(&replace, "replace", "r", false, "whether to allow rewrite of a file that already exists.")
	genConfigCmd.Flags().BoolVar(&retainKeys, "retain-keys", false, "retain current keys")
	genConfigCmd.Flags().VarP(&configLocType, "type", "m", fmt.Sprintf("config generation mode. Valid values: %v", pathutil.AllConfigLocationTypes()))
	genConfigCmd.Flags().BoolVarP(&testenv, "testing-environment", "t", false, "whether to use production or test deployment service.")
}

var genConfigCmd = &cobra.Command{
	Use:   "gen-config",
	Short: "Generates a config file",
	PreRun: func(_ *cobra.Command, _ []string) {
		if output == "" {
			output = pathutil.VisorDefaults().Get(configLocType)
			logger.Infof("No 'output' set; using default path: %s", output)
		}
		var err error
		if output, err = filepath.Abs(output); err != nil {
			logger.WithError(err).Fatalln("invalid output provided")
		}
	},
	Run: func(_ *cobra.Command, _ []string) {
		var conf *visor.Config
		switch configLocType {
		case pathutil.WorkingDirLoc:
			conf = defaultConfig()
		case pathutil.HomeLoc:
			conf = homeConfig()
		case pathutil.LocalLoc:
			conf = localConfig()
		default:
			logger.Fatalln("invalid config type:", configLocType)
		}
		if replace && retainKeys && pathutil.Exists(output) {
			if err := fillInOldKeys(output, conf); err != nil {
				logger.WithError(err).Fatalln("Error retaining old keys")
			}
		}
		pathutil.WriteJSONConfig(conf, output, replace)
	},
}

func fillInOldKeys(confPath string, conf *visor.Config) error {
	oldConfBytes, err := ioutil.ReadFile(path.Clean(confPath))
	if err != nil {
		return fmt.Errorf("error reading old config file: %w", err)
	}

	var oldConf visor.Config
	if err := json.Unmarshal(oldConfBytes, &oldConf); err != nil {
		return fmt.Errorf("invalid old configuration file: %w", err)
	}

	conf.KeyPair = oldConf.KeyPair

	return nil
}

func homeConfig() *visor.Config {
	c := defaultConfig()
	c.AppsPath = filepath.Join(pathutil.HomeDir(), ".skycoin/skywire/apps")
	c.Transport.LogStore.Location = filepath.Join(pathutil.HomeDir(), ".skycoin/skywire/transport_logs")
	return c
}

func localConfig() *visor.Config {
	c := defaultConfig()
	c.AppsPath = "/usr/local/skycoin/skywire/apps"
	c.Transport.LogStore.Location = "/usr/local/skycoin/skywire/transport_logs"
	return c
}

func defaultConfig() *visor.Config {
	conf := &visor.Config{}

	if sk.Null() {
		conf.KeyPair = visor.NewKeyPair()
	} else {
		conf.KeyPair = visor.RestoreKeyPair(sk)
	}

	stcp, err := visor.DefaultSTCPConfig()
	if err != nil {
		logger.Warn(err)
	} else {
		conf.STCP = stcp
	}

	conf.Dmsg = visor.DefaultDmsgConfig()

	ptyConf := defaultDmsgPtyConfig()
	conf.DmsgPty = &ptyConf

	// TODO(evanlinjin): We have disabled skysocks passcode by default for now - We should make a cli arg for this.
	//passcode := base64.StdEncoding.Strict().EncodeToString(cipher.RandByte(8))
	conf.Apps = []visor.AppConfig{
		defaultSkychatConfig(),
		defaultSkysocksConfig(""),
		defaultSkysocksClientConfig(),
	}

	conf.TrustedVisors = []cipher.PubKey{}

	conf.Transport = visor.DefaultTransportConfig()
	conf.Routing = visor.DefaultRoutingConfig()

	if testenv {
		conf.Dmsg.Discovery = skyenv.TestDmsgDiscAddr
		conf.Transport.Discovery = skyenv.TestTpDiscAddr
		conf.Routing.RouteFinder = skyenv.TestRouteFinderAddr
		conf.Routing.SetupNodes = []cipher.PubKey{skyenv.MustPK(skyenv.TestSetupPK)}
	}

	conf.Hypervisors = []visor.HypervisorConfig{}

	conf.UptimeTracker = visor.DefaultUptimeTrackerConfig()

	conf.AppsPath = visor.DefaultAppsPath
	conf.LocalPath = visor.DefaultLocalPath

	conf.LogLevel = visor.DefaultLogLevel
	conf.ShutdownTimeout = visor.DefaultTimeout

	conf.Interfaces = &visor.InterfaceConfig{
		RPCAddress: "localhost:3435",
	}

	conf.AppServerAddr = appcommon.DefaultServerAddr
	conf.RestartCheckDelay = restart.DefaultCheckDelay.String()

	return conf
}

func defaultDmsgPtyConfig() visor.DmsgPtyConfig {
	return visor.DmsgPtyConfig{
		Port:     skyenv.DmsgPtyPort,
		AuthFile: "./skywire/dmsgpty/whitelist.json",
		CLINet:   skyenv.DefaultDmsgPtyCLINet,
		CLIAddr:  skyenv.DefaultDmsgPtyCLIAddr,
	}
}

func defaultSkychatConfig() visor.AppConfig {
	return visor.AppConfig{
		App:       skyenv.SkychatName,
		AutoStart: true,
		Port:      routing.Port(skyenv.SkychatPort),
		Args:      []string{"-addr", skyenv.SkychatAddr},
	}
}

func defaultSkysocksConfig(passcode string) visor.AppConfig {
	var args []string
	if passcode != "" {
		args = []string{"-passcode", passcode}
	}
	return visor.AppConfig{
		App:       skyenv.SkysocksName,
		AutoStart: true,
		Port:      routing.Port(skyenv.SkysocksPort),
		Args:      args,
	}
}

func defaultSkysocksClientConfig() visor.AppConfig {
	return visor.AppConfig{
		App:       skyenv.SkysocksClientName,
		AutoStart: false,
		Port:      routing.Port(skyenv.SkysocksClientPort),
	}
}
