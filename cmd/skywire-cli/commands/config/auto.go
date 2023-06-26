//go:build exclude

// Package cliconfig cmd/skywire-cli/commands/config/auto.go
package cliconfig

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bitfield/script"
	"github.com/spf13/cobra"

	utilenv "github.com/skycoin/skywire-utilities/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func init() {
	var msg string
	//disable sorting, flags appear in the order shown here
	autoConfigCmd.Flags().SortFlags = false
	RootCmd.AddCommand(autoConfigCmd)

	autoConfigCmd.Flags().StringVarP(&serviceConfURL, "url", "a", scriptExecArray(fmt.Sprintf("${SVCCONFADDR[@]-%s}", utilenv.ServiceConfAddr)), "services conf url\n\r")
	autoConfigCmd.Flags().StringVar(&logLevel, "loglvl", scriptExecString("${LOGLVL:-info}"), "level of logging in config\033[0m")
	autoConfigCmd.Flags().BoolVarP(&isBestProtocol, "bestproto", "b", scriptExecBool("${BESTPROTO:-false}"), "best protocol (dmsg | direct) based on location\033[0m") //this will also disable public autoconnect based on location
	autoConfigCmd.Flags().BoolVarP(&isDisableAuth, "noauth", "c", false, "disable authentication for hypervisor UI\033[0m")
	autoConfigCmd.Flags().BoolVarP(&isDmsgHTTP, "dmsghttp", "d", scriptExecBool("${DMSGHTTP:-false}"), "use dmsg connection to skywire services\033[0m")
	autoConfigCmd.Flags().BoolVarP(&isEnableAuth, "auth", "e", false, "enable auth on hypervisor UI\033[0m")
	autoConfigCmd.Flags().BoolVarP(&isForce, "force", "f", false, "remove pre-existing config\033[0m")
	autoConfigCmd.Flags().StringVarP(&disableApps, "disableapps", "g", "", "comma separated list of apps to disable\033[0m")
	autoConfigCmd.Flags().BoolVarP(&isHypervisor, "ishv", "i", scriptExecBool("${ISHYPERVISOR:-false}"), "local hypervisor configuration\033[0m")
	msg = "list of public keys to add as hypervisor"
	if scriptExecArray("${HYPERVISORPKS[@]}") != "" {
		msg += "\n\r"
	}
	autoConfigCmd.Flags().StringVarP(&hypervisorPKs, "hvpks", "j", scriptExecArray("${HYPERVISORPKS[@]}"), msg)
	msg = "add dmsgpty whitelist PKs"
	if scriptExecArray("${DMSGPTYPKS[@]}") != "" {
		msg += "\n\r"
	}
	autoConfigCmd.Flags().StringVar(&dmsgptywlPKs, "dmsgpty", scriptExecArray("${DMSGPTYPKS[@]}"), msg)
	msg = "add survey whitelist PKs"
	if scriptExecArray("${SURVEYPKS[@]}") != "" {
		msg += "\n\r"
	}

	autoConfigCmd.Flags().StringVar(&surveywhitelistPks, "survey", scriptExecArray("${SURVEYPKS[@]}"), msg)
	msg = "add route setup node PKs"
	if scriptExecArray("${ROUTESETUPPKS[@]}") != "" {
		msg += "\n\r"
	}
	autoConfigCmd.Flags().StringVar(&routesetupnodePks, "routesetup", scriptExecArray("${ROUTESETUPPKS[@]}"), msg)
	msg = "add transport setup node PKs"
	if scriptExecArray("${ROUTESETUPPKS[@]}") != "" {
		msg += "\n\r"
	}
	autoConfigCmd.Flags().StringVar(&transportsetupnodePks, "tpsetup", scriptExecArray("${ROUTESETUPPKS[@]}"), msg)

	autoConfigCmd.Flags().StringVarP(&selectedOS, "os", "k", visorconfig.OS, "(linux / mac / win) paths\033[0m")
	autoConfigCmd.Flags().BoolVarP(&isDisplayNodeIP, "publicip", "l", scriptExecBool("${DISPLAYNODEIP:-false}"), "allow display node ip in services\033[0m")
	autoConfigCmd.Flags().BoolVarP(&addExampleApps, "example-apps", "m", false, "add example apps to the config\033[0m")
	autoConfigCmd.Flags().BoolVarP(&isStdout, "stdout", "n", false, "write config to stdout\033[0m")
	msg = "output config"
	if scriptExecString("${OUTPUT}") == "" {
		msg += ": " + visorconfig.ConfigName
	}
	autoConfigCmd.Flags().StringVarP(&output, "out", "o", scriptExecString("${OUTPUT}"), msg+"\033[0m")
	if visorconfig.OS == "linux" {
		pText = "use path for package: "
	}
	autoConfigCmd.Flags().BoolVarP(&isPkgEnv, "pkg", "p", scriptExecBool("${PKGENV:-false}"), pText+visorconfig.SkywirePath+"\033[0m")
	homepath := visorconfig.HomePath()
	if homepath != "" {
		autoConfigCmd.Flags().BoolVarP(&isUsrEnv, "user", "u", scriptExecBool("${USRENV:-false}"), "use paths for user space: "+homepath+"\033[0m")
	}
	autoConfigCmd.Flags().BoolVarP(&isRegen, "regen", "r", false, "re-generate existing config & retain keys")
	if scriptExecString("${SK:-0000000000000000000000000000000000000000000000000000000000000000}") != "0000000000000000000000000000000000000000000000000000000000000000" {
		sk.Set(scriptExecString("${SK:-0000000000000000000000000000000000000000000000000000000000000000}")) //nolint
	}
	autoConfigCmd.Flags().VarP(&sk, "sk", "s", "a random key is generated if unspecified\n\r")
	autoConfigCmd.Flags().BoolVarP(&isTestEnv, "testenv", "t", scriptExecBool("${TESTENV:-false}"), "use test deployment "+testConf+"\033[0m")
	autoConfigCmd.Flags().BoolVarP(&isVpnServerEnable, "servevpn", "v", scriptExecBool("${SERVEVPN:-false}"), "enable vpn server\033[0m")
	autoConfigCmd.Flags().BoolVarP(&isHide, "hide", "w", false, "dont print the config to the terminal :: show errors with -n flag\033[0m")
	autoConfigCmd.Flags().BoolVarP(&isRetainHypervisors, "retainhv", "x", false, "retain existing hypervisors with regen\033[0m")
	autoConfigCmd.Flags().BoolVarP(&disablePublicAutoConn, "autoconn", "y", scriptExecBool("${DISABLEPUBLICAUTOCONN:-false}"), "disable autoconnect to public visors\033[0m")
	autoConfigCmd.Flags().BoolVarP(&isPublic, "public", "z", scriptExecBool("${VISORISPUBLIC:-false}"), "publicize visor in service discovery\033[0m")
	autoConfigCmd.Flags().IntVar(&stcprPort, "stcpr", scriptExecInt("${STCPRPORT:-0}"), "set tcp transport listening port - 0 for random\033[0m")
	autoConfigCmd.Flags().IntVar(&sudphPort, "sudph", scriptExecInt("${SUDPHPORT:-0}"), "set udp transport listening port - 0 for random\033[0m")
	autoConfigCmd.Flags().BoolVar(&isAll, "all", false, "show all flags")
	autoConfigCmd.Flags().StringVar(&binPath, "binpath", scriptExecString("${BINPATH}"), "set bin_path\033[0m")
	autoConfigCmd.Flags().BoolVarP(&isEnvs, "envs", "q", false, "show the environmental variable settings")
	autoConfigCmd.Flags().BoolVar(&noFetch, "nofetch", false, "do not fetch the services from the service conf url")
	autoConfigCmd.Flags().BoolVar(&noDefaults, "nodefaults", false, "do not use hardcoded defaults for production / test services")
	autoConfigCmd.Flags().StringVar(&ver, "version", scriptExecString("${VERSION}"), "custom version testing override\033[0m")
}

