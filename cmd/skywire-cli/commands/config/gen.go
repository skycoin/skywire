package config

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/bitfield/script"
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
	confPath          string
	configName        string
	stdout            bool
	regen             bool
	retainHypervisors bool
	testEnv           bool
	pkgEnv            bool
	usrEnv            bool
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
	ver               string
	root              bool
	svcconf           = strings.ReplaceAll(utilenv.ServiceConfAddr, "http://", "")     //skyenv.DefaultServiceConfAddr
	testconf          = strings.ReplaceAll(utilenv.TestServiceConfAddr, "http://", "") //skyenv.DefaultServiceConfAddr
	hiddenflags       []string
)

func init() {
	//disable sorting, flags appear in the order shown here
	genConfigCmd.Flags().SortFlags = false
	RootCmd.AddCommand(genConfigCmd)

	genConfigCmd.Flags().StringVarP(&serviceConfURL, "url", "a", svcconf, "services conf")
	hiddenflags = append(hiddenflags, "url")
	genConfigCmd.Flags().BoolVarP(&bestProtocol, "bestproto", "b", false, "best protocol (dmsg | direct) based on location")
	genConfigCmd.Flags().BoolVarP(&disableauth, "noauth", "c", false, "disable authentication for hypervisor UI")
	hiddenflags = append(hiddenflags, "noauth")
	genConfigCmd.Flags().BoolVarP(&dmsgHTTP, "dmsghttp", "d", false, "use dmsg connection to skywire services")
	hiddenflags = append(hiddenflags, "dmsghttp")
	genConfigCmd.Flags().BoolVarP(&enableauth, "auth", "e", false, "enable auth on hypervisor UI")
	hiddenflags = append(hiddenflags, "auth")
	genConfigCmd.Flags().BoolVarP(&force, "force", "f", false, "remove pre-existing config")
	hiddenflags = append(hiddenflags, "force")
	genConfigCmd.Flags().StringVarP(&disableApps, "disableapps", "g", "", "comma separated list of apps to disable")
	hiddenflags = append(hiddenflags, "disableapps")
	genConfigCmd.Flags().BoolVarP(&hypervisor, "ishv", "i", false, "local hypervisor configuration")
	genConfigCmd.Flags().StringVarP(&hypervisorPKs, "hvpks", "j", "", "list of public keys to use as hypervisor")
	genConfigCmd.Flags().StringVarP(&selectedOS, "os", "k", skyenv.OS, "(linux / macos / windows) paths")
	hiddenflags = append(hiddenflags, "os")
	genConfigCmd.Flags().BoolVarP(&stdout, "stdout", "n", false, "write config to stdout")
	hiddenflags = append(hiddenflags, "stdout")
	genConfigCmd.Flags().StringVarP(&output, "out", "o", "", "output config: "+skyenv.ConfigName)
	genConfigCmd.Flags().BoolVarP(&pkgEnv, "pkg", "p", false, skyenv.Ptext)
	homepath := skyenv.HomePath()
	if homepath != "" {
		genConfigCmd.Flags().BoolVarP(&usrEnv, "user", "u", false, "use paths for user space: "+homepath)
	}
	genConfigCmd.Flags().BoolVarP(&publicRPC, "publicrpc", "q", false, "allow rpc requests from LAN")
	hiddenflags = append(hiddenflags, "publicrpc")
	genConfigCmd.Flags().BoolVarP(&regen, "regen", "r", false, "re-generate existing config & retain keys")
	genConfigCmd.Flags().VarP(&sk, "sk", "s", "a random key is generated if unspecified\n\r")
	hiddenflags = append(hiddenflags, "sk")
	genConfigCmd.Flags().BoolVarP(&testEnv, "testenv", "t", false, "use test deployment "+testconf)
	hiddenflags = append(hiddenflags, "testenv")
	genConfigCmd.Flags().BoolVarP(&vpnServerEnable, "servevpn", "v", false, "enable vpn server")
	hiddenflags = append(hiddenflags, "servevpn")
	genConfigCmd.Flags().BoolVarP(&hide, "hide", "w", false, "dont print the config to the terminal")
	hiddenflags = append(hiddenflags, "hide")
	genConfigCmd.Flags().BoolVarP(&retainHypervisors, "retainhv", "x", false, "retain existing hypervisors with regen")
	hiddenflags = append(hiddenflags, "retainhv")
	genConfigCmd.Flags().StringVar(&ver, "version", "", "custom version testing override")
	hiddenflags = append(hiddenflags, "version")
	genConfigCmd.Flags().BoolVar(&all, "all", false, "show all flags")
	genConfigCmd.Flags().StringVar(&print, "print", "", "parse test ; read config from file & print")
	hiddenflags = append(hiddenflags, "print")

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
			confPath = skyenv.ConfigName
		} else {
			confPath = output
		}

		if output == visorconfig.StdoutName {
			stdout = true
			force = false
			regen = false
		}
		//hide defeats the purpose of stdout.
		if (stdout) && (hide) {
			logger.Fatal("Use of mutually exclusive flags: -w --hide and -n --stdout")
		}
		//--force will delete a config, which excludes --regen
		if (force) && (regen) {
			logger.Fatal("Use of mutually exclusive flags: -f --force cannot override -r --regen")
		}
		// these flags overwrite each other
		if (usrEnv) && (pkgEnv) {
			logger.Fatal("Use of mutually exclusive flags: -u --user and -p --pkg")
		}
		//enable local hypervisor by default for user
		if usrEnv {
			hypervisor = true
		}
		var err error
		if dmsgHTTP {
			dmsgHTTPPath := skyenv.DMSGHTTPName
			if pkgEnv {
				dmsgHTTPPath = skyenv.SkywirePath + "/" + skyenv.DMSGHTTPName
			}
			if _, err := os.Stat(dmsgHTTPPath); err == nil {
				if !stdout {
					logger.Info("Found Dmsghttp config: ", dmsgHTTPPath)
				}
			} else {
				logger.Fatal("Dmsghttp config not found at: ", dmsgHTTPPath)
			}
		}
		if (print == "") && !stdout {
			if confPath, err = filepath.Abs(confPath); err != nil {
				logger.WithError(err).Fatal("Invalid output provided.")
			}
			if force {
				if _, err := os.Stat(confPath); err == nil {
					err := os.Remove(confPath)
					if err != nil {
						logger.WithError(err).Warn("Could not remove file")
					}
				} else {
					logger.Info("Ignoring -f --force flag, config not found.")
				}
			}
			if !regen {
				//check if the config exists
				if _, err := os.Stat(confPath); err == nil {
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
		// skywire-cli config gen -p
		if !stdout && outunset {
			if pkgEnv && (selectedOS == "linux") {
				configName = skyenv.Configjson
				confPath = skyenv.SkywirePath + "/" + configName
			}
			if usrEnv {
				confPath = skyenv.HomePath() + "/" + skyenv.ConfigName
			}
		}
		// Read in old config and obtain old secret key or generate a new random secret key
		// and obtain old hypervisors (if any)
		var sk cipher.SecKey
		if oldConf, err := visorconfig.ReadFile(confPath); err != nil {
			if !stdout {
				_, sk = cipher.GenerateKeyPair()
			}
		} else {
			sk = oldConf.SK
			if retainHypervisors {
				for _, j := range oldConf.Hypervisors {
					hypervisorPKs = hypervisorPKs + "," + fmt.Sprintf("\t%s\n", j)
				}
			}
		}

		//determine best protocol
		if bestProtocol && netutil.LocalProtocol() {
			dmsgHTTP = true
		}

		//create the conf
		conf, err := visorconfig.MakeDefaultConfig(mLog, &sk, usrEnv, pkgEnv, testEnv, dmsgHTTP, hypervisor, confPath, hypervisorPKs, services)
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
		skywire := os.Args[0]
		match := strings.Contains("/tmp/", skywire)
		if (!stdout) || (!match) {
			// Disable apps not found at bin_path with above exceptions for go run and stdout
			if _, err := os.Stat(conf.Launcher.BinPath + "/" + "skychat"); err != nil {
				if disableApps == "" {
					disableApps = "skychat"
				} else {
					disableApps = disableApps + ",skychat"
				}
			}
			if _, err := os.Stat(conf.Launcher.BinPath + "/" + "skysocks"); err != nil {
				if disableApps == "" {
					disableApps = "skysocks"
				} else {
					disableApps = disableApps + ",skysocks"
				}
			}
			if _, err := os.Stat(conf.Launcher.BinPath + "/" + "skysocks-client"); err != nil {
				if disableApps == "" {
					disableApps = "skysocks-client"
				} else {
					disableApps = disableApps + ",skysocks-client"
				}
			}
			if _, err := os.Stat(conf.Launcher.BinPath + "/" + "vpn-client"); err != nil {
				if disableApps == "" {
					disableApps = "vpn-client"
				} else {
					disableApps = disableApps + ",vpn-client"
				}
			}
			if _, err := os.Stat(conf.Launcher.BinPath + "/" + "vpn-server"); err != nil {
				if disableApps == "" {
					disableApps = "vpn-server"
				} else {
					disableApps = disableApps + ",vpn-server"
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
		if (selectedOS == "windows") || (selectedOS == "macos") {
			if hypervisor {
				conf.Hypervisor.EnableAuth = true
			}
		}
		if ver != "" {
			conf.Common.Version = ver
		}
		//don't write file with stdout
		if !stdout {
			userLvl, err := user.Current()
			if err != nil {
				logger.WithError(err).Error("Failed to detect user.")
			} else {
				if userLvl.Username == "root" {
					root = true
				}
			}
			//dont write config as root to non root owned dir & vice versa
			if _, err = exec.LookPath("stat"); err == nil {
				confPath1, _ := filepath.Split(confPath)
				if confPath1 == "" {
					confPath1 = "./"
				}
				logger.Info("confPath: " + confPath)
				owner, err := script.Exec(`stat -c '%U' ` + confPath1).String()
				if err != nil {
					logger.Error("cannot stat: " + confPath1)
				}
				rootOwner, err := script.Exec(`stat -c '%U' /root`).String()
				if err != nil {
					logger.Error("cannot stat: /root")
				}
				if (owner != rootOwner) && root {
					logger.Warn("writing config as root to directory not owned by root")
				}
				if !root && (owner == rootOwner) {
					logger.Fatal("Insufficient permissions to write to the specified path")
				}
			}

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
		//omit logging messages with stdout
		//print config to stdout, omit logging messages, exit
		if stdout {
			fmt.Printf("%s", j)
			os.Exit(0)
		}
		//hide the printing of the config to the terminal
		if hide {
			logger.Infof("Updated file '%s'\n", output)
			os.Exit(0)
		}
		//default behavior
		logger.Infof("Updated file '%s' to:\n%s\n", output, j)
	},
}
