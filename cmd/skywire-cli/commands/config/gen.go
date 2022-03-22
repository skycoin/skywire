package config

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	coinCipher "github.com/skycoin/skycoin/src/cipher"
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/netutil"
	"github.com/skycoin/skywire-utilities/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func init() {
	genConfigCmd.Flags().SortFlags = false
	RootCmd.AddCommand(genConfigCmd)
}

var (
	sk                 cipher.SecKey
	output             string
	stdout             bool
	replace            bool
	replaceHypervisors bool
	testEnv            bool
	packageConfig      bool
	hypervisor         bool
	hypervisorPKs      string
	dmsgHTTP           bool
	publicRPC          bool
	vpnServerEnable    bool
	disableAUTH        bool
	enableAUTH         bool
	selectedOS         string
	disableApps        string
	bestProtocol       bool
	serviceConfURL     string
)

func init() {
	genConfigCmd.Flags().StringVarP(&serviceConfURL, "url", "a", "skywire.skycoin.com", "service configuration URL")
	genConfigCmd.Flags().BoolVarP(&bestProtocol, "best-proto", "b", false, "determine best protocol (dmsg / direct) based on location")
	genConfigCmd.Flags().BoolVarP(&disableAUTH, "disable-auth", "c", false, "disable authentication for hypervisor UI.")
	genConfigCmd.Flags().BoolVarP(&dmsgHTTP, "dmsghttp", "d", false, "use dmsg connection to skywire services")
	genConfigCmd.Flags().BoolVarP(&enableAUTH, "enable-auth", "e", false, "enable auth on hypervisor UI.")
	genConfigCmd.Flags().StringVarP(&disableApps, "disable-apps", "f", "", "comma separated list of apps to disable")
	genConfigCmd.Flags().BoolVarP(&hypervisor, "is-hv", "i", false, "hypervisor configuration.")
	genConfigCmd.Flags().StringVarP(&hypervisorPKs, "hvpks", "j", "", "comma separated list of public keys to use as hypervisor")
	genConfigCmd.Flags().StringVarP(&selectedOS, "os", "k", "linux", "use os-specific paths (linux / macos / windows)")
	genConfigCmd.Flags().BoolVarP(&stdout, "stdout", "n", false, "write config to stdout")
	genConfigCmd.Flags().StringVarP(&output, "output", "o", "skywire-config.json", "path of output config file.")
	genConfigCmd.Flags().BoolVarP(&packageConfig, "package", "p", false, "use paths for package (/opt/skywire)")
	genConfigCmd.Flags().BoolVarP(&publicRPC, "public-rpc", "q", false, "allow rpc requests from LAN.")
	genConfigCmd.Flags().BoolVarP(&replace, "replace", "r", false, "rewrite existing config & retain keys.")
	genConfigCmd.Flags().VarP(&sk, "sk", "s", "if unspecified, a random key pair will be generated.\n")
	genConfigCmd.Flags().BoolVarP(&testEnv, "testenv", "t", false, "use test deployment service.")
	genConfigCmd.Flags().BoolVarP(&vpnServerEnable, "serve-vpn", "v", false, "enable vpn server.")
	genConfigCmd.Flags().BoolVarP(&replaceHypervisors, "retain-hv", "x", false, "retain existing hypervisors with replace")
}

