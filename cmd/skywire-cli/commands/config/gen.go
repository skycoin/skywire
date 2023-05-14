// Package cliconfig cmd/skywire-cli/commands/config/gen.go
package cliconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bitfield/script"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/netutil"
	utilenv "github.com/skycoin/skywire-utilities/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var isEnvs bool

func init() {
	var msg string
	//disable sorting, flags appear in the order shown here
	genConfigCmd.Flags().SortFlags = false
	RootCmd.AddCommand(genConfigCmd)

	genConfigCmd.Flags().StringVarP(&serviceConfURL, "url", "a", scriptExecString(fmt.Sprintf("${SVCCONFADDR:-%s}", utilenv.ServiceConfAddr)), "services conf url\n\r")
	gHiddenFlags = append(gHiddenFlags, "url")
	genConfigCmd.Flags().StringVar(&logLevel, "loglvl", scriptExecString("${LOGLVL:-info}"), "level of logging in config\033[0m")
	gHiddenFlags = append(gHiddenFlags, "loglvl")
	genConfigCmd.Flags().BoolVarP(&isBestProtocol, "bestproto", "b", scriptExecBool("${BESTPROTO:-false}"), "best protocol (dmsg | direct) based on location") //this will also disable public autoconnect based on location
	genConfigCmd.Flags().BoolVarP(&isDisableAuth, "noauth", "c", false, "disable authentication for hypervisor UI")
	gHiddenFlags = append(gHiddenFlags, "noauth")
	genConfigCmd.Flags().BoolVarP(&isDmsgHTTP, "dmsghttp", "d", scriptExecBool("${DMSGHTTP:-false}"), "use dmsg connection to skywire services")
	gHiddenFlags = append(gHiddenFlags, "dmsghttp")
	genConfigCmd.Flags().BoolVarP(&isEnableAuth, "auth", "e", false, "enable auth on hypervisor UI")
	gHiddenFlags = append(gHiddenFlags, "auth")
	genConfigCmd.Flags().BoolVarP(&isForce, "force", "f", false, "remove pre-existing config")
	gHiddenFlags = append(gHiddenFlags, "force")
	genConfigCmd.Flags().StringVarP(&disableApps, "disableapps", "g", "", "comma separated list of apps to disable")
	gHiddenFlags = append(gHiddenFlags, "disableapps")
	genConfigCmd.Flags().BoolVarP(&isHypervisor, "ishv", "i", scriptExecBool("${ISHYPERVISOR:-false}"), "local hypervisor configuration")
	msg = "list of public keys to use as hypervisor"
	if scriptExecString("${OUTPUT}") == "" {
		msg += "\n\r"
	}
	genConfigCmd.Flags().StringVarP(&hypervisorPKs, "hvpks", "j", scriptExecArray("${HYPERVISORPKS[@]}"), msg)
	genConfigCmd.Flags().StringVarP(&selectedOS, "os", "k", visorconfig.OS, "(linux / mac / win) paths\033[0m")
	gHiddenFlags = append(gHiddenFlags, "os")
	genConfigCmd.Flags().BoolVarP(&isDisplayNodeIP, "publicip", "l", scriptExecBool("${DISPLAYNODEIP:-false}"), "allow display node ip in services")
	gHiddenFlags = append(gHiddenFlags, "publicip")
	genConfigCmd.Flags().BoolVarP(&addExampleApps, "example-apps", "m", false, "add example apps to the config")
	gHiddenFlags = append(gHiddenFlags, "example-apps")
	genConfigCmd.Flags().BoolVarP(&isStdout, "stdout", "n", false, "write config to stdout")
	gHiddenFlags = append(gHiddenFlags, "stdout")
	msg = "output config"
	if scriptExecString("${OUTPUT}") == "" {
		msg += ": " + visorconfig.ConfigName
	}
	genConfigCmd.Flags().StringVarP(&output, "out", "o", scriptExecString("${OUTPUT}"), msg)
	if visorconfig.OS == "win" {
		pText = "use .msi installation path: "
	}
	if visorconfig.OS == "linux" {
		pText = "use path for package: "
	}
	if visorconfig.OS == "mac" {
		pText = "use mac installation path: "
	}
	genConfigCmd.Flags().BoolVarP(&isPkgEnv, "pkg", "p", scriptExecBool("${PKGENV:-false}"), pText+"\033[0m"+visorconfig.SkywirePath)
	homepath := visorconfig.HomePath()
	if homepath != "" {
		genConfigCmd.Flags().BoolVarP(&isUsrEnv, "user", "u", scriptExecBool("${USRENV:-false}"), "use paths for user space: \033[0m"+homepath)
	}
	genConfigCmd.Flags().BoolVarP(&isRegen, "regen", "r", false, "re-generate existing config & retain keys")
	if scriptExecString("${SK:-0000000000000000000000000000000000000000000000000000000000000000}") != "0000000000000000000000000000000000000000000000000000000000000000" {
		sk.Set(scriptExecString("${SK:-0000000000000000000000000000000000000000000000000000000000000000}"))
	}
	genConfigCmd.Flags().VarP(&sk, "sk", "s", "a random key is generated if unspecified\n\r")
	gHiddenFlags = append(gHiddenFlags, "sk")
	genConfigCmd.Flags().BoolVarP(&isTestEnv, "testenv", "t", scriptExecBool("${TESTENV:-false}"), "use test deployment "+testConf)
	gHiddenFlags = append(gHiddenFlags, "testenv")
	genConfigCmd.Flags().BoolVarP(&isVpnServerEnable, "servevpn", "v", scriptExecBool("${SERVEVPN:-false}"), "enable vpn server")
	gHiddenFlags = append(gHiddenFlags, "servevpn")
	genConfigCmd.Flags().BoolVarP(&isHide, "hide", "w", false, "dont print the config to the terminal")
	gHiddenFlags = append(gHiddenFlags, "hide")
	genConfigCmd.Flags().BoolVarP(&isRetainHypervisors, "retainhv", "x", false, "retain existing hypervisors with regen")
	gHiddenFlags = append(gHiddenFlags, "retainhv")
	genConfigCmd.Flags().BoolVarP(&disablePublicAutoConn, "autoconn", "y", scriptExecBool("${DISABLEPUBLICAUTOCONN:-false}"), "disable autoconnect to public visors")
	gHiddenFlags = append(gHiddenFlags, "hide")
	genConfigCmd.Flags().BoolVarP(&isPublic, "public", "z", scriptExecBool("${VISORISPUBLIC:-false}"), "publicize visor in service discovery")
	gHiddenFlags = append(gHiddenFlags, "public")
	//	genConfigCmd.Flags().BoolVar(&isDisplayNodeIP, "publicip", false, "display node ip")
	genConfigCmd.Flags().StringVar(&ver, "version", scriptExecString("${VERSION}"), "custom version testing override\033[0m")
	gHiddenFlags = append(gHiddenFlags, "version")
	genConfigCmd.Flags().BoolVar(&isAll, "all", false, "show all flags")
	genConfigCmd.Flags().StringVar(&binPath, "binpath", scriptExecString("${BINPATH}"), "set bin_path\033[0m")
	gHiddenFlags = append(gHiddenFlags, "binpath")
	genConfigCmd.Flags().BoolVarP(&isEnvs, "envs", "q", false, "show the environmental variable settings")
	gHiddenFlags = append(gHiddenFlags, "envs")

	//show all flags on help
	if os.Getenv("UNHIDEFLAGS") != "1" {
		for _, j := range gHiddenFlags {
			genConfigCmd.Flags().MarkHidden(j) //nolint
		}
	}
}

