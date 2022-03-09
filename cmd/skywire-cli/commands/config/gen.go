package config

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
	coinCipher "github.com/skycoin/skycoin/src/cipher"
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/netutil"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func init() {
	RootCmd.AddCommand(genConfigCmd)
}

var (
	sk                 cipher.SecKey
	output             string
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
)

func init() {
	genConfigCmd.Flags().Var(&sk, "sk", "if unspecified, a random key pair will be generated.\n")
	genConfigCmd.Flags().StringVarP(&output, "output", "o", "skywire-config.json", "path of output config file.")
	genConfigCmd.Flags().BoolVarP(&replace, "replace", "r", false, "rewrite existing config (retains keys).")
	genConfigCmd.Flags().BoolVarP(&replaceHypervisors, "use-old-hypervisors", "x", false, "use old hypervisors keys.")
	genConfigCmd.Flags().BoolVarP(&packageConfig, "package", "p", false, "use defaults for package-based installations in /opt/skywire")
	genConfigCmd.Flags().BoolVarP(&testEnv, "testenv", "t", false, "use test deployment service.")
	genConfigCmd.Flags().BoolVarP(&hypervisor, "is-hypervisor", "i", false, "generate a hypervisor configuration.")
	genConfigCmd.Flags().StringVar(&hypervisorPKs, "hypervisor-pks", "", "public keys of hypervisors that should be added to this visor")
	genConfigCmd.Flags().BoolVarP(&dmsgHTTP, "dmsghttp", "d", false, "connect to Skywire Services via dmsg")
	genConfigCmd.Flags().BoolVar(&publicRPC, "public-rpc", false, "change rpc service to public.")
	genConfigCmd.Flags().BoolVar(&vpnServerEnable, "vpn-server-enable", false, "enable vpn server in generated config.")
	genConfigCmd.Flags().BoolVar(&disableAUTH, "disable-auth", false, "disable auth on hypervisor UI.")
	genConfigCmd.Flags().BoolVar(&enableAUTH, "enable-auth", false, "enable auth on hypervisor UI.")
	genConfigCmd.Flags().StringVar(&selectedOS, "os", "linux", "generate configuration with paths for 'macos' or 'windows'")
	genConfigCmd.Flags().StringVar(&disableApps, "disable-apps", "", "set list of apps to disable, separated by ','")
	genConfigCmd.Flags().BoolVarP(&bestProtocol, "best-protocol", "b", false, "choose best protocol (dmsg / direct) to connect based on location")
}

var genConfigCmd = &cobra.Command{
	Use:   "gen",
	Short: "Generates a config file",
	PreRun: func(_ *cobra.Command, _ []string) {
		var err error
		if output, err = filepath.Abs(output); err != nil {
			logger.WithError(err).Fatal("Invalid output provided.")
		}
	},
	Run: func(cmd *cobra.Command, _ []string) {
		mLog := logging.NewMasterLogger()
		mLog.SetLevel(logrus.InfoLevel)

		//Fail on -pt combination
		if packageConfig && testEnv {
			logger.Fatal("Failed to create config: use of mutually exclusive flags")
		}

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

		// Read in old config (if any) and obtain old secret key.
		// Otherwise, we generate a new random secret key.
		var sk cipher.SecKey
		if oldConf, ok := readOldConfig(mLog, output, replace); !ok {
			_, sk = cipher.GenerateKeyPair()
		} else {
			sk = oldConf.SK
		}

		// Determine config type to generate.
		var genConf func(log *logging.MasterLogger, confPath string, sk *cipher.SecKey, hypervisor bool) (*visorconfig.V1, error)

		//  default paths for different installations
		if packageConfig {
			genConf = visorconfig.MakePackageConfig
		} else if testEnv {
			genConf = visorconfig.MakeTestConfig
		} else {
			genConf = visorconfig.MakeDefaultConfig
		}

		// Generate config.
		conf, err := genConf(mLog, output, &sk, hypervisor)
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
					conf, err = genConf(mLog, output, &sk, hypervisor)
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
			}
		}

		// Read in old config (if any) and obtain old hypervisors.
		if replaceHypervisors {
			if oldConf, ok := readOldConfig(mLog, output, true); ok {
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

		// Make true EnableAuth for hypervisor UI by --enable-auth flag
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

		// Save config to file.
		if err := conf.Flush(); err != nil {
			logger.WithError(err).Fatal("Failed to flush config to file.")
		}

		// Print results.
		j, err := json.MarshalIndent(conf, "", "\t")
		if err != nil {
			logger.WithError(err).Fatal("An unexpected error occurred. Please contact a developer.")
		}
		logger.Infof("Updated file '%s' to: %s", output, j)
	},
}

func readOldConfig(log *logging.MasterLogger, confPath string, replace bool) (*visorconfig.V1, bool) {
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

	conf, err := visorconfig.Parse(log, confPath, raw)
	if err != nil {
		logger.WithError(err).Fatal("Failed to parse old config file.")
	}

	return conf, true
}