var genConfigCmd = &cobra.Command{
	Use:   "gen",
	Short: "generate a config file",
	PreRun: func(_ *cobra.Command, _ []string) {
		var err error
		if output == visorconfig.StdoutName {
			stdout = true
		}
		if !stdout {
			if output, err = filepath.Abs(output); err != nil {
				logger.WithError(err).Fatal("Invalid output provided.")
			}
		}
	},
	Run: func(cmd *cobra.Command, _ []string) {
		mLog := logging.NewMasterLogger()
		mLog.SetLevel(logrus.InfoLevel)

		urlstr := []string{"http://", serviceConfURL, "/config"}
		serviceConfURL = strings.Join(urlstr, "")
		client := http.Client{
			Timeout: time.Second * 2, // Timeout after 2 seconds
		}
		req, err := http.NewRequest(http.MethodGet, serviceConfURL, nil)
		if err != nil {
			mLog.Fatal(err)
		}

		res, err := client.Do(req)
		if err != nil {
			mLog.Fatal(err)
		}

		if res.Body != nil {
			defer res.Body.Close()
		}

		body, err := ioutil.ReadAll(res.Body)
		if err != nil {
			mLog.Fatal(err)
		}
		var services visorconfig.Services
		err = json.Unmarshal(body, &services)
		if err != nil {
			mLog.Fatal(err)
		}

		//fmt.Println(services)
		if !stdout {
			//set output for package and skybian configs
			if packageConfig {
				configName := "skywire-visor.json"
				if hypervisor {
					configName = "skywire.json"
				}
				if !cmd.Flags().Changed("output") {
					output = filepath.Join(skyenv.PackageSkywirePath(), configName)
				}
			}
		}

		// Read in old config (if any) and obtain old secret key.
		// Otherwise, we generate a new random secret key.
		var sk cipher.SecKey
		if !stdout {
			if oldConf, ok := readOldConfig(mLog, output, replace, hypervisor, services); !ok {
				_, sk = cipher.GenerateKeyPair()
			} else {
				sk = oldConf.SK
			}
			//			if output == visorconfig.StdoutName {
			//			_, sk = cipher.GenerateKeyPair()
		}
		// Determine config type to generate.
		var genConf func(log *logging.MasterLogger, confPath string, sk *cipher.SecKey, hypervisor bool, services visorconfig.Services) (*visorconfig.V1, error)

		//  default paths for different installations
		if packageConfig {
			genConf = visorconfig.MakePackageConfig
		} else if testEnv {
			genConf = visorconfig.MakeTestConfig
		} else {
			genConf = visorconfig.MakeDefaultConfig
		}

		// Generate config.
		conf, err := genConf(mLog, output, &sk, hypervisor, services)
		if err != nil {
			logger.WithError(err).Fatal("Failed to create config.")
		}

		// Manipulate Hypervisor PKs
		if hypervisorPKs != "" {
			keys := strings.Split(hypervisorPKs, ",")
			for _, key := range keys {
				keyParsed, err := coinCipher.PubKeyFromHex(strings.TrimSpace(key))
				if err != nil {
					logger.WithError(err).Fatalf("Failed to parse hypervisor private key: %s.", key)
				}
				conf.Hypervisors = append(conf.Hypervisors, cipher.PubKey(keyParsed))

				// Compare key value and visor PK, if same, then this visor should be hypervisor
				if key == conf.PK.Hex() {
					hypervisor = true
					conf, err = genConf(mLog, output, &sk, hypervisor, services)
					if err != nil {
						logger.WithError(err).Fatal("Failed to create config.")
					}
					conf.Hypervisors = []cipher.PubKey{}
					break
				}
			}
		}

		if bestProtocol {
			if netutil.LocalProtocol() {
				dmsgHTTP = true
			}
		}

		// Use dmsg urls for services and add dmsg-servers
		if dmsgHTTP {
			var dmsgHTTPServersList visorconfig.DmsgHTTPServers
			serversListJSON, err := ioutil.ReadFile(conf.DMSGHTTPPath)
			if err != nil {
				logger.WithError(err).Fatal("Failed to read servers.json file.")
			}
			err = json.Unmarshal(serversListJSON, &dmsgHTTPServersList)
			if err != nil {
				logger.WithError(err).Fatal("Error during parsing servers list")
			}
			if testEnv {
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
				/*
					conf.Dmsg.Servers = dmsgHTTPServersList.Prod.DMSGServers
					conf.Dmsg.Discovery = services.DmsgDiscovery
					conf.Transport.AddressResolver = services.AddressResolver
					conf.Transport.Discovery = services.TransportDiscovery
					conf.UptimeTracker.Addr = services.UptimeTracker
					conf.Routing.RouteFinder = services.RouteFinder
					conf.Routing.SetupNodes = services.SetupNodes
					conf.Launcher.ServiceDisc = services.ServiceDiscovery
					conf.StunServers = services.StunServers
				*/
			}
		}

		// Read in old config (if any) and obtain old hypervisors.
		if replaceHypervisors {
			if oldConf, ok := readOldConfig(mLog, output, true, hypervisor, services); ok {
				conf.Hypervisors = oldConf.Hypervisors
			}
		}

		// Change rpc address from local to public
		if publicRPC {
			conf.CLIAddr = ":3435"
		}

		// Set autostart enable for vpn-server
		if vpnServerEnable {
			for i, app := range conf.Launcher.Apps {
				if app.Name == "vpn-server" {
					conf.Launcher.Apps[i].AutoStart = true
				}
			}
		}

		// Disable apps that listed on --disable-apps flag
		if disableApps != "" {
			apps := strings.Split(disableApps, ",")
			appsSlice := make(map[string]bool)
			for _, app := range apps {
				appsSlice[app] = true
			}
			var newConfLauncherApps []launcher.AppConfig
			for _, app := range conf.Launcher.Apps {
				if _, ok := appsSlice[app.Name]; !ok {
					newConfLauncherApps = append(newConfLauncherApps, app)
				}
			}
			conf.Launcher.Apps = newConfLauncherApps
		}

		// Make false EnableAuth for hypervisor UI by --disable-auth flag
		if disableAUTH {
			if hypervisor {
				conf.Hypervisor.EnableAuth = false
			}
		}

		// Set EnableAuth true  for hypervisor UI by --enable-auth flag
		if enableAUTH {
			if hypervisor {
				conf.Hypervisor.EnableAuth = true
			}
		}

		// Check OS and enable auth for windows or macos
		if selectedOS == "windows" || selectedOS == "macos" {
			if hypervisor {
				conf.Hypervisor.EnableAuth = true
			}
		}

		if !stdout {
			// Save config to file.
			if err := conf.Flush(); err != nil {
				logger.WithError(err).Fatal("Failed to flush config to file.")
			}
		}
		// Print results.
		j, err := json.MarshalIndent(conf, "", "\t")
		if err != nil {
			logger.WithError(err).Fatal("An unexpected error occurred. Please contact a developer.")
		}
		if !stdout {
			logger.Infof("Updated file '%s' to: %s", output, j)
		} else {
			fmt.Printf("%s", j)
		}

	},
}

func readOldConfig(log *logging.MasterLogger, confPath string, replace bool, hypervisor bool, services visorconfig.Services) (*visorconfig.V1, bool) {
	raw, err := ioutil.ReadFile(confPath) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false
		}
		logger.WithError(err).Fatal("Unexpected error occurred when attempting to read old config.")
	}

	if !replace {
		logger.Fatal("Config file already exists. Specify the 'replace, r' flag to replace this.")
	}

	conf, err := visorconfig.Parse(log, confPath, raw, hypervisor, services)
	if err != nil {
		logger.WithError(err).Fatal("Failed to parse old config file.")
	}

	return conf, true
}