func scriptExecString(s string) string {
	if visorconfig.OS == "windows" {
		var variable, defaultvalue string
		if strings.Contains(s, ":-") {
			parts := strings.SplitN(s, ":-", 2)
			variable = parts[0] + "}"
			defaultvalue = strings.TrimRight(parts[1], "}")
		} else {
			variable = s
			defaultvalue = ""
		}
		out, err := script.Exec(fmt.Sprintf(`powershell -c '$SKYENV = "%s"; if ($SKYENV -ne "" -and (Test-Path $SKYENV)) { . $SKYENV }; echo %s"`, skyenvfile, variable)).String()
		if err == nil {
			if (out == "") || (out == variable) {
				return defaultvalue
			}
			return strings.TrimRight(out, "\n")
		}
		return defaultvalue
	}
	z, err := script.Exec(fmt.Sprintf(`bash -c 'SKYENV=%s ; if [[ $SKYENV != "" ]] && [[ -f $SKYENV ]] ; then source $SKYENV ; fi ; printf "%s"'`, skyenvfile, s)).String()
	if err == nil {
		return strings.TrimSpace(z)
	}
	return ""
}

func scriptExecBool(s string) bool {
	if visorconfig.OS == "windows" {
		var variable string
		if strings.Contains(s, ":-") {
			parts := strings.SplitN(s, ":-", 2)
			variable = parts[0] + "}"
		} else {
			variable = s
		}
		out, err := script.Exec(fmt.Sprintf(`powershell -c '$SKYENV = "%s"; if ($SKYENV -ne "" -and (Test-Path $SKYENV)) { . $SKYENV }; echo %s"`, skyenvfile, variable)).String()
		if err == nil {
			if (out == "") || (out == variable) {
				return false
			}
			b, err := strconv.ParseBool(strings.TrimSpace(strings.TrimRight(out, "\n")))
			if err == nil {
				return b
			}
		}
		return false
	}
	z, err := script.Exec(fmt.Sprintf(`bash -c 'SKYENV=%s ; if [[ $SKYENV != "" ]] && [[ -f $SKYENV ]] ; then source $SKYENV ; fi ; printf "%s"'`, skyenvfile, s)).String()
	if err == nil {
		b, err := strconv.ParseBool(z)
		if err == nil {
			return b
		}
	}

	return false
}

