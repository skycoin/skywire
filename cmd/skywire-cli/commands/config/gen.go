package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/netutil"
	utilenv "github.com/skycoin/skywire-utilities/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var (
	sk                cipher.SecKey
	output            string
	configName        string
	stdout            bool
	regen             bool
	retainHypervisors bool
	testEnv           bool
	pkgEnv            bool
	hypervisor        bool
	hypervisorPKs     string
	dmsgHTTP          bool
	publicRPC         bool
	vpnServerEnable   bool
	disableauth       bool
	enableauth        bool
	selectedOS        string
	disableApps       string
	bestProtocol      bool
	serviceConfURL    string
	services          *visorconfig.Services
	force             bool
	print             string
	hide              bool
	all               bool
	outunset          bool
	svcconf           = strings.ReplaceAll(utilenv.ServiceConfAddr, "http://", "")     //skyenv.DefaultServiceConfAddr
	testconf          = strings.ReplaceAll(utilenv.TestServiceConfAddr, "http://", "") //skyenv.DefaultServiceConfAddr
	hiddenflags       []string
)

func init() {
	//disable sorting, flags appear in the order shown here
	genConfigCmd.Flags().SortFlags = false
	RootCmd.AddCommand(genConfigCmd)

	genConfigCmd.Flags().StringVarP(&serviceConfURL, "url", "a", svcconf, "services conf")
	genConfigCmd.Flags().BoolVarP(&bestProtocol, "bestproto", "b", false, "best protocol (dmsg | direct) based on location")
	genConfigCmd.Flags().BoolVarP(&disableauth, "noauth", "c", false, "disable authentication for hypervisor UI")
	genConfigCmd.Flags().BoolVarP(&dmsgHTTP, "dmsghttp", "d", false, "use dmsg connection to skywire services")
	genConfigCmd.Flags().BoolVarP(&enableauth, "auth", "e", false, "enable auth on hypervisor UI")
	genConfigCmd.Flags().BoolVarP(&force, "force", "f", false, "remove pre-existing config")
	genConfigCmd.Flags().StringVarP(&disableApps, "disableapps", "g", "", "comma separated list of apps to disable")
	genConfigCmd.Flags().BoolVarP(&hypervisor, "ishv", "i", false, "local hypervisor configuration")
	genConfigCmd.Flags().StringVarP(&hypervisorPKs, "hvpks", "j", "", "list of public keys to use as hypervisor")
	genConfigCmd.Flags().StringVarP(&selectedOS, "os", "k", skyenv.OS, "(linux / macos / windows) paths")
	genConfigCmd.Flags().BoolVarP(&stdout, "stdout", "n", false, "write config to stdout")
	genConfigCmd.Flags().StringVarP(&output, "out", "o", "", "output config default:"+skyenv.ConfigName)
	genConfigCmd.Flags().BoolVarP(&pkgEnv, "package", "p", false, "use paths for package "+skyenv.SkywirePath)
	genConfigCmd.Flags().BoolVarP(&publicRPC, "publicrpc", "q", false, "allow rpc requests from LAN")
	genConfigCmd.Flags().BoolVarP(&regen, "regen", "r", false, "re-generate existing config & retain keys")
	genConfigCmd.Flags().VarP(&sk, "sk", "s", "a random key is generated if unspecified\n\r")
	genConfigCmd.Flags().BoolVarP(&testEnv, "testenv", "t", false, "use test deployment "+testconf)
	genConfigCmd.Flags().BoolVarP(&vpnServerEnable, "servevpn", "v", false, "enable vpn server")
	genConfigCmd.Flags().BoolVarP(&hide, "hide", "w", false, "dont print the config to the terminal")
	genConfigCmd.Flags().BoolVarP(&retainHypervisors, "retainhv", "x", false, "retain existing hypervisors with regen")
	genConfigCmd.Flags().BoolVar(&all, "all", false, "show all flags")
	genConfigCmd.Flags().StringVar(&print, "print", "", "parse test ; read config from file & print")

	hiddenflags = []string{"url", "print", "noauth", "dmsghttp", "auth", "force", "disableapps", "os", "stdout", "publicrpc", "sk", "testenv", "servevpn", "hide", "retainhv", "print"}
	for _, j := range hiddenflags {
		genConfigCmd.Flags().MarkHidden(j) //nolint
	}
}

