// Package cliconfig cmd/skywire-cli/commands/config/gen.go
package cliconfig

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/bitfield/script"
	"github.com/skycoin/dmsg/pkg/disc"
	"github.com/skycoin/dmsg/pkg/dmsgpty"
	coinCipher "github.com/skycoin/skycoin/src/cipher"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/netutil"
	utilenv "github.com/skycoin/skywire-utilities/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/dmsgc"
	"github.com/skycoin/skywire/pkg/restart"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport/network"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// RootCmd contains commands that interact with the config of local skywire-visor
var checkPKCmd = &cobra.Command{
	Use:   "check-pk <public-key>",
	Short: "check a skywire public key",
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			return
		}
		var checkKey cipher.PubKey
		err := checkKey.Set(args[0])
		if err != nil {
			logger.WithError(err).Fatal("invalid public key ") //nolint
		}
		logger.Info("Valid public key: ", checkKey.String())
	},
}

// RootCmd contains commands that interact with the config of local skywire-visor
var genKeysCmd = &cobra.Command{
	Use:   "gen-keys",
	Short: "generate public / secret keypair",
	Run: func(cmd *cobra.Command, args []string) {
		pk, sk := cipher.GenerateKeyPair()
		fmt.Println(pk)
		fmt.Println(sk)
	},
}

