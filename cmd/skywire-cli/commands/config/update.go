package cliconfig

import (
	"encoding/json"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
	coinCipher "github.com/skycoin/skycoin/src/cipher"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/dmsgc"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func init() {
	usrLvl, err := user.Current()
	if err != nil {
		panic(err)
	}
	if usrLvl.Username == "root" {
		isRoot = true
	}
	RootCmd.AddCommand(updateCmd)

	updateCmd.Flags().SortFlags = false
	updateCmd.Flags().BoolVarP(&isUpdateEndpoints, "endpoints", "a", false, "update server endpoints")
	updateCmd.Flags().StringVar(&logLevel, "log-level", "", "level of logging in config")
	updateCmd.Flags().StringVarP(&serviceConfURL, "url", "b", "", "service config URL: "+svcconf)
	updateCmd.Flags().BoolVarP(&isTestEnv, "testenv", "t", false, "use test deployment: "+testconf)
	updateCmd.Flags().StringVar(&setPublicAutoconnect, "public-autoconn", "", "change public autoconnect configuration")
	updateCmd.Flags().IntVar(&minHops, "set-minhop", -1, "change min hops value")
	updateCmd.PersistentFlags().StringVarP(&input, "input", "i", "", "path of input config file.")
	uhiddenflags = append(uhiddenflags, "input")
	updateCmd.PersistentFlags().StringVarP(&output, "output", "o", "", "config file to output")
	if isRoot {
		if _, err := os.Stat(skyenv.SkywirePath + "/" + skyenv.Configjson); err == nil {
			updateCmd.PersistentFlags().BoolVarP(&isPkg, "pkg", "p", false, "update package config "+skyenv.SkywirePath+"/"+skyenv.Configjson)
			uhiddenflags = append(uhiddenflags, "pkg")
		}
	}
	if !isRoot {
		if _, err := os.Stat(skyenv.HomePath() + "/" + skyenv.ConfigName); err == nil {
			updateCmd.PersistentFlags().BoolVarP(&isUsr, "user", "u", false, "update config at: $HOME/"+skyenv.ConfigName)
		}
	}

	for _, j := range uhiddenflags {
		updateCmd.Flags().MarkHidden(j) //nolint
	}

	updateCmd.AddCommand(hyperVisorUpdateCmd)
	hyperVisorUpdateCmd.Flags().SortFlags = false
	hyperVisorUpdateCmd.Flags().StringVarP(&addHypervisorPKs, "add-pks", "+", "", "public keys of hypervisors that should be added to this visor")
	hyperVisorUpdateCmd.Flags().BoolVarP(&isResetHypervisor, "reset", "r", false, "resets hypervisor configuration")

	updateCmd.AddCommand(skySocksClientUpdateCmd)
	skySocksClientUpdateCmd.Flags().SortFlags = false
	skySocksClientUpdateCmd.Flags().StringVarP(&addSkysocksClientSrv, "add-server", "+", "", "add skysocks server address to skysock-client")
	skySocksClientUpdateCmd.Flags().BoolVarP(&isResetSkysocksClient, "reset", "r", false, "reset skysocks-client configuration")

	updateCmd.AddCommand(skySocksServerUpdateCmd)
	skySocksServerUpdateCmd.Flags().SortFlags = false
	skySocksServerUpdateCmd.Flags().StringVarP(&skysocksPasscode, "passwd", "s", "", "add passcode to skysocks server")
	skySocksServerUpdateCmd.Flags().BoolVarP(&isResetSkysocks, "reset", "r", false, "reset skysocks configuration")

	updateCmd.AddCommand(vpnClientUpdateCmd)
	vpnClientUpdateCmd.Flags().SortFlags = false
	vpnClientUpdateCmd.Flags().StringVarP(&setVPNClientKillswitch, "killsw", "x", "", "change killswitch status of vpn-client")
	vpnClientUpdateCmd.Flags().StringVar(&addVPNClientSrv, "add-server", "", "add server address to vpn-client")
	vpnClientUpdateCmd.Flags().StringVarP(&addVPNClientPasscode, "pass", "s", "", "add passcode of server if needed")
	vpnClientUpdateCmd.Flags().BoolVarP(&isResetVPNclient, "reset", "r", false, "reset vpn-client configurations")

	updateCmd.AddCommand(vpnServerUpdateCmd)
	vpnServerUpdateCmd.Flags().SortFlags = false
	vpnServerUpdateCmd.Flags().StringVarP(&addVPNServerPasscode, "passwd", "s", "", "add passcode to vpn-server")
	vpnServerUpdateCmd.Flags().StringVar(&setVPNServerSecure, "secure", "", "change secure mode status of vpn-server")
	vpnServerUpdateCmd.Flags().StringVar(&setVPNServerAutostart, "autostart", "", "change autostart of vpn-server")
	vpnServerUpdateCmd.Flags().StringVar(&setVPNServerNetIfc, "netifc", "", "set default network interface")
	vpnServerUpdateCmd.Flags().BoolVarP(&isResetVPNServer, "reset", "r", false, "reset vpn-server configurations")
}

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update a config file",
	PreRun: func(_ *cobra.Command, _ []string) {
		if isUpdateEndpoints && (serviceConfURL == "") {
			if !isTestEnv {
				serviceConfURL = svcconf
			} else {
				serviceConfURL = testconf
			}
		}
		setDefaults()
		checkConfig()
	},
	Run: func(cmd *cobra.Command, _ []string) {
		if cmd.Flags().Changed("serviceConfURL") {
			isUpdateEndpoints = true
		}
		conf = initUpdate()
		if isUpdateEndpoints {
			if isTestEnv {
				serviceConfURL = testconf
			}
			mLog := logging.NewMasterLogger()
			mLog.SetLevel(logrus.InfoLevel)
			services := visorconfig.Fetch(mLog, serviceConfURL, isStdout)
			conf.Dmsg = &dmsgc.DmsgConfig{
				Discovery: services.DmsgDiscovery, //utilenv.DefaultDmsgDiscAddr,
			}
			conf.Transport = &visorconfig.Transport{
				Discovery:       services.TransportDiscovery, //utilenv.DefaultTpDiscAddr,
				AddressResolver: services.AddressResolver,    //utilenv.DefaultAddressResolverAddr,
			}
			conf.Routing = &visorconfig.Routing{
				RouteFinder: services.RouteFinder, //utilenv.DefaultRouteFinderAddr,
				SetupNodes:  services.SetupNodes,  //[]cipher.PubKey{utilenv.MustPK(utilenv.DefaultSetupPK)},
			}
			conf.Launcher = &visorconfig.Launcher{
				ServiceDisc: services.ServiceDiscovery, //utilenv.DefaultServiceDiscAddr,
			}
			conf.UptimeTracker = &visorconfig.UptimeTracker{
				Addr: services.UptimeTracker, //utilenv.DefaultUptimeTrackerAddr,
			}
			conf.StunServers = services.StunServers //utilenv.GetStunServers()
		}

		if conf.LogLevel != logLevel {
			if logLevel == "trace" || logLevel == "debug" || logLevel == "info" {
				conf.LogLevel = logLevel
			}
		}

		switch setPublicAutoconnect {
		case "true":
			conf.Transport.PublicAutoconnect = true
		case "false":
			conf.Transport.PublicAutoconnect = false
		case "":
			break
		default:
			logger.Fatal("Unrecognized public autoconnect value: ", setPublicAutoconnect)
		}
		if minHops >= 0 {
			conf.Routing.MinHops = uint16(minHops)
		}
		saveConfig(conf)
	},
}

