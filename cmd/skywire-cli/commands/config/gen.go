// Package cliconfig gen.go
package cliconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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
	gHiddenFlags = append(gHiddenFlags, "url")
	genConfigCmd.Flags().StringVar(&logLevel, "log-level", "info", "level of logging in config")
	gHiddenFlags = append(gHiddenFlags, "log-level")
	genConfigCmd.Flags().BoolVarP(&isBestProtocol, "bestproto", "b", false, "best protocol (dmsg | direct) based on location")
	genConfigCmd.Flags().BoolVarP(&isDisableAuth, "noauth", "c", false, "disable authentication for hypervisor UI")
	gHiddenFlags = append(gHiddenFlags, "noauth")
	genConfigCmd.Flags().BoolVarP(&isDmsgHTTP, "dmsghttp", "d", false, "use dmsg connection to skywire services")
	gHiddenFlags = append(gHiddenFlags, "dmsghttp")
	genConfigCmd.Flags().BoolVarP(&isEnableAuth, "auth", "e", false, "enable auth on hypervisor UI")
	gHiddenFlags = append(gHiddenFlags, "auth")
	genConfigCmd.Flags().BoolVarP(&isForce, "force", "f", false, "remove pre-existing config")
	gHiddenFlags = append(gHiddenFlags, "force")
	genConfigCmd.Flags().StringVarP(&disableApps, "disableapps", "g", "", "comma separated list of apps to disable")
	gHiddenFlags = append(gHiddenFlags, "disableapps")
	genConfigCmd.Flags().BoolVarP(&isHypervisor, "ishv", "i", false, "local hypervisor configuration")
	genConfigCmd.Flags().StringVarP(&hypervisorPKs, "hvpks", "j", "", "list of public keys to use as hypervisor")
	genConfigCmd.Flags().StringVarP(&selectedOS, "os", "k", skyenv.OS, "(linux / mac / win) paths")
	genConfigCmd.Flags().BoolVarP(&isDisplayNodeIP, "publicip", "l", false, "allow display node ip in services")
	gHiddenFlags = append(gHiddenFlags, "os")
	genConfigCmd.Flags().BoolVarP(&isStdout, "stdout", "n", false, "write config to stdout")
	gHiddenFlags = append(gHiddenFlags, "stdout")
	genConfigCmd.Flags().StringVarP(&output, "out", "o", "", "output config: "+skyenv.ConfigName)
	if skyenv.OS == "win" {
		pText = "use .msi installation path: "
	}
	if skyenv.OS == "linux" {
		pText = "use path for package: "
	}
	if skyenv.OS == "mac" {
		pText = "use mac installation path: "
	}
	genConfigCmd.Flags().BoolVarP(&isPkgEnv, "pkg", "p", false, pText+skyenv.SkywirePath)
	homepath := skyenv.HomePath()
	if homepath != "" {
		genConfigCmd.Flags().BoolVarP(&isUsrEnv, "user", "u", false, "use paths for user space: "+homepath)
	}
	genConfigCmd.Flags().BoolVarP(&isPublicRPC, "publicrpc", "q", false, "allow rpc requests from LAN")
	gHiddenFlags = append(gHiddenFlags, "publicrpc")
	genConfigCmd.Flags().BoolVarP(&isRegen, "regen", "r", false, "re-generate existing config & retain keys")
	genConfigCmd.Flags().VarP(&sk, "sk", "s", "a random key is generated if unspecified\n\r")
	gHiddenFlags = append(gHiddenFlags, "sk")
	genConfigCmd.Flags().BoolVarP(&isTestEnv, "testenv", "t", false, "use test deployment "+testConf)
	gHiddenFlags = append(gHiddenFlags, "testenv")
	genConfigCmd.Flags().BoolVarP(&isVpnServerEnable, "servevpn", "v", false, "enable vpn server")
	gHiddenFlags = append(gHiddenFlags, "servevpn")
	genConfigCmd.Flags().BoolVarP(&isHide, "hide", "w", false, "dont print the config to the terminal")
	gHiddenFlags = append(gHiddenFlags, "hide")
	genConfigCmd.Flags().BoolVarP(&isRetainHypervisors, "retainhv", "x", false, "retain existing hypervisors with regen")
	gHiddenFlags = append(gHiddenFlags, "retainhv")
	genConfigCmd.Flags().BoolVarP(&isPublicAutoConn, "autoconn", "y", false, "disable autoconnect to public visors")
	gHiddenFlags = append(gHiddenFlags, "hide")
	genConfigCmd.Flags().BoolVarP(&isPublic, "public", "z", false, "publicize visor in service discovery")
	gHiddenFlags = append(gHiddenFlags, "public")
	genConfigCmd.Flags().BoolVar(&displayNodeIP, "publicip", false, "display node ip")
	genConfigCmd.Flags().StringVar(&ver, "version", "", "custom version testing override")
	gHiddenFlags = append(gHiddenFlags, "version")
	genConfigCmd.Flags().BoolVar(&isAll, "all", false, "show all flags")
	genConfigCmd.Flags().StringVar(&binPath, "binpath", "", "set bin_path")
	gHiddenFlags = append(gHiddenFlags, "binpath")
	for _, j := range gHiddenFlags {
		genConfigCmd.Flags().MarkHidden(j) //nolint
	}
}

