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
	svcconf           = strings.ReplaceAll(serviceconfaddr, "http://", "") //skyenv.DefaultServiceConfAddr
	testconf          = strings.ReplaceAll(testconfaddr, "http://", "")    //skyenv.DefaultServiceConfAddr
	hiddenflags       []string
)

const serviceconfaddr = "http://conf.skywire.skycoin.com"
const testconfaddr = "http://conf.skywire.dev"

func init() {
	genConfigCmd.Flags().SortFlags = false
	RootCmd.AddCommand(genConfigCmd)

	genConfigCmd.Flags().StringVarP(&serviceConfURL, "url", "a", svcconf, "services conf")
	genConfigCmd.Flags().BoolVarP(&bestProtocol, "bestproto", "b", false, "best protocol (dmsg | direct) based on location")
	genConfigCmd.Flags().BoolVarP(&disableauth, "noauth", "c", false, "disable authentication for hypervisor UI")
	genConfigCmd.Flags().BoolVarP(&dmsgHTTP, "dmsghttp", "d", false, "use dmsg connection to skywire services")
	genConfigCmd.Flags().BoolVarP(&enableauth, "auth", "e", false, "enable auth on hypervisor UI")
	genConfigCmd.Flags().BoolVarP(&force, "force", "f", false, "remove pre-existing config")
	genConfigCmd.Flags().StringVarP(&disableApps, "disableapps", "g", "", "comma separated list of apps to disable")
	genConfigCmd.Flags().BoolVarP(&hypervisor, "ishv", "i", false, "hypervisor configuration")
	genConfigCmd.Flags().StringVarP(&hypervisorPKs, "hvpks", "j", "", "list of public keys to use as hypervisor")
	genConfigCmd.Flags().StringVarP(&selectedOS, "os", "k", "linux", "(linux / macos / windows) paths")
	genConfigCmd.Flags().BoolVarP(&stdout, "stdout", "n", false, "write config to stdout")
	genConfigCmd.Flags().StringVarP(&output, "out", "o", "", "output config default:"+skyenv.ConfigName)
	genConfigCmd.Flags().BoolVarP(&pkgEnv, "package", "p", false, "use paths for package (/opt/skywire)")
	genConfigCmd.Flags().BoolVarP(&publicRPC, "publicrpc", "q", false, "allow rpc requests from LAN")
	genConfigCmd.Flags().BoolVarP(&regen, "regen", "r", false, "re-generate existing config & retain keys")
	genConfigCmd.Flags().VarP(&sk, "sk", "s", "a random key is generated if unspecified\n\r")
	genConfigCmd.Flags().BoolVarP(&testEnv, "testenv", "t", false, "use test deployment "+testconf)
	genConfigCmd.Flags().BoolVarP(&vpnServerEnable, "servevpn", "v", false, "enable vpn server")
	genConfigCmd.Flags().BoolVarP(&hide, "hide", "w", false, "dont print the config to the terminal")
	genConfigCmd.Flags().BoolVarP(&retainHypervisors, "retainhv", "x", false, "retain existing hypervisors with regen")
	genConfigCmd.Flags().BoolVar(&all, "all", false, "show all flags")
	genConfigCmd.Flags().StringVar(&print, "print", "", "parse test ; read config from file & print")

	hiddenflags = []string{"url", "print", "noauth", "dmsghttp", "auth", "force", "disableapps", "stdout", "publicrpc", "sk", "testenv", "servevpn", "hide", "retainhv", "print"}
	for _, j := range hiddenflags {
		genConfigCmd.Flags().MarkHidden(j) //nolint
	}
}

var genConfigCmd = &cobra.Command{
	Use:   "gen",
	Short: "generate a config file",
	PreRun: func(cmd *cobra.Command, _ []string) {
		//unhide flags and print help menu
		if all {
			for _, j := range hiddenflags {
				f := cmd.Flags().Lookup(j) //nolint
				f.Hidden = false
			}
			cmd.Help() //nolint
			os.Exit(0)
		}
		//set default output filename
		if output == "" {
			output = skyenv.ConfigName
			outunset = true
		}
		if (force) && (regen) {
			logger.Fatal("Use of mutually exclusive flags: -f --force cannot override -r --regen")
		}
		var err error
		if output == visorconfig.StdoutName {
			stdout = true
		}
		if (stdout) && (hide) {
			logger.Fatal("Use of mutually exclusive flags: -w --hide and -n --stdout")
		}
		if (print == "") && !stdout {
			if output, err = filepath.Abs(output); err != nil {
				logger.WithError(err).Fatal("Invalid output provided.")
			}
			if force {
				err := os.Remove(output)
				if err != nil {
					logger.WithError(err).Fatal("Could not remove file")
				}
			}
			if !regen {
				//check if the config exists
				if _, err := os.Stat(output); err == nil {
					//error config exists !regen
					logger.Fatal("Config file already exists. Specify the '-r --regen' flag to regenerate.")
				}
			}
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		mLog := logging.NewMasterLogger()
		mLog.SetLevel(logrus.InfoLevel)

		if print != "" {
			Print(mLog)
		}

		if testEnv {
			serviceConfURL = testconf
		}
		services = Fetch(mLog)

		if !stdout {
			//set output package
			if outunset {
				//default config visor
				if pkgEnv {
					configName := "skywire-visor.json"
					//default config hypervisor
					if hypervisor {
						configName = "skywire.json"
					}
					output = filepath.Join(utilenv.PackageSkywirePath(), configName)
				}
			}
		}

		// Read in old config and obtain old secret key or generate a new random secret key
		var sk cipher.SecKey
		if !stdout {
			if oldConf, err := visorconfig.ReadConfig(output); err != nil {
				_, sk = cipher.GenerateKeyPair()
			} else {
				sk = oldConf.SK
			}
		}

		//determine best protocol
		if bestProtocol {
			if netutil.LocalProtocol() {
				dmsgHTTP = true
			}
		}
		// Read in old config (if any) and obtain old hypervisors.
		if retainHypervisors {
			if oldConf, err := visorconfig.ReadConfig(output); err != nil {
				for _, j := range oldConf.Hypervisors {
					hypervisorPKs = hypervisorPKs + "," + fmt.Sprintf("\t%s\n", j)
				}
			}
		}

		conf, err := visorconfig.MakeDefaultConfig(mLog, output, &sk, pkgEnv, testEnv, dmsgHTTP, hypervisor, hypervisorPKs, services)
		if err != nil {
			logger.WithError(err).Fatal("Failed to create config.")
		}

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
		if pkgEnv && hypervisor {
			conf.Hypervisor.EnableAuth = true
		}

		// Make false EnableAuth hypervisor UI by --disable-auth flag
		if disableauth && hypervisor {
			conf.Hypervisor.EnableAuth = false
		}

		// Set EnableAuth true  hypervisor UI by --enable-auth flag
		if enableauth && hypervisor {
			conf.Hypervisor.EnableAuth = true
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
			if err := conf.Flush(); err != nil {
				logger.WithError(err).Fatal("Failed to flush config to file.")
			}
		}
		// Print results.
		j, err := json.MarshalIndent(conf, "", "\t")
		if err != nil {
			logger.WithError(err).Fatal("Could not unmarshal json.")
		}
		//omit logging messages stdout
		if !stdout {
			//hide the printing of the config to the terminal
			if hide {
				logger.Infof("Updated file '%s'", output)
			} else {
				//default behavior
				logger.Infof("Updated file '%s' to: %s", output, j)
			}
		} else {
			//print config to stdout
			fmt.Printf("%s", j)
		}
	},
}