func init() {
	//disable sorting, flags appear in the order shown here
	genConfigCmd.Flags().SortFlags = false
	RootCmd.AddCommand(genConfigCmd, genKeysCmd, checkPKCmd)

	genConfigCmd.Flags().StringVarP(&serviceConfURL, "url", "a", utilenv.ServiceConfAddr, "services conf url\n\r")
	gHiddenFlags = append(gHiddenFlags, "url")
	genConfigCmd.Flags().StringVar(&logLevel, "loglvl", "info", "[ debug | warn | error | fatal | panic | trace | info ]\033[0m")
	gHiddenFlags = append(gHiddenFlags, "loglvl")
	genConfigCmd.Flags().BoolVarP(&isBestProtocol, "bestproto", "b", false, "best protocol (dmsg | direct) based on location\033[0m") //this will also disable public autoconnect based on location
	genConfigCmd.Flags().BoolVarP(&isDisableAuth, "noauth", "c", false, "disable authentication for hypervisor UI\033[0m")
	gHiddenFlags = append(gHiddenFlags, "noauth")
	genConfigCmd.Flags().BoolVarP(&isDmsgHTTP, "dmsghttp", "d", false, "use dmsg connection to skywire services\033[0m")
	gHiddenFlags = append(gHiddenFlags, "dmsghttp")
	genConfigCmd.Flags().BoolVarP(&isEnableAuth, "auth", "e", false, "enable auth on hypervisor UI\033[0m")
	gHiddenFlags = append(gHiddenFlags, "auth")
	genConfigCmd.Flags().BoolVarP(&isForce, "force", "f", false, "remove pre-existing config\033[0m")
	gHiddenFlags = append(gHiddenFlags, "force")
	genConfigCmd.Flags().StringVarP(&disableApps, "disableapps", "g", "", "comma separated list of apps to disable\033[0m")
	gHiddenFlags = append(gHiddenFlags, "disableapps")
	genConfigCmd.Flags().BoolVarP(&isHypervisor, "ishv", "i", false, "local hypervisor configuration\033[0m")
	genConfigCmd.Flags().StringVarP(&hypervisorPKs, "hvpks", "j", "", "list of public keys to add as hypervisor\033[0m")
	genConfigCmd.Flags().StringVar(&dmsgptyWlPKs, "dmsgpty", "", "add dmsgpty whitelist PKs")
	genConfigCmd.Flags().StringVar(&surveyWhitelistPKs, "survey", "", "add survey whitelist PKs")
	gHiddenFlags = append(gHiddenFlags, "survey")
	genConfigCmd.Flags().StringVar(&routeSetupNodes, "routesetup", "", "add route setup node PKs")
	gHiddenFlags = append(gHiddenFlags, "routesetup")
	genConfigCmd.Flags().StringVar(&transportSetupPKs, "tpsetup", "", "add transport setup PKs")
	gHiddenFlags = append(gHiddenFlags, "tpsetup")
	genConfigCmd.Flags().StringVarP(&selectedOS, "os", "k", visorconfig.OS, "(linux / mac / win) paths\033[0m")
	gHiddenFlags = append(gHiddenFlags, "os")
	genConfigCmd.Flags().BoolVarP(&isDisplayNodeIP, "publicip", "l", false, "allow display node ip in services\033[0m")
	gHiddenFlags = append(gHiddenFlags, "publicip")
	genConfigCmd.Flags().BoolVarP(&addExampleApps, "example-apps", "m", false, "add example apps to the config\033[0m")
	gHiddenFlags = append(gHiddenFlags, "example-apps")
	genConfigCmd.Flags().BoolVarP(&isStdout, "stdout", "n", false, "write config to stdout\033[0m")
	gHiddenFlags = append(gHiddenFlags, "stdout")
	genConfigCmd.Flags().StringVarP(&output, "out", "o", "", "output config\033[0m")
	if visorconfig.OS == "win" {
		pText = "use .msi installation path: "
	}
	if visorconfig.OS == "linux" {
		pText = "use path for package: "
	}
	if visorconfig.OS == "mac" {
		pText = "use mac installation path: "
	}
	genConfigCmd.Flags().BoolVarP(&isPkgEnv, "pkg", "p", false, pText+visorconfig.SkywirePath+"\033[0m")
	homepath := visorconfig.HomePath()
	if homepath != "" {
		genConfigCmd.Flags().BoolVarP(&isUsrEnv, "user", "u", false, "use paths for user space: "+homepath+"\033[0m")
	}
	genConfigCmd.Flags().BoolVarP(&isRegen, "regen", "r", false, "re-generate existing config & retain keys")
	genConfigCmd.Flags().VarP(&sk, "sk", "s", "a random key is generated if unspecified\n\r")
	gHiddenFlags = append(gHiddenFlags, "sk")
	genConfigCmd.Flags().BoolVarP(&isTestEnv, "testenv", "t", false, "use test deployment "+testConf+"\033[0m")
	gHiddenFlags = append(gHiddenFlags, "testenv")
	genConfigCmd.Flags().BoolVarP(&isVpnServerEnable, "servevpn", "v", false, "enable vpn server\033[0m")
	gHiddenFlags = append(gHiddenFlags, "servevpn")
	genConfigCmd.Flags().BoolVarP(&isHide, "hide", "w", false, "dont print the config to the terminal :: show errors with -n flag\033[0m")
	gHiddenFlags = append(gHiddenFlags, "hide")
	genConfigCmd.Flags().BoolVarP(&isRetainHypervisors, "retainhv", "x", false, "retain existing hypervisors with regen\033[0m")
	gHiddenFlags = append(gHiddenFlags, "retainhv")
	genConfigCmd.Flags().BoolVarP(&disablePublicAutoConn, "autoconn", "y", false, "disable autoconnect to public visors\033[0m")
	gHiddenFlags = append(gHiddenFlags, "hide")
	genConfigCmd.Flags().BoolVarP(&isPublic, "public", "z", false, "publicize visor in service discovery\033[0m")
	gHiddenFlags = append(gHiddenFlags, "public")
	genConfigCmd.Flags().IntVar(&stcprPort, "stcpr", 0, "set tcp transport listening port - 0 for random\033[0m")
	gHiddenFlags = append(gHiddenFlags, "stcpr")
	genConfigCmd.Flags().IntVar(&sudphPort, "sudph", 0, "set udp transport listening port - 0 for random\033[0m")
	gHiddenFlags = append(gHiddenFlags, "sudph")
	genConfigCmd.Flags().BoolVar(&isAll, "all", false, "show all flags")
	genConfigCmd.Flags().StringVar(&binPath, "binpath", "", "set bin_path\033[0m")
	gHiddenFlags = append(gHiddenFlags, "binpath")
	genConfigCmd.Flags().BoolVar(&noFetch, "nofetch", false, "do not fetch the services from the service conf url")
	gHiddenFlags = append(gHiddenFlags, "nofetch")
	genConfigCmd.Flags().BoolVar(&noDefaults, "nodefaults", false, "do not use hardcoded defaults for production / test services")
	gHiddenFlags = append(gHiddenFlags, "nodefaults")
	genConfigCmd.Flags().StringVar(&ver, "version", "", "custom version testing override\033[0m")
	gHiddenFlags = append(gHiddenFlags, "version")

	//show all flags on help
	if os.Getenv("UNHIDEFLAGS") != "1" {
		for _, j := range gHiddenFlags {
			genConfigCmd.Flags().MarkHidden(j) //nolint
		}
	}
}

