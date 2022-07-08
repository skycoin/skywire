package cliconfig

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
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/netutil"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func init() {
	//disable sorting, flags appear in the order shown here
	genConfigCmd.Flags().SortFlags = false
	RootCmd.AddCommand(genConfigCmd)

	genConfigCmd.Flags().StringVarP(&serviceConfURL, "url", "a", "", "services conf")
	ghiddenflags = append(ghiddenflags, "url")
	genConfigCmd.Flags().StringVar(&logLevel, "log-level", "info", "level of logging in config")
	ghiddenflags = append(ghiddenflags, "log-level")
	genConfigCmd.Flags().BoolVarP(&bestProtocol, "bestproto", "b", false, "best protocol (dmsg | direct) based on location")
	genConfigCmd.Flags().BoolVarP(&disableauth, "noauth", "c", false, "disable authentication for hypervisor UI")
	ghiddenflags = append(ghiddenflags, "noauth")
	genConfigCmd.Flags().BoolVarP(&dmsgHTTP, "dmsghttp", "d", false, "use dmsg connection to skywire services")
	ghiddenflags = append(ghiddenflags, "dmsghttp")
	genConfigCmd.Flags().BoolVarP(&enableauth, "auth", "e", false, "enable auth on hypervisor UI")
	ghiddenflags = append(ghiddenflags, "auth")
	genConfigCmd.Flags().BoolVarP(&force, "force", "f", false, "remove pre-existing config")
	ghiddenflags = append(ghiddenflags, "force")
	genConfigCmd.Flags().StringVarP(&disableApps, "disableapps", "g", "", "comma separated list of apps to disable")
	ghiddenflags = append(ghiddenflags, "disableapps")
	genConfigCmd.Flags().BoolVarP(&hypervisor, "ishv", "i", false, "local hypervisor configuration")
	genConfigCmd.Flags().StringVarP(&hypervisorPKs, "hvpks", "j", "", "list of public keys to use as hypervisor")
	genConfigCmd.Flags().StringVarP(&selectedOS, "os", "k", skyenv.OS, "(linux / mac / win) paths")
	ghiddenflags = append(ghiddenflags, "os")
	genConfigCmd.Flags().BoolVarP(&stdout, "stdout", "n", false, "write config to stdout")
	ghiddenflags = append(ghiddenflags, "stdout")
	genConfigCmd.Flags().StringVarP(&output, "out", "o", "", "output config: "+skyenv.ConfigName)
	if skyenv.OS == "win" {
		ptext = "use .msi installation path: "
	}
	if skyenv.OS == "linux" {
		ptext = "use path for package: "
	}
	if skyenv.OS == "mac" {
		ptext = "use mac installation path: "
	}
	genConfigCmd.Flags().BoolVarP(&pkgEnv, "pkg", "p", false, ptext+skyenv.SkywirePath)
	homepath := skyenv.HomePath()
	if homepath != "" {
		genConfigCmd.Flags().BoolVarP(&usrEnv, "user", "u", false, "use paths for user space: "+homepath)
	}
	genConfigCmd.Flags().BoolVarP(&publicRPC, "publicrpc", "q", false, "allow rpc requests from LAN")
	ghiddenflags = append(ghiddenflags, "publicrpc")
	genConfigCmd.Flags().BoolVarP(&regen, "regen", "r", false, "re-generate existing config & retain keys")
	genConfigCmd.Flags().VarP(&sk, "sk", "s", "a random key is generated if unspecified\n\r")
	ghiddenflags = append(ghiddenflags, "sk")
	genConfigCmd.Flags().BoolVarP(&testEnv, "testenv", "t", false, "use test deployment "+testconf)
	ghiddenflags = append(ghiddenflags, "testenv")
	genConfigCmd.Flags().BoolVarP(&vpnServerEnable, "servevpn", "v", false, "enable vpn server")
	ghiddenflags = append(ghiddenflags, "servevpn")
	genConfigCmd.Flags().BoolVarP(&hide, "hide", "w", false, "dont print the config to the terminal")
	ghiddenflags = append(ghiddenflags, "hide")
	genConfigCmd.Flags().BoolVarP(&retainHypervisors, "retainhv", "x", false, "retain existing hypervisors with regen")
	ghiddenflags = append(ghiddenflags, "retainhv")
	genConfigCmd.Flags().StringVar(&ver, "version", "", "custom version testing override")
	ghiddenflags = append(ghiddenflags, "version")
	genConfigCmd.Flags().BoolVar(&all, "all", false, "show all flags")
	genConfigCmd.Flags().StringVar(&binPath, "binpath", "", "set bin_path")
	ghiddenflags = append(ghiddenflags, "binpath")
	for _, j := range ghiddenflags {
		genConfigCmd.Flags().MarkHidden(j) //nolint
	}
}

var genConfigCmd = &cobra.Command{
	Use:   "gen",
	Short: "Generate a config file",
	PreRun: func(cmd *cobra.Command, _ []string) {
		//--all unhides flags, prints help menu, and exits
		if all {
			for _, j := range ghiddenflags {
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
			output = confPath
		} else {
			confPath = output
		}

		if output == visorconfig.StdoutName {
			stdout = true
			force = false
		}
		if stdout {
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
		if !stdout {
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
		}
		// skywire-cli config gen -p
		if !stdout && outunset {
			if pkgEnv && (selectedOS == "linux") {
				configName = skyenv.Configjson
				confPath = skyenv.SkywirePath + "/" + configName
				output = confPath
			}
			if usrEnv {
				confPath = skyenv.HomePath() + "/" + skyenv.ConfigName
				output = confPath
			}
		}
		if !regen && !stdout {
			//check if the config exists
			if _, err := os.Stat(confPath); err == nil {
				//error config exists !regen
				logger.Fatal("Config file already exists. Specify the '-r --regen' flag to regenerate.")
			}
		}
		//don't write file with stdout
		if !stdout {
			if skyenv.OS == "linux" {
				userLvl, err := user.Current()
				if err != nil {
					logger.WithError(err).Error("Failed to detect user.")
				} else {
					if userLvl.Username == "root" {
						root = true
					}
				}
				//warn when writing config as root to non root owned dir & fail on the reverse instance
				if _, err = exec.LookPath("stat"); err == nil {
					confPath1, _ := filepath.Split(confPath)
					if confPath1 == "" {
						confPath1 = "./"
					}
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
			}
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		mLog := logging.NewMasterLogger()
		mLog.SetLevel(logrus.InfoLevel)
		serviceConfURL = svcconf
		//use test deployment
		if testEnv {
			serviceConfURL = testconf
		}
		//fetch the service endpoints
		services = visorconfig.Fetch(mLog, serviceConfURL, stdout)
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
			//binaries have .exe extension on windows
			var exe string
			if skyenv.OS == "win" {
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
		// Set EnableAuth true  hypervisor UI by --enable-auth flag
		if hypervisor {
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
		if (selectedOS == "win") || (selectedOS == "mac") {
			if hypervisor {
				conf.Hypervisor.EnableAuth = true
			}
		}
		// Set log level
		if logLevel != "" {
			if logLevel == "trace" || logLevel == "debug" {
				conf.LogLevel = logLevel
			}
		}
		// check binpath argument and use if set
		if binPath != "" {
			conf.Launcher.BinPath = binPath
		}

		if ver != "" {
			conf.Common.Version = ver
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