func scriptExecString(s string) string {
	z, _ := script.Exec(fmt.Sprintf(`bash -c 'SKYENV=%s ; if [[ $SKYENV != "" ]] && [[ -f $SKYENV ]] ; then source $SKYENV ; fi ; printf "%s"'`, skyenvfile, s)).String()
	return z
}
func scriptExecBool(s string) bool {
	z, _ := script.Exec(fmt.Sprintf(`bash -c 'SKYENV=%s ; if [[ $SKYENV != "" ]] && [[ -f $SKYENV ]] ; then source $SKYENV ; fi ; printf "%s"'`, skyenvfile, s)).String()
	b, err := strconv.ParseBool(z)
	if err == nil {
		return b
	}
	return false
}
func scriptExecArray(s string) string {
	y, _ := script.Exec(fmt.Sprintf(`bash -c 'SKYENV=%s ; if [[ $SKYENV != "" ]] && [[ -f $SKYENV ]] ; then source $SKYENV ; fi ; for _i in %s ; do echo "$_i" ; done'`, skyenvfile, s)).Slice()
	strings.Join(y, ",")
	return strings.Join(y, ",")
}

var genConfigCmd = &cobra.Command{
	Use: "gen",
	Short: func() string {
		if skyenvfile == "" {
			return "Generate a config file\nConfig defaults file may also be specified with SKYENV=/path/to/skywire.conf"
		}
		if _, err := os.Stat(skyenvfile); err == nil {
			return "Generate a config file\n skyenv file detected: " + skyenvfile
		}
		return "Generate a config file\nConfig defaults file may also be specified with SKYENV=/path/to/skywire.conf"
	}(),
	PreRun: func(cmd *cobra.Command, _ []string) {
		if isEnvs {
			fmt.Printf("\n")
			fmt.Printf("#DEFAULT VALUES ARE SHOWN\n")
			fmt.Printf("#Other Visors will automatically establish transports to this visor\n#requires port forwarding or public ip\n#VISORISPUBLIC=false\n")
			fmt.Printf("#Autostart vpn server for this visor\n#VPNSERVER=false\n")
			fmt.Printf("#Use test deployment\n#TESTENV=false\n")
			fmt.Printf("#Automatically determine the best protocol (dmsg or http) based on location to connect to the production deployment\n#BESTPROTO=false\n")
			fmt.Printf("#Set custom service conf URLs\n#SVCCONFADDR=('')\n")
			fmt.Printf("#Set visor runtime log level\n#LOGLVL=info\n")
			fmt.Printf("#Use dmsghttp to connect to the production deployment\n#DMSGHTTP=false\n")
			fmt.Printf("#Start the hypervisor interface for this visor\n#ISHYPERVISOR=false\n")
			fmt.Printf("#Output path of the config file\n#OUTPUT='./skywire-config.json'\n")
			fmt.Printf("#Display the node ip in the service discovery for any public services this visor is running\n#DISPLAYNODEIP=false\n")
			fmt.Printf("#Set remote hypervisor public keys\n#HYPERVISORPKS=('')\n")
			fmt.Printf("#Default config paths for the installer or package (system paths)\n#PKGENV=false\n")
			fmt.Printf("#Default config paths for the current userspace\n#USRENV=false\n")
			fmt.Printf("#Set secret key\n#SK=''\n")
			fmt.Printf("#Disable auto-transports to public visors\n#DISABLEPUBLICAUTOCONN=false\n")
			fmt.Printf("#Custom config version override\n#VERSION=''\n")
			fmt.Printf("#Set app bin_path\n#BINPATH='./apps'\n")
			fmt.Printf("\n\r")
			os.Exit(0)
		}

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
			dmsgHTTPPath := visorconfig.DMSGHTTPName
			if isPkgEnv {
				dmsgHTTPPath = visorconfig.SkywirePath + "/" + visorconfig.DMSGHTTPName
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
				logger.Fatal("Config file already exists. Specify the '-r --regen' flag to regenerate.")
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
		if serviceConfURL == "" {
			serviceConfURL = utilenv.ServiceConfAddr
		}
		//use test deployment
		if isTestEnv {
			serviceConfURL = utilenv.TestServiceConfAddr
		}
		//fetch the service endpoints
		services = visorconfig.Fetch(mLog, serviceConfURL, isStdout)
		// Read in old config and obtain old secret key or generate a new random secret key
		// and obtain old hypervisors (if any)
		if !isStdout {
			if oldConf, err := visorconfig.ReadFile(confPath); err != nil {
				_, sk = cipher.GenerateKeyPair()
			} else {
				sk = oldConf.SK
				if isRetainHypervisors {
					for _, j := range oldConf.Hypervisors {
						hypervisorPKs = hypervisorPKs + "," + fmt.Sprintf("\t%s\n", j)
					}
				}
			}
		}

		//determine best protocol
		if isBestProtocol && netutil.LocalProtocol() {
			disablePublicAutoConn = true
			isDmsgHTTP = true

		}

		//create the conf
		conf, err := visorconfig.MakeDefaultConfig(mLog, &sk, isUsrEnv, isPkgEnv, isTestEnv, isDmsgHTTP, isHypervisor, confPath, hypervisorPKs, services)
		if err != nil {
			logger.WithError(err).Fatal("Failed to create config.")
		}
		//edit the conf
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
		if disablePublicAutoConn {
			conf.Transport.PublicAutoconnect = false
		}
		if isPublic {
			conf.IsPublic = true
		}
		if isDisplayNodeIP {
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
