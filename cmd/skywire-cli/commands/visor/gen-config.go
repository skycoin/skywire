package visor

import (
	"errors"
	"fmt"
	"net"
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
	testenv       bool
)

func init() {
	genConfigCmd.Flags().StringVarP(&output, "output", "o", "", "path of output config file. Uses default of 'type' flag if unspecified.")
	genConfigCmd.Flags().BoolVarP(&replace, "replace", "r", false, "whether to allow rewrite of a file that already exists.")
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
		pathutil.WriteJSONConfig(conf, output, replace)
	},
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
	conf.Version = "1.0"

	pk, sk := cipher.GenerateKeyPair()
	conf.Visor.StaticPubKey = pk
	conf.Visor.StaticSecKey = sk

	lIPaddr, err := getLocalIPAddress()
	if err != nil {
		logger.Warn(err)
	}

	conf.STCP.LocalAddr = lIPaddr

	if testenv {
		conf.Dmsg.Discovery = skyenv.TestDmsgDiscAddr
	} else {
		conf.Dmsg.Discovery = skyenv.DefaultDmsgDiscAddr
	}
	conf.Dmsg.SessionsCount = 1

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

	if testenv {
		conf.Transport.Discovery = skyenv.TestTpDiscAddr
	} else {
		conf.Transport.Discovery = skyenv.DefaultTpDiscAddr
	}

	conf.Transport.LogStore.Type = "file"
	conf.Transport.LogStore.Location = "./skywire/transport_logs"

	if testenv {
		conf.Routing.RouteFinder = skyenv.TestRouteFinderAddr
	} else {
		conf.Routing.RouteFinder = skyenv.DefaultRouteFinderAddr
	}

	var sPK cipher.PubKey
	if err := sPK.UnmarshalText([]byte(skyenv.DefaultSetupPK)); err != nil {
		logger.WithError(err).Warnf("Failed to unmarshal default setup-node public key %s", skyenv.DefaultSetupPK)
	}
	conf.Routing.SetupNodes = []cipher.PubKey{sPK}
	conf.Routing.RouteFinderTimeout = visor.Duration(10 * time.Second)

	conf.Hypervisors = []visor.HypervisorConfig{}

	conf.Uptime.Tracker = ""

	conf.AppsPath = "./apps"
	conf.LocalPath = "./local"

	conf.LogLevel = "info"

	conf.ShutdownTimeout = visor.Duration(10 * time.Second)

	conf.Interfaces.RPCAddress = "localhost:3435"

	conf.AppServerSockFile = "/tmp/visor_" + pk.Hex() + ".sock"

	conf.AutoStartSpd = false

	return conf
}

func defaultDmsgPtyConfig() visor.DmsgPtyConfig {
	return visor.DmsgPtyConfig{
		Port:     skyenv.DefaultDmsgPtyPort,
		AuthFile: "./skywire/dmsgpty/whitelist.json",
		CLINet:   skyenv.DefaultDmsgPtyCLINet,
		CLIAddr:  skyenv.DefaultDmsgPtyCLIAddr,
	}
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

func defaultSkysocksConfig(passcode string) visor.AppConfig {
	var args []string
	if passcode != "" {
		args = []string{"-passcode", passcode}
	}
	return visor.AppConfig{
		App:       skyenv.SkysocksName,
		Version:   skyenv.SkysocksVersion,
		AutoStart: true,
		Port:      routing.Port(skyenv.SkysocksPort),
		Args:      args,
	}
}

func defaultSkysocksClientConfig() visor.AppConfig {
	return visor.AppConfig{
		App:       skyenv.SkysocksClientName,
		Version:   skyenv.SkysocksClientVersion,
		AutoStart: false,
		Port:      routing.Port(skyenv.SkysocksClientPort),
	}
}

func getLocalIPAddress() (string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}

	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String() + ":7777", nil
			}
		}
	}
	return "", errors.New("could not find local IP address")
}