var genConfigCmd = &cobra.Command{
	Use:   "gen",
	Short: "Generate a config file",
	PreRun: func(cmd *cobra.Command, _ []string) {
		log := logger
		//--all unhides flags, prints help menu, and exits
		if isAll {
			for _, j := range gHiddenFlags {
				f := cmd.Flags().Lookup(j) //nolint
				f.Hidden = false
			}
			cmd.Flags().MarkHidden("all") //nolint
			cmd.Help()                    //nolint
			os.Exit(0)
		}
		//set default output filename
		if output == "" {
			isOutUnset = true
			confPath = visorconfig.ConfigName
			output = confPath
		} else {
			confPath = output
		}

		if output == visorconfig.Stdout {
			isStdout = true
			isForce = false
		}
		if isStdout {
			isRegen = false
		}
		//--force will delete a config, which excludes --regen
		if (isForce) && (isRegen) {
			log.Fatal("Use of mutually exclusive flags: -f --force cannot override -r --regen")
		}
		// these flags overwrite each other
		if (isUsrEnv) && (isPkgEnv) {
			log.Fatal("Use of mutually exclusive flags: -u --user and -p --pkg")
		}
		//enable local hypervisor by default for user
		if isUsrEnv {
			isHypervisor = true
		}
		var err error
		if isDmsgHTTP {
			dmsgHTTPPath := visorconfig.DMSGHTTPName
			if isPkgEnv {
				dmsgHTTPPath = visorconfig.SkywirePath + "/" + visorconfig.DMSGHTTPName
			}
			if _, err := os.Stat(dmsgHTTPPath); err == nil {
				if !isStdout {
					log.Info("Found Dmsghttp config: ", dmsgHTTPPath)
				}
			} else {
				log.Fatal("Dmsghttp config not found at: ", dmsgHTTPPath)
			}
		}
		if !isStdout {
			if confPath, err = filepath.Abs(confPath); err != nil {
				log.WithError(err).Fatal("Invalid output provided.")
			}
			if isForce {
				if _, err := os.Stat(confPath); err == nil {
					err := os.Remove(confPath)
					if err != nil {
						log.WithError(err).Warn("Could not remove file")
					}
				} else {
					log.Info("Ignoring -f --force flag, config not found.")
				}
			}
		}
		// skywire-cli config gen -p
		if !isStdout && isOutUnset {
			if isPkgEnv {
				configName = visorconfig.ConfigJSON
				confPath = visorconfig.SkywireConfig()
				output = confPath
			}
			if isUsrEnv {
				confPath = visorconfig.HomePath() + "/" + visorconfig.ConfigName
				output = confPath
			}
		}
		if !isRegen && !isStdout {
			//check if the config exists
			if _, err := os.Stat(confPath); err == nil {
				//error config exists !regen
				log.Fatal("Config file already exists. Specify the '-r --regen' flag to regenerate.")
			}
		}
		//don't write file with stdout
		if !isStdout {
			if visorconfig.OS == "linux" {
				//warn when writing config as root to non root owned dir & fail on the reverse instance
				if _, err = exec.LookPath("stat"); err == nil {
					confPath1, _ := filepath.Split(confPath)
					if confPath1 == "" {
						confPath1 = "./"
					}
					owner, err := script.Exec(`stat -c '%U' ` + confPath1).String()
					if err != nil {
						log.Error("cannot stat: " + confPath1)
					}
					rootOwner, err := script.Exec(`stat -c '%U' /root`).String()
					if err != nil {
						log.Error("cannot stat: /root")
					}
					if (owner != rootOwner) && isRoot {
						log.Warn("writing config as root to directory not owned by root")
					}
					if !isRoot && (owner == rootOwner) {
						log.Fatal("Insufficient permissions to write to the specified path")
					}
				}
			}
		}
	},
	Run: func(cmd *cobra.Command, args []string) {

		log := logger

		if !noFetch {
			// set default service conf url if none is specified
			if serviceConfURL == "" {
				serviceConfURL = utilenv.ServiceConfAddr
			}
			//use test deployment
			if serviceConfURL == "" && isTestEnv {
				serviceConfURL = utilenv.TestServiceConfAddr
			}
			// enable errors from service conf fetch from the combination of these flags
			wasStdout := isStdout
			if isStdout && isHide {
				isStdout = false
			}
			// create an http client to fetch the services
			client := http.Client{
				Timeout: time.Second * 15, // Timeout after 30 seconds
			}
			//create the http request
			req, err := http.NewRequest(http.MethodGet, fmt.Sprint(serviceConfURL), nil)
			if err != nil {
				log.WithError(err).Fatal("Failed to create http request\n")
			}
			req.Header.Add("Cache-Control", "no-cache")
			//check for errors in the response
			res, err := client.Do(req)
			if err != nil {
				//silence errors for stdout
				if !isStdout {
					log.WithError(err).Error("Failed to fetch servers\n")
					log.Warn("Falling back on hardcoded servers")
				}
			} else {
				// nil error from client.Do(req)
				if res.Body != nil {
					defer res.Body.Close() //nolint
				}
				body, err := io.ReadAll(res.Body)
				if err != nil {
					log.WithError(err).Fatal("Failed to read response\n")
				}
				//fill in services struct with the response
				err = json.Unmarshal(body, &services)
				if err != nil {
					log.WithError(err).Fatal("Failed to unmarshal json response\n")
				}
				if !isStdout {
					log.Infof("Fetched service endpoints from '%s'", serviceConfURL)
				}
			}
			// reset the state of isStdout
			isStdout = wasStdout
		}

		// Read in old config and obtain old secret key or generate a new random secret key
		// and obtain old hypervisors (if any)
		var oldConf visorconfig.V1
		if isRegen {
			// Read the JSON configuration file
			oldConfJSON, err := os.ReadFile(confPath)
			if err != nil {
				if !isStdout || isStdout && isHide {
					log.Fatalf("Failed to read config file: %v", err)
				}
			}
			// Decode JSON data
			err = json.Unmarshal(oldConfJSON, &oldConf)
			if err != nil {
				if !isStdout || isStdout && isHide {
					log.WithError(err).Fatal("Failed to unmarshal old config json")
				}
			}
			if err != nil {
				_, sk = cipher.GenerateKeyPair()
			} else {
				sk = oldConf.SK
				if isRetainHypervisors {
					for _, j := range oldConf.Hypervisors {
						hypervisorPKs = hypervisorPKs + "," + fmt.Sprintf("\t%s\n", j)
					}
					for _, j := range oldConf.Dmsgpty.Whitelist {
						dmsgptyWlPKs = dmsgptyWlPKs + "," + fmt.Sprintf("\t%s\n", j)
					}
				}
			}
		}

		//determine best protocol
		if isBestProtocol && netutil.LocalProtocol() {
			disablePublicAutoConn = true
			isDmsgHTTP = true
		}
		//generate the common config containing public & secret keys
		u := buildinfo.Version()
		x := u
		if u == "unknown" {
			//check for .git folder for versioning
			if _, err := os.Stat(".git"); err == nil {
				//attempt to version from git sources
				if _, err = exec.LookPath("git"); err == nil {
					if x, err = script.Exec(`git describe`).String(); err == nil {
						x = strings.ReplaceAll(x, "\n", "")
						x = strings.Split(x, "-")[0]
					}
				}
			}
		}
		pk, err := sk.PubKey()
		if err != nil {
			pk, sk = cipher.GenerateKeyPair()
		}

		conf.Common = new(visorconfig.Common)
		conf.Common.Version = x
		conf.Common.SK = sk
		conf.Common.PK = pk

		dnsServer := utilenv.DNSServer
		if services != nil {
			if services.DNSServer != "" {
				dnsServer = services.DNSServer
			}
		}
		if isDmsgHTTP {
			dmsghttpConfig := visorconfig.DMSGHTTPName
			// TODO
			//if isUsrEnv {
			//	dmsghttpConfig = homepath + "/" + visorconfig.DMSGHTTPName
			//}
			if isPkgEnv {
				dmsghttpConfig = visorconfig.SkywirePath + "/" + visorconfig.DMSGHTTPName
			}

			// Read the JSON configuration file
			dmsghttpConfigData, err := os.ReadFile(dmsghttpConfig) //nolint
			if err != nil {
				log.Fatalf("Failed to read config file: %v", err)
			}

			// Decode JSON data
			err = json.Unmarshal(dmsghttpConfigData, &dmsgHTTPServersList)
			if err != nil {
				log.WithError(err).Fatal("Failed to unmarshal " + visorconfig.DMSGHTTPName)
			}
		}

		//fall back on  defaults
		var routeSetupPKs cipher.PubKeys
		var tpSetupPKs cipher.PubKeys
		var surveyWlPKs cipher.PubKeys
		// If nothing was fetched
		if services.SurveyWhitelist == nil {
			// By default
			if !noDefaults {
				// set the hardcoded defaults from skywire-utilities for the service public keys
				if err := surveyWlPKs.Set(utilenv.SurveyWhitelistPKs); err != nil {
					log.Fatalf("Failed to unmarshal survey whitelist public keys: %v", err)
				}
			}
		}
		//if the flag is not empty
		if surveyWhitelistPKs != "" {
			// validate public keys set via flag / fail explicitly on errors
			if err := surveyWlPKs.Set(surveyWhitelistPKs); err != nil {
				log.Fatalf("bad key set for survey whitelist flag: %v", err)
			}
		}
		services.SurveyWhitelist = append(services.SurveyWhitelist, surveyWlPKs...)

		if !isTestEnv {
			if services.DmsgDiscovery == "" {
				services.DmsgDiscovery = utilenv.DmsgDiscAddr
			}
			if services.DmsgDiscovery == "" {
				services.DmsgDiscovery = utilenv.DmsgDiscAddr
			}
			if services.TransportDiscovery == "" {
				services.TransportDiscovery = utilenv.TpDiscAddr
			}
			if services.AddressResolver == "" {
				services.AddressResolver = utilenv.AddressResolverAddr
			}
			if services.RouteFinder == "" {
				services.RouteFinder = utilenv.RouteFinderAddr
			}
			if services.UptimeTracker == "" {
				services.UptimeTracker = utilenv.UptimeTrackerAddr
			}
			if services.ServiceDiscovery == "" {
				services.ServiceDiscovery = utilenv.ServiceDiscAddr
			}
			if services.StunServers == nil {
				services.StunServers = utilenv.GetStunServers()
			}
			if services.DNSServer == "" {
				services.DNSServer = utilenv.DNSServer
			}
			if routeSetupNodes != "" {
				if err := routeSetupPKs.Set(routeSetupNodes); err != nil {
					log.Fatalf("bad key set for route setup node flag: %v", err)
				}
			}
			if services.RouteSetupNodes == nil {
				if !noDefaults {
					if err := routeSetupPKs.Set(utilenv.RouteSetupPKs); err != nil {
						log.Fatalf("Failed to unmarshal route setup-node public keys: %v", err)
					}
				}
			}
			services.RouteSetupNodes = append(services.RouteSetupNodes, routeSetupPKs...)
			if transportSetupPKs != "" {
				if err := tpSetupPKs.Set(transportSetupPKs); err != nil {
					log.Fatalf("bad key set for transport setup node flag: %v", err)
				}
			}
			if services.TransportSetupPKs == nil {
				if !noDefaults {
					if err := tpSetupPKs.Set(utilenv.TPSetupPKs); err != nil {
						log.Fatalf("Failed to unmarshal transport setup-node public keys: %v", err)
					}
				}
			}
			services.TransportSetupPKs = append(services.TransportSetupPKs, tpSetupPKs...)
		} else {
			if services.DmsgDiscovery == "" {
				services.DmsgDiscovery = utilenv.TestDmsgDiscAddr
			}
			if services.TransportDiscovery == "" {
				services.TransportDiscovery = utilenv.TestTpDiscAddr
			}
			if services.AddressResolver == "" {
				services.AddressResolver = utilenv.TestAddressResolverAddr
			}
			if services.RouteFinder == "" {
				services.RouteFinder = utilenv.TestRouteFinderAddr
			}
			if services.UptimeTracker == "" {
				services.UptimeTracker = utilenv.TestUptimeTrackerAddr
			}
			if services.ServiceDiscovery == "" {
				services.ServiceDiscovery = utilenv.TestServiceDiscAddr
			}
			if services.StunServers == nil {
				services.StunServers = utilenv.GetStunServers()
			}
			if services.DNSServer == "" {
				services.DNSServer = utilenv.DNSServer
			}
			if routeSetupNodes != "" {
				if err := routeSetupPKs.Set(routeSetupNodes); err != nil {
					log.Fatalf("bad key set for route setup node flag: %v", err)
				}
			}
			if services.RouteSetupNodes == nil {
				if err := routeSetupPKs.Set(utilenv.TestRouteSetupPKs); err != nil {
					log.Fatalf("Failed to unmarshal route setup-node public keys: %v", err)
				}
			}
			services.RouteSetupNodes = append(services.RouteSetupNodes, routeSetupPKs...)
			if transportSetupPKs != "" {
				if err := tpSetupPKs.Set(transportSetupPKs); err != nil {
					log.Fatalf("bad key set for transport setup node flag: %v", err)
				}
			}
			if services.TransportSetupPKs == nil {
				if !noDefaults {
					if err := tpSetupPKs.Set(utilenv.TestTPSetupPKs); err != nil {
						log.Fatalf("Failed to unmarshal transport setup-node public keys: %v", err)
					}
				}
			}
			services.TransportSetupPKs = append(services.TransportSetupPKs, tpSetupPKs...)
		}

		conf.Dmsg = &dmsgc.DmsgConfig{
			Discovery:     services.DmsgDiscovery,
			SessionsCount: 1,
			Servers:       []*disc.Entry{},
		}
		conf.Transport = &visorconfig.Transport{
			Discovery:         services.TransportDiscovery, //utilenv.TpDiscAddr,
			AddressResolver:   services.AddressResolver,    //utilenv.AddressResolverAddr,
			PublicAutoconnect: visorconfig.PublicAutoconnect,
			TransportSetupPKs: services.TransportSetupPKs,
			LogStore: &visorconfig.LogStore{
				Type:             visorconfig.FileLogStore,
				Location:         visorconfig.LocalPath + "/" + visorconfig.TpLogStore,
				RotationInterval: visorconfig.DefaultLogRotationInterval,
			},
			SudphPort: sudphPort,
			StcprPort: sudphPort,
		}
		conf.Routing = &visorconfig.Routing{
			RouteFinder:        services.RouteFinder,     //utilenv.RouteFinderAddr,
			RouteSetupNodes:    services.RouteSetupNodes, //[]cipher.PubKey{utilenv.MustPK(utilenv.SetupPK)},
			RouteFinderTimeout: visorconfig.DefaultTimeout,
		}
		conf.Launcher = &visorconfig.Launcher{
			ServiceDisc:   services.ServiceDiscovery, //utilenv.ServiceDiscAddr,
			Apps:          nil,
			ServerAddr:    visorconfig.AppSrvAddr,
			BinPath:       visorconfig.AppBinPath,
			DisplayNodeIP: isDisplayNodeIP,
		}
		conf.UptimeTracker = &visorconfig.UptimeTracker{
			Addr: services.UptimeTracker, //utilenv.UptimeTrackerAddr,
		}
		conf.CLIAddr = visorconfig.RPCAddr
		conf.LogLevel = logLevel
		conf.LocalPath = visorconfig.LocalPath
		conf.DmsgHTTPServerPath = visorconfig.LocalPath + "/" + visorconfig.Custom
		conf.StunServers = services.StunServers //utilenv.GetStunServers()
		conf.ShutdownTimeout = visorconfig.DefaultTimeout
		conf.RestartCheckDelay = visorconfig.Duration(restart.DefaultCheckDelay)

		conf.Dmsgpty = &visorconfig.Dmsgpty{
			DmsgPort: visorconfig.DmsgPtyPort,
			CLINet:   visorconfig.DmsgPtyCLINet,
			CLIAddr:  dmsgpty.DefaultCLIAddr(),
		}

		conf.STCP = &network.STCPConfig{
			ListeningAddress: visorconfig.STCPAddr,
			PKTable:          nil,
		}

		// Use dmsg urls for services and add dmsg-servers
		if isDmsgHTTP {
			if dmsgHTTPServersList != nil {
				if isTestEnv {
					conf.Dmsg.Servers = dmsgHTTPServersList.Test.DMSGServers
					conf.Dmsg.Discovery = dmsgHTTPServersList.Test.DMSGDiscovery
					conf.Transport.AddressResolver = dmsgHTTPServersList.Test.AddressResolver
					conf.Transport.Discovery = dmsgHTTPServersList.Test.TransportDiscovery
					conf.UptimeTracker.Addr = dmsgHTTPServersList.Test.UptimeTracker
					conf.Routing.RouteFinder = dmsgHTTPServersList.Test.RouteFinder
					conf.Launcher.ServiceDisc = dmsgHTTPServersList.Test.ServiceDiscovery
				} else {
					conf.Dmsg.Servers = dmsgHTTPServersList.Prod.DMSGServers
					conf.Dmsg.Discovery = dmsgHTTPServersList.Prod.DMSGDiscovery
					conf.Transport.AddressResolver = dmsgHTTPServersList.Prod.AddressResolver
					conf.Transport.Discovery = dmsgHTTPServersList.Prod.TransportDiscovery
					conf.UptimeTracker.Addr = dmsgHTTPServersList.Prod.UptimeTracker
					conf.Routing.RouteFinder = dmsgHTTPServersList.Prod.RouteFinder
					conf.Launcher.ServiceDisc = dmsgHTTPServersList.Prod.ServiceDiscovery
				}
			}
		}

		// Configure public visor
		conf.IsPublic = isPublic

		// Manipulate Hypervisor PKs
		conf.Hypervisors = make([]cipher.PubKey, 0)
		if hypervisorPKs != "" {
			keys := strings.Split(hypervisorPKs, ",")
			for _, key := range keys {
				if key != "" {
					keyParsed, err := coinCipher.PubKeyFromHex(strings.TrimSpace(key))
					if err != nil {
						log.WithError(err).Fatalf("Failed to parse hypervisor public key: %s.", key)
					}
					if key != conf.PK.Hex() {
						conf.Hypervisors = append(conf.Hypervisors, cipher.PubKey(keyParsed))
					} else {
						// setting the same public key as the current visor for a remote hypervisor is a weird misconfiguration
						// the intention was likely to configure this visor as the hypervisor
						isHypervisor = true
					}
				}
			}
		}
		// Local hypervisor setting
		if isHypervisor {
			config := visorconfig.GenerateWorkDirConfig(false)
			conf.Hypervisor = &config
		}

		// Manipulate dmsgpty whitelist PKs
		conf.Dmsgpty.Whitelist = make([]cipher.PubKey, 0)
		if dmsgptyWlPKs != "" {
			keys := strings.Split(dmsgptyWlPKs, ",")
			for _, key := range keys {
				if key != "" {
					keyParsed, err := coinCipher.PubKeyFromHex(strings.TrimSpace(key))
					if err != nil {
						log.WithError(err).Fatalf("Failed to parse Dmsgpty Whitelist public key: %s.", key)
					}
					conf.Dmsgpty.Whitelist = append(conf.Dmsgpty.Whitelist, cipher.PubKey(keyParsed))
				}
			}
		}
		// set survey collection whitelist - will include by default hypervisors & dmsgpty whitelisted keys
		conf.SurveyWhitelist = services.SurveyWhitelist
		// set package-specific config paths
		if isPkgEnv {
			pkgConfig := visorconfig.PackageConfig()
			conf.LocalPath = pkgConfig.LocalPath
			conf.DmsgHTTPServerPath = pkgConfig.LocalPath + "/" + visorconfig.Custom
			conf.Launcher.BinPath = pkgConfig.LauncherBinPath
			conf.Transport.LogStore.Location = pkgConfig.LocalPath + "/" + visorconfig.TpLogStore
			if conf.Hypervisor != nil {
				conf.Hypervisor.EnableAuth = pkgConfig.Hypervisor.EnableAuth
				conf.Hypervisor.DBPath = pkgConfig.Hypervisor.DbPath
			}
		}
		// set config paths for the user space
		if isUsr {
			usrConfig := visorconfig.UserConfig()
			conf.LocalPath = usrConfig.LocalPath
			conf.DmsgHTTPServerPath = usrConfig.LocalPath + "/" + visorconfig.Custom
			conf.Launcher.BinPath = usrConfig.LauncherBinPath
			conf.Transport.LogStore.Location = usrConfig.LocalPath + "/" + visorconfig.TpLogStore
			if conf.Hypervisor != nil {
				conf.Hypervisor.EnableAuth = usrConfig.Hypervisor.EnableAuth
				conf.Hypervisor.DBPath = usrConfig.Hypervisor.DbPath
			}
		}
		// App config settings
		conf.Launcher.Apps = []appserver.AppConfig{
			{
				Name:      visorconfig.VPNClientName,
				Binary:    visorconfig.VPNClientName,
				AutoStart: false,
				Port:      routing.Port(skyenv.VPNClientPort),
				Args:      []string{"-dns", dnsServer},
			},
			{
				Name:      visorconfig.SkychatName,
				Binary:    visorconfig.SkychatName,
				AutoStart: true,
				Port:      routing.Port(skyenv.SkychatPort),
				Args:      []string{"-addr", visorconfig.SkychatAddr},
			},
			{
				Name:      visorconfig.SkysocksName,
				Binary:    visorconfig.SkysocksName,
				AutoStart: true,
				Port:      routing.Port(visorconfig.SkysocksPort),
			},
			{
				Name:      visorconfig.SkysocksClientName,
				Binary:    visorconfig.SkysocksClientName,
				AutoStart: false,
				Port:      routing.Port(visorconfig.SkysocksClientPort),
			},
			{
				Name:      visorconfig.VPNServerName,
				Binary:    visorconfig.VPNServerName,
				AutoStart: isVpnServerEnable,
				Port:      routing.Port(visorconfig.VPNServerPort),
			},
		}

		skywire := os.Args[0]
		isMatch := strings.Contains("/tmp/", skywire)
		if (!isStdout) || (!isMatch) {
			//binaries have .exe extension on windows
			var exe string
			if visorconfig.OS == "win" {
				exe = ".exe"
			}
			// Disable apps not found at bin_path with above exceptions for go run and stdout
			if _, err := os.Stat(conf.Launcher.BinPath + "/" + "skychat" + exe); err != nil {
				if disableApps == "" {
					disableApps = "skychat"
				} else {
					disableApps = disableApps + ",skychat"
				}
			}
			if _, err := os.Stat(conf.Launcher.BinPath + "/" + "skysocks" + exe); err != nil {
				if disableApps == "" {
					disableApps = "skysocks"
				} else {
					disableApps = disableApps + ",skysocks"
				}
			}
			if _, err := os.Stat(conf.Launcher.BinPath + "/" + "skysocks-client" + exe); err != nil {
				if disableApps == "" {
					disableApps = "skysocks-client"
				} else {
					disableApps = disableApps + ",skysocks-client"
				}
			}
			if _, err := os.Stat(conf.Launcher.BinPath + "/" + "vpn-client" + exe); err != nil {
				if disableApps == "" {
					disableApps = "vpn-client"
				} else {
					disableApps = disableApps + ",vpn-client"
				}
			}
			if _, err := os.Stat(conf.Launcher.BinPath + "/" + "vpn-server" + exe); err != nil {
				if disableApps == "" {
					disableApps = "vpn-server"
				} else {
					disableApps = disableApps + ",vpn-server"
				}
			}
		}
		// Disable apps --disable-apps flag
		if disableApps != "" {
			apps := strings.Split(disableApps, ",")
			appsSlice := make(map[string]bool)
			for _, app := range apps {
				appsSlice[app] = true
			}
			var newConfLauncherApps []appserver.AppConfig
			for _, app := range conf.Launcher.Apps {
				if _, ok := appsSlice[app.Name]; !ok {
					newConfLauncherApps = append(newConfLauncherApps, app)
				}
			}
			conf.Launcher.Apps = newConfLauncherApps
		}
		// add example applications to the config
		if addExampleApps {
			exampleApps := []appserver.AppConfig{
				{
					Name:      skyenv.ExampleServerName,
					AutoStart: false,
					Port:      routing.Port(skyenv.ExampleServerPort),
				},
			}
			newConfLauncherApps := append(conf.Launcher.Apps, exampleApps...)
			conf.Launcher.Apps = newConfLauncherApps
		}
		if isHypervisor {
			// Disable hypervisor UI authentication --disable-auth flag
			if isDisableAuth {
				conf.Hypervisor.EnableAuth = false
			}
			// Enable hypervisor UI authentication --enable-auth flag
			if isEnableAuth {
				conf.Hypervisor.EnableAuth = true
			}
		}
		// Enable hypervisor UI authentication on windows & macos
		if (selectedOS == "win") || (selectedOS == "mac") {
			if isHypervisor {
				conf.Hypervisor.EnableAuth = true
			}
		}
		// set bin_path for apps from flag
		if binPath != "" {
			conf.Launcher.BinPath = binPath
		}
		// set version of the config file from flag - testing override
		if ver != "" {
			conf.Common.Version = ver
		}
		// Disable autoconnect to public visors
		if disablePublicAutoConn {
			conf.Transport.PublicAutoconnect = false
		}
		// Enable the display of the visor's ip address in service discovery services
		if isDisplayNodeIP {
			conf.Launcher.DisplayNodeIP = true
		}

		//don't write file with stdout
		if !isStdout {
			// Marshal the modified config to JSON with indentation
			jsonData, err := json.MarshalIndent(conf, "", "  ")
			if err != nil {
				log.Fatalf("Failed to marshal config to JSON: %v", err)
			}
			// Write the JSON data back to the file
			err = os.WriteFile(confPath, jsonData, 0644) //nolint
			if err != nil {
				log.Fatalf("Failed to write config file: %v", err)
			}
		}
		// Print results.
		j, err := json.MarshalIndent(conf, "", "\t")
		if err != nil {
			log.WithError(err).Fatal("Could not unmarshal json.")
		}
		//print config to stdout, omit logging messages, exit
		if isStdout {
			fmt.Printf("%s", j)
			os.Exit(0)
		}
		//hide the printing of the config to the terminal
		if isHide {
			log.Infof("Updated file '%s'\n", output)
			os.Exit(0)
		}
		//default behavior
		log.Infof("Updated file '%s' to:\n%s\n", output, j)
	},
}