var genConfigCmd = &cobra.Command{
	Use:   "gen",
	Short: "generate a config file",
	PreRun: func(cmd *cobra.Command, _ []string) {
		//--all unhides flags, prints help menu, and exits
		if all {
			for _, j := range hiddenflags {
				f := cmd.Flags().Lookup(j) //nolint
				f.Hidden = false
			}
			cmd.Flags().MarkHidden("all") //nolint
			cmd.Help()                    //nolint
			os.Exit(0)
		}
		//set default output filename
		if output == "" {
			outunset = true
		} else {
			skyenv.ConfigName = output
		}
		if output == visorconfig.StdoutName {
			stdout = true
			force = false
			regen = false
		}
		//--force will delete a config, which excludes --regen
		if (force) && (regen) {
			logger.Fatal("Use of mutually exclusive flags: -f --force cannot override -r --regen")
		}
		var err error
		//hide defeats the purpose of stdout.
		if (stdout) && (hide) {
			logger.Fatal("Use of mutually exclusive flags: -w --hide and -n --stdout")
		}
		if dmsgHTTP {
			if pkgEnv {
				skyenv.DMSGHTTPPath = skyenv.DmsghttpPath
			}
			if _, err := os.Stat(skyenv.DMSGHTTPPath); err == nil {
				if !stdout {
					logger.Info("Found Dmsghttp config: ", skyenv.DMSGHTTPPath)
				}
			} else {
				logger.Fatal("Dmsghttp config not found at: ", skyenv.DMSGHTTPPath)
			}
		}
		if (print == "") && !stdout {
			if skyenv.ConfigName, err = filepath.Abs(skyenv.ConfigName); err != nil {
				logger.WithError(err).Fatal("Invalid output provided.")
			}
			if force {
				if _, err := os.Stat(skyenv.ConfigName); err == nil {
					err := os.Remove(skyenv.ConfigName)
					if err != nil {
						logger.WithError(err).Warn("Could not remove file")
					}
				} else {
					logger.Info("Ignoring -f --force flag, config not found.")
				}
			}
			if !regen {
				//check if the config exists
				if _, err := os.Stat(skyenv.ConfigName); err == nil {
					//error config exists !regen
					logger.Fatal("Config file already exists. Specify the '-r --regen' flag to regenerate.")
				}
			}
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		mLog := logging.NewMasterLogger()
		mLog.SetLevel(logrus.InfoLevel)
		//print reads in the config and then prints it
		if print != "" {
			Print(mLog)
		}
		//use test deployment
		if testEnv {
			serviceConfURL = testconf
		}
		//fetch the service endpoints
		services = visorconfig.Fetch(mLog, serviceConfURL, stdout)

		// skywire-cli config gen -ip || skywire-cli config gen -p
		if !stdout && outunset && pkgEnv && (selectedOS == "linux") {
			if hypervisor {
				//default config hypervisor
				configName = "skywire.json"
			} else {
				configName = "skywire-visor.json"
			}
			skyenv.ConfigName = skyenv.SkywirePath + "/" + configName
		}

		// Read in old config and obtain old secret key or generate a new random secret key
		var sk cipher.SecKey
		if !stdout {
			if oldConf, err := visorconfig.ReadFile(skyenv.ConfigName); err != nil {
				_, sk = cipher.GenerateKeyPair()
			} else {
				sk = oldConf.SK
			}
		}
		//determine best protocol
		if bestProtocol && netutil.LocalProtocol() {
			dmsgHTTP = true
		}

		// Read in old config (if any) and obtain old hypervisors.
		if retainHypervisors {
			if oldConf, err := visorconfig.ReadFile(skyenv.ConfigName); err != nil {
				for _, j := range oldConf.Hypervisors {
					hypervisorPKs = hypervisorPKs + "," + fmt.Sprintf("\t%s\n", j)
				}
			}
		}
		//create the conf
		conf, err := visorconfig.MakeDefaultConfig(mLog, &sk, pkgEnv, testEnv, dmsgHTTP, hypervisor, hypervisorPKs, services)
		if err != nil {
			logger.WithError(err).Fatal("Failed to create config.")
		}
		//edit the conf
		// Change rpc address from local to public
		if publicRPC {
			conf.CLIAddr = ":3435"
		}
		// Set autostart enable vpn-server
		if vpnServerEnable {
			for i, app := range conf.Launcher.Apps {
				if app.Name == "vpn-server" {
					conf.Launcher.Apps[i].AutoStart = true
				}
			}
		}
		// Disable apps listed on --disable-apps flag
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
		// Set EnableAuth true  hypervisor UI by --enable-auth flag
		if hypervisor {
			if pkgEnv {
				conf.Hypervisor.EnableAuth = true
			}
			// Make false EnableAuth hypervisor UI by --disable-auth flag
			if disableauth {
				conf.Hypervisor.EnableAuth = false
			}
			// Set EnableAuth true  hypervisor UI by --enable-auth flag
			if enableauth {
				conf.Hypervisor.EnableAuth = true
			}
		}
		// Check OS and enable auth windows or macos
		if selectedOS == "windows" || selectedOS == "macos" {
			if hypervisor {
				conf.Hypervisor.EnableAuth = true
			}
		}
		//don't write file with stdout
		if !stdout {
			// Save config to file.
			if err := conf.Flush(skyenv.ConfigName); err != nil {
				logger.WithError(err).Fatal("Failed to flush config to file.")
			}
		}
		// Print results.
		j, err := json.MarshalIndent(conf, "", "\t")
		if err != nil {
			logger.WithError(err).Fatal("Could not unmarshal json.")
		}
		//omit logging messages with stdout
		//print config to stdout, omit logging messages, exit
		if stdout {
			fmt.Printf("%s", j)
			os.Exit(0)
		}
		//hide the printing of the config to the terminal
		if hide {
			logger.Infof("Updated file '%s'\n", skyenv.ConfigName)
			os.Exit(0)
		}
		//default behavior
		logger.Infof("Updated file '%s' to:\n%s\n", skyenv.ConfigName, j)
	},
}
