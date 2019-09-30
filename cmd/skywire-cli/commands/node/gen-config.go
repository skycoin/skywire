package node

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/SkycoinProject/skywire-mainnet/internal/skyenv"
	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"

	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/spf13/cobra"

	"github.com/SkycoinProject/skywire-mainnet/pkg/util/pathutil"
	"github.com/SkycoinProject/skywire-mainnet/pkg/visor"
)

func init() {
	RootCmd.AddCommand(genConfigCmd)
}

var (
	output        string
	replace       bool
	configLocType = pathutil.WorkingDirLoc
)

func init() {
	genConfigCmd.Flags().StringVarP(&output, "output", "o", "", "path of output config file. Uses default of 'type' flag if unspecified.")
	genConfigCmd.Flags().BoolVarP(&replace, "replace", "r", false, "whether to allow rewrite of a file that already exists.")
	genConfigCmd.Flags().VarP(&configLocType, "type", "m", fmt.Sprintf("config generation mode. Valid values: %v", pathutil.AllConfigLocationTypes()))
}

var genConfigCmd = &cobra.Command{
	Use:   "gen-config",
	Short: "Generates a config file",
	PreRun: func(_ *cobra.Command, _ []string) {
		if output == "" {
			output = pathutil.NodeDefaults().Get(configLocType)
			log.Infof("No 'output' set; using default path: %s", output)
		}
		var err error
		if output, err = filepath.Abs(output); err != nil {
			log.WithError(err).Fatalln("invalid output provided")
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
			log.Fatalln("invalid config type:", configLocType)
		}
		pathutil.WriteJSONConfig(conf, output, replace)
	},
}

func homeConfig() *visor.Config {
	c := defaultConfig()
	c.AppsPath = filepath.Join(pathutil.HomeDir(), ".skycoin/skywire/apps")
	c.Transport.LogStore.Location = filepath.Join(pathutil.HomeDir(), ".skycoin/skywire/transport_logs")
	c.Routing.Table.Location = filepath.Join(pathutil.HomeDir(), ".skycoin/skywire/routing.db")
	return c
}

func localConfig() *visor.Config {
	c := defaultConfig()
	c.AppsPath = "/usr/local/skycoin/skywire/apps"
	c.Transport.LogStore.Location = "/usr/local/skycoin/skywire/transport_logs"
	c.Routing.Table.Location = "/usr/local/skycoin/skywire/routing.db"
	return c
}

func defaultConfig() *visor.Config {
	conf := &visor.Config{}
	conf.Version = "1.0"

	pk, sk := cipher.GenerateKeyPair()
	conf.Node.StaticPubKey = pk
	conf.Node.StaticSecKey = sk

	conf.Messaging.Discovery = skyenv.DefaultDmsgDiscAddr
	conf.Messaging.ServerCount = 1

	// TODO(evanlinjin): We have disabled skyproxy passcode by default for now - We should make a cli arg for this.
	//passcode := base64.StdEncoding.Strict().EncodeToString(cipher.RandByte(8))
	conf.Apps = []visor.AppConfig{
		defaultSkychatConfig(),
		defaultSkysshConfig(),
		defaultSkyproxyConfig(""),
		defaultSkysshClientConfig(),
		defaultSkyproxyClientConfig(),
	}
	conf.TrustedNodes = []cipher.PubKey{}

	conf.Transport.Discovery = skyenv.DefaultTpDiscAddr
	conf.Transport.LogStore.Type = "file"
	conf.Transport.LogStore.Location = "./skywire/transport_logs"

	conf.Routing.RouteFinder = skyenv.DefaultRouteFinderAddr

	var sPK cipher.PubKey
	if err := sPK.UnmarshalText([]byte(skyenv.DefaultSetupPK)); err != nil {
		log.WithError(err).Warnf("Failed to unmarshal default setup node public key %s", skyenv.DefaultSetupPK)
	}
	conf.Routing.SetupNodes = []cipher.PubKey{sPK}
	conf.Routing.Table.Type = "boltdb"
	conf.Routing.Table.Location = "./skywire/routing.db"
	conf.Routing.RouteFinderTimeout = visor.Duration(10 * time.Second)

	conf.Hypervisors = []visor.HypervisorConfig{}

	conf.Uptime.Tracker = ""

	conf.AppsPath = "./apps"
	conf.LocalPath = "./local"

	conf.LogLevel = "info"

	conf.ShutdownTimeout = visor.Duration(10 * time.Second)

	conf.Interfaces.RPCAddress = "localhost:3435"

	return conf
}

func defaultSkychatConfig() visor.AppConfig {
	return visor.AppConfig{
		App:       skyenv.SkychatName,
		Version:   skyenv.SkychatVersion,
		AutoStart: true,
		Port:      routing.Port(skyenv.SkychatPort),
		Args:      []string{"-addr", skyenv.SkychatAddr},
	}
}

func defaultSkysshConfig() visor.AppConfig {
	return visor.AppConfig{
		App:       skyenv.SkysshName,
		Version:   skyenv.SkysshVersion,
		AutoStart: true,
		Port:      routing.Port(skyenv.SkysshPort),
	}
}

func defaultSkyproxyConfig(passcode string) visor.AppConfig {
	var args []string
	if passcode != "" {
		args = []string{"-passcode", passcode}
	}
	return visor.AppConfig{
		App:       skyenv.SkyproxyName,
		Version:   skyenv.SkyproxyVersion,
		AutoStart: false,
		Port:      routing.Port(skyenv.SkyproxyPort),
		Args:      args,
	}
}

func defaultSkysshClientConfig() visor.AppConfig {
	return visor.AppConfig{
		App:       skyenv.SkysshClientName,
		Version:   skyenv.SkysshVersion,
		AutoStart: true,
		Port:      routing.Port(skyenv.SkysshClientPort),
	}
}

func defaultSkyproxyClientConfig() visor.AppConfig {
	return visor.AppConfig{
		App:       skyenv.SkyproxyClientName,
		Version:   skyenv.SkyproxyClientVersion,
		AutoStart: false,
		Port:      routing.Port(skyenv.SkyproxyClientPort),
	}
}
