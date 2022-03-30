package update

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/dmsgc"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var (
	output                 string
	stdout                 bool
	input                  string
	testEnv                bool
	updateEndpoints        bool
	addHypervisorPKs       string
	resetHypervisor        bool
	setVPNClientKillswitch string
	addVPNClientSrv        string
	addVPNClientPasscode   string
	resetVPNclient         bool
	addVPNServerPasscode   string
	setVPNServerSecure     string
	resetVPNServer         bool
	addSkysocksClientSrv   string
	resetSkysocksClient    bool
	skysocksPasscode       string
	resetSkysocks          bool
	setPublicAutoconnect   string
	serviceConfURL         string
	minHops                int
	svcconf                = strings.ReplaceAll(serviceconfaddr, "http://", "") //skyenv.DefaultServiceConfAddr
	testconf               = strings.ReplaceAll(testconfaddr, "http://", "")    //skyenv.DefaultServiceConfAddr
)

const serviceconfaddr = "http://conf.skywire.skycoin.com"
const testconfaddr = "http://conf.skywire.dev"

var logger = logging.MustGetLogger("skywire-cli")

func init() {
	RootCmd.Flags().SortFlags = false
	RootCmd.Flags().BoolVarP(&updateEndpoints, "endpoints", "a", false, "update server endpoints")
	RootCmd.Flags().StringVarP(&serviceConfURL, "url", "b", "", "service config URL: "+svcconf)
	RootCmd.Flags().BoolVarP(&testEnv, "testenv", "t", false, "use test deployment: "+testconf)
	RootCmd.Flags().StringVar(&setPublicAutoconnect, "public-autoconn", "", "change public autoconnect configuration")
	RootCmd.Flags().IntVar(&minHops, "set-minhop", -1, "change min hops value")
	RootCmd.PersistentFlags().StringVarP(&input, "input", "i", "", "path of input config file.")
	RootCmd.PersistentFlags().StringVarP(&output, "output", "o", "", "config file to output")
	RootCmd.PersistentFlags().BoolVarP(&pkg, "pkg", "p", false, "read from /opt/skywire/skywire.json")
}

// RootCmd contains commands that update the config
var RootCmd = &cobra.Command{
	Use:   "update",
	Short: "update a config file",
	PreRun: func(_ *cobra.Command, _ []string) {
		//set default output filename
		if output == "" {
			output = skyenv.ConfigName
			//outunset = true
		}
		var err error
		if output, err = filepath.Abs(output); err != nil {
			logger.WithError(err).Fatal("Invalid config output.")
		}
	},
	Run: func(cmd *cobra.Command, _ []string) {
		mLog := logging.NewMasterLogger()
		mLog.SetLevel(logrus.InfoLevel)
		if cmd.Flags().Changed("serviceConfURL") {
			updateEndpoints = true
		}
		if input == "" {
			input = output
		}
		conf, ok := visorconfig.ReadConfig(input)
		if ok != nil {
			mLog.WithError(ok).Fatal("Failed to parse config.")
		}

		if updateEndpoints {
			if testEnv {
				serviceConfURL = testconf
			}
			services := visorconfig.Fetch(mLog, serviceConfURL, stdout)
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