func scriptExecArray(s string) string {
	if visorconfig.OS == "windows" {
		variable := s
		if strings.Contains(variable, "[@]}") {
			variable = strings.TrimRight(variable, "[@]}")
			variable = strings.TrimRight(variable, "{")
		}
		out, err := script.Exec(fmt.Sprintf(`powershell -c '$SKYENV = "%s"; if ($SKYENV -ne "" -and (Test-Path $SKYENV)) { . $SKYENV }; foreach ($item in %s) { Write-Host $item }'`, skyenvfile, variable)).Slice()
		if err == nil {
			if len(out) != 0 {
				return ""
			}
			return strings.Join(out, ",")
		}
	}
	y, err := script.Exec(fmt.Sprintf(`bash -c 'SKYENV=%s ; if [[ $SKYENV != "" ]] && [[ -f $SKYENV ]] ; then source $SKYENV ; fi ; for _i in %s ; do echo "$_i" ; done'`, skyenvfile, s)).Slice()
	if err == nil {
		return strings.Join(y, ",")
	}
	return ""
}

func scriptExecInt(s string) int {
	if visorconfig.OS == "windows" {
		var variable string
		if strings.Contains(s, ":-") {
			parts := strings.SplitN(s, ":-", 2)
			variable = parts[0] + "}"
		} else {
			variable = s
		}
		out, err := script.Exec(fmt.Sprintf(`powershell -c '$SKYENV = "%s"; if ($SKYENV -ne "" -and (Test-Path $SKYENV)) { . $SKYENV }; echo %s"`, skyenvfile, variable)).String()
		if err == nil {
			if (out == "") || (out == variable) {
				return 0
			}
			i, err := strconv.Atoi(strings.TrimSpace(strings.TrimRight(out, "\n")))
			if err == nil {
				return i
			}
			return 0
		}
		return 0
	}
	z, err := script.Exec(fmt.Sprintf(`bash -c 'SKYENV=%s ; if [[ $SKYENV != "" ]] && [[ -f $SKYENV ]] ; then source $SKYENV ; fi ; printf "%s"'`, skyenvfile, s)).String()
	if err == nil {
		if z == "" {
			return 0
		}
		i, err := strconv.Atoi(z)
		if err == nil {
			return i
		}
	}
	return 0
}

var autoConfigCmd = &cobra.Command{
	Use:   "gen",
	Short: "Generate a config file",
	Long: func() string {
		if visorconfig.OS == "linux" {
			if skyenvfile == "" {
				return `Generate a config file

	Config defaults file may also be specified with
	SKYENV=/path/to/skywire.conf skywire-cli config gen`
			}
			if _, err := os.Stat(skyenvfile); err == nil {
				return `Generate a config file

	skyenv file detected: ` + skyenvfile
			}
			return `Generate a config file

	Config defaults file may also be specified with
	SKYENV=/path/to/skywire.conf skywire-cli config gen`
		}
		return `Generate a config file`

	}(),
	PreRun: func(cmd *cobra.Command, _ []string) {
		log := logger
		if isEnvs {
			if visorconfig.OS == "windows" {
				envfile = envfileWindows
			} else {
				envfile = envfileLinux
			}
			fmt.Println(envfile)
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

	},
}