func changeAppsConfig(conf *visorconfig.V1, appName string, argName string, argValue string) {
	apps := conf.Launcher.Apps
	for index := range apps {
		if apps[index].Name != appName {
			continue
		}
		updated := false
		for ind, arg := range apps[index].Args {
			if arg == argName {
				apps[index].Args[ind+1] = argValue
				updated = true
			}
		}
		if !updated {
			apps[index].Args = append(apps[index].Args, argName, argValue)
		}
	}
}

func resetAppsConfig(conf *visorconfig.V1, appName string) {
	apps := conf.Launcher.Apps
	for index := range apps {
		if apps[index].Name == appName {
			apps[index].Args = []string{}
		}
	}
}

func saveConfig(conf *visorconfig.V1) {
	// Save config to file.
	if err := conf.Flush(); err != nil {
		logger.WithError(err).Fatal("Failed to flush config to file.")
	}

	// Print results.
	j, err := json.MarshalIndent(conf, "", "\t")
	if err != nil {
		logger.WithError(err).Fatal("Could not unmarshal json.")
	}
	logger.Infof("Updated file '%s' to: %s", output, j)
}

func initUpdate() (conf *visorconfig.V1) {
	mLog := logging.NewMasterLogger()
	mLog.SetLevel(logrus.InfoLevel)
	if input == "" {
		input = output
	}
	conf, ok := visorconfig.ReadFile(input)
	if ok != nil {
		mLog.WithError(ok).Fatal("Failed to parse config.")
	}
	cc, err := visorconfig.NewCommon(mLog, output, &conf.SK)
	if err != nil {
		mLog.WithError(ok).Fatal("Failed to regenerate config.")
	}
	conf.Common = cc
	return conf
}