var genConfigCmd = &cobra.Command{
	Use:   "gen",
	Short: "Generate a config file",
	PreRun: func(cmd *cobra.Command, _ []string) {
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
			confPath = skyenv.ConfigName
			output = confPath
		} else {
			confPath = output
		}

		if output == visorconfig.StdoutName {
			isStdout = true
			isForce = false
		}
		if isStdout {
			isRegen = false
		}
		//hide defeats the purpose of stdout.
		if (isStdout) && (isHide) {
			logger.Warn("Use of mutually exclusive flags: -w --hide and -n --stdout")
		}
		//--force will delete a config, which excludes --regen
		if (isForce) && (isRegen) {
			logger.Fatal("Use of mutually exclusive flags: -f --force cannot override -r --regen")
		}
		// these flags overwrite each other
		if (isUsrEnv) && (isPkgEnv) {
			logger.Fatal("Use of mutually exclusive flags: -u --user and -p --pkg")
		}
		//enable local hypervisor by default for user
		if isUsrEnv {
			isHypervisor = true
		}
		var err error
		if isDmsgHTTP {
			dmsgHTTPPath := skyenv.DMSGHTTPName
			if isPkgEnv {
				dmsgHTTPPath = skyenv.SkywirePath + "/" + skyenv.DMSGHTTPName
			}
			if _, err := os.Stat(dmsgHTTPPath); err == nil {
				if !isStdout {
					logger.Info("Found Dmsghttp config: ", dmsgHTTPPath)
				}
			} else {
				logger.Fatal("Dmsghttp config not found at: ", dmsgHTTPPath)
			}
		}
		if !isStdout {
			if confPath, err = filepath.Abs(confPath); err != nil {
				logger.WithError(err).Fatal("Invalid output provided.")
			}
			if isForce {
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
		if !isStdout && isOutUnset {
			if isPkgEnv && (selectedOS == "linux") {
				configName = skyenv.ConfigJSON
				confPath = skyenv.SkywirePath + "/" + configName
				output = confPath
			}
			if isUsrEnv {
				confPath = skyenv.HomePath() + "/" + skyenv.ConfigName
				output = confPath
			}
		}
		if !isRegen && !isStdout {
			//check if the config exists
			if _, err := os.Stat(confPath); err == nil {
				//error config exists !regen
				logger.Fatal("Config file already exists. Specify the '-r --regen' flag to regenerate.")
			}
		}
		//don't write file with stdout
		if !isStdout {
			if skyenv.OS == "linux" {
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
					if (owner != rootOwner) && isRoot {
						logger.Warn("writing config as root to directory not owned by root")
					}
					if !isRoot && (owner == rootOwner) {
						logger.Fatal("Insufficient permissions to write to the specified path")
					}
				}
			}
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		mLog := logging.NewMasterLogger()
		mLog.SetLevel(logrus.InfoLevel)
		serviceConfURL = svcConf
		//use test deployment
		if isTestEnv {
			serviceConfURL = testConf
		}
		//fetch the service endpoints
		services = visorconfig.Fetch(mLog, serviceConfURL, isStdout)
		// Read in old config and obtain old secret key or generate a new random secret key
		// and obtain old hypervisors (if any)
		var sk cipher.SecKey
		if oldConf, err := visorconfig.ReadFile(confPath); err != nil {
			if !isStdout {
				_, sk = cipher.GenerateKeyPair()
			}
		} else {
			sk = oldConf.SK
			if isRetainHypervisors {
				for _, j := range oldConf.Hypervisors {
					hypervisorPKs = hypervisorPKs + "," + fmt.Sprintf("\t%s\n", j)
				}
			}
		}

		//determine best protocol
		if isBestProtocol && netutil.LocalProtocol() {
			isDmsgHTTP = true
		}

		//create the conf
		conf, err := visorconfig.MakeDefaultConfig(mLog, &sk, isUsrEnv, isPkgEnv, isTestEnv, isDmsgHTTP, isHypervisor, confPath, hypervisorPKs, services)
		if err != nil {
			logger.WithError(err).Fatal("Failed to create config.")
		}
		//edit the conf
		// Change rpc address from local to public
		if isPublicRPC {
			conf.CLIAddr = ":3435"
		}
		// Set autostart enable vpn-server
		if isVpnServerEnable {
			for i, app := range conf.Launcher.Apps {
				if app.Name == "vpn-server" {
					conf.Launcher.Apps[i].AutoStart = true
				}
			}
		}
		skywire := os.Args[0]
		isMatch := strings.Contains("/tmp/", skywire)
		if (!isStdout) || (!isMatch) {
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
		if isHypervisor {
			// Make false EnableAuth hypervisor UI by --disable-auth flag
			if isDisableAuth {
				conf.Hypervisor.EnableAuth = false
			}
			// Set EnableAuth true  hypervisor UI by --enable-auth flag
			if isEnableAuth {
				conf.Hypervisor.EnableAuth = true
			}
		}
		// Check OS and enable auth windows or macos
		if (selectedOS == "win") || (selectedOS == "mac") {
			if isHypervisor {
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
		if isPublicAutoConn {
			conf.Transport.PublicAutoconnect = false
		}
		if isPublic {
			conf.IsPublic = true
		}
		//		uncomment after config struct changes
		//		if isDisplayNodeIP {
		//			conf.DisplayNodeIP = true
		//		}

		if displayNodeIP {
			conf.Launcher.DisplayNodeIP = true
		}
		//don't write file with stdout
		if !isStdout {
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
		if isStdout {
			fmt.Printf("%s", j)
			os.Exit(0)
		}
		//hide the printing of the config to the terminal
		if isHide {
			logger.Infof("Updated file '%s'\n", output)
			os.Exit(0)
		}
		//default behavior
		logger.Infof("Updated file '%s' to:\n%s\n", output, j)
	},
}