func checkConfig() {
	//set default output filename
	if output == "" {
		output = skyenv.ConfigName
	}
	var err error
	if output, err = filepath.Abs(output); err != nil {
		logger.WithError(err).Fatal("Invalid config output.")
	}
	if _, err := os.Stat(output); err != nil {
		logger.WithError(err).Fatal("Invalid config output.")
	}
	if (input != output) && (input != "") {
		if input, err = filepath.Abs(input); err != nil {
			logger.WithError(err).Fatal("Invalid config input.")
		}
		if _, err := os.Stat(input); err != nil {
			logger.WithError(err).Fatal("Invalid config input.")
		}
	}
}

func setDefaults() {
	if (input != "") && (output == "") {
		output = input
	}
	if isPkg {
		output = skyenv.SkywirePath + "/" + skyenv.Configjson
		input = skyenv.SkywirePath + "/" + skyenv.Configjson
	}
	if isUsr {
		output = skyenv.HomePath() + "/" + skyenv.ConfigName
		input = skyenv.HomePath() + "/" + skyenv.ConfigName
	}

}

var hyperVisorUpdateCmd = &cobra.Command{
	Use:   "hv",
	Short: "update hypervisor config",
	PreRun: func(_ *cobra.Command, _ []string) {
		setDefaults()
		checkConfig()
	},
	Run: func(_ *cobra.Command, _ []string) {
		conf = initUpdate()
		if addHypervisorPKs != "" {
			keys := strings.Split(addHypervisorPKs, ",")
			for _, key := range keys {
				keyParsed, err := coinCipher.PubKeyFromHex(strings.TrimSpace(key))
				if err != nil {
					logger.WithError(err).Fatalf("Failed to parse hypervisor private key: %s.", key)
				}
				conf.Hypervisors = append(conf.Hypervisors, cipher.PubKey(keyParsed))
			}
		}
		if isResetHypervisor {
			conf.Hypervisors = []cipher.PubKey{}
		}
		saveConfig(conf)
	},
}

var skySocksClientUpdateCmd = &cobra.Command{
	Use:   "sc",
	Short: "update skysocks-client config",
	PreRun: func(_ *cobra.Command, _ []string) {
		setDefaults()
		checkConfig()
	},
	Run: func(_ *cobra.Command, _ []string) {
		conf = initUpdate()
		if addSkysocksClientSrv != "" {
			keyParsed, err := coinCipher.PubKeyFromHex(strings.TrimSpace(addSkysocksClientSrv))
			if err != nil {
				logger.WithError(err).Fatalf("Failed to parse hypervisor private key: %s.", addSkysocksClientSrv)
			}
			changeAppsConfig(conf, "skysocks-client", "--srv", keyParsed.Hex())
		}
		if isResetSkysocksClient {
			resetAppsConfig(conf, "skysocks-client")
		}
		saveConfig(conf)
	},
}

var skySocksServerUpdateCmd = &cobra.Command{
	Use:   "ss",
	Short: "update skysocks-server config",
	PreRun: func(_ *cobra.Command, _ []string) {
		setDefaults()
		checkConfig()
	},
	Run: func(_ *cobra.Command, _ []string) {

		conf = initUpdate()
		if skysocksPasscode != "" {
			changeAppsConfig(conf, "skysocks", "--passcode", skysocksPasscode)
		}
		if isResetSkysocks {
			resetAppsConfig(conf, "skysocks")
		}
		saveConfig(conf)
	},
}

var vpnClientUpdateCmd = &cobra.Command{
	Use:   "vpnc",
	Short: "update vpn-client config",
	PreRun: func(_ *cobra.Command, _ []string) {
		setDefaults()
		checkConfig()
	},
	Run: func(_ *cobra.Command, _ []string) {
		conf = initUpdate()
		switch setVPNClientKillswitch {
		case "true":
			changeAppsConfig(conf, "vpn-client", "--killswitch", setVPNClientKillswitch)
		case "false":
			changeAppsConfig(conf, "vpn-client", "--killswitch", setVPNClientKillswitch)
		case "":
			break
		default:
			logger.Fatal("Unrecognized killswitch value: ", setVPNClientKillswitch)
		}
		if addVPNClientSrv != "" {
			keyParsed, err := coinCipher.PubKeyFromHex(strings.TrimSpace(addVPNClientSrv))
			if err != nil {
				logger.WithError(err).Fatalf("Failed to parse hypervisor private key: %s.", addVPNClientSrv)
			}
			changeAppsConfig(conf, "vpn-client", "--srv", keyParsed.Hex())
		}

		if addVPNClientPasscode != "" {
			changeAppsConfig(conf, "vpn-client", "--passcode", addVPNClientPasscode)
		}
		if isResetVPNclient {
			resetAppsConfig(conf, "vpn-client")
		}
		saveConfig(conf)
	},
}

var vpnServerUpdateCmd = &cobra.Command{
	Use:   "vpns",
	Short: "update vpn-server config",
	PreRun: func(_ *cobra.Command, _ []string) {
		setDefaults()
		checkConfig()
	},
	Run: func(_ *cobra.Command, _ []string) {
		conf = initUpdate()
		if addVPNServerPasscode != "" {
			changeAppsConfig(conf, "vpn-server", "--passcode", addVPNServerPasscode)
		}
		if setVPNServerNetIfc != "" {
			changeAppsConfig(conf, "vpn-server", "--netifc", setVPNServerNetIfc)
		}
		switch setVPNServerSecure {
		case "true":
			changeAppsConfig(conf, "vpn-server", "--secure", setVPNServerSecure)
		case "false":
			changeAppsConfig(conf, "vpn-server", "--secure", setVPNServerSecure)
		case "":
			break
		default:
			logger.Fatal("Unrecognized vpn server secure value: ", setVPNServerSecure)
		}
		switch setVPNServerAutostart {
		case "true":
			for i, app := range conf.Launcher.Apps {
				if app.Name == "vpn-server" {
					conf.Launcher.Apps[i].AutoStart = true
				}
			}
		case "false":
			for i, app := range conf.Launcher.Apps {
				if app.Name == "vpn-server" {
					conf.Launcher.Apps[i].AutoStart = false
				}
			}
		case "":
			break
		default:
			logger.Fatal("Unrecognized vpn server autostart value: ", setVPNServerSecure)
		}
		if isResetVPNServer {
			resetAppsConfig(conf, "vpn-server")
		}
		saveConfig(conf)
	},
}
