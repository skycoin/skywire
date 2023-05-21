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
	"strconv"
	"strings"
	"time"

	"github.com/bitfield/script"
	"github.com/sirupsen/logrus"
	"github.com/skycoin/dmsg/pkg/disc"
	"github.com/skycoin/dmsg/pkg/dmsgpty"
	coinCipher "github.com/skycoin/skycoin/src/cipher"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
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

var (
	isEnvs     bool
	skyenvfile = os.Getenv("SKYENV")
)
var envfile string

func init() {

	var msg string
	//disable sorting, flags appear in the order shown here
	genConfigCmd.Flags().SortFlags = false
	RootCmd.AddCommand(genConfigCmd)

	genConfigCmd.Flags().StringVarP(&serviceConfURL, "url", "a", scriptExecArray(fmt.Sprintf("${SVCCONFADDR[@]-%s}", utilenv.ServiceConfAddr)), "services conf url\n\r")
	gHiddenFlags = append(gHiddenFlags, "url")
	genConfigCmd.Flags().StringVar(&logLevel, "loglvl", scriptExecString("${LOGLVL:-info}"), "level of logging in config\033[0m")
	gHiddenFlags = append(gHiddenFlags, "loglvl")
	genConfigCmd.Flags().BoolVarP(&isBestProtocol, "bestproto", "b", scriptExecBool("${BESTPROTO:-false}"), "best protocol (dmsg | direct) based on location\033[0m") //this will also disable public autoconnect based on location
	genConfigCmd.Flags().BoolVarP(&isDisableAuth, "noauth", "c", false, "disable authentication for hypervisor UI\033[0m")
	gHiddenFlags = append(gHiddenFlags, "noauth")
	genConfigCmd.Flags().BoolVarP(&isDmsgHTTP, "dmsghttp", "d", scriptExecBool("${DMSGHTTP:-false}"), "use dmsg connection to skywire services\033[0m")
	gHiddenFlags = append(gHiddenFlags, "dmsghttp")
	genConfigCmd.Flags().BoolVarP(&isEnableAuth, "auth", "e", false, "enable auth on hypervisor UI\033[0m")
	gHiddenFlags = append(gHiddenFlags, "auth")
	genConfigCmd.Flags().BoolVarP(&isForce, "force", "f", false, "remove pre-existing config\033[0m")
	gHiddenFlags = append(gHiddenFlags, "force")
	genConfigCmd.Flags().StringVarP(&disableApps, "disableapps", "g", "", "comma separated list of apps to disable\033[0m")
	gHiddenFlags = append(gHiddenFlags, "disableapps")
	genConfigCmd.Flags().BoolVarP(&isHypervisor, "ishv", "i", scriptExecBool("${ISHYPERVISOR:-false}"), "local hypervisor configuration\033[0m")
	msg = "list of public keys to add as hypervisor"
	if scriptExecArray("${HYPERVISORPKS[@]}") != "" {
		msg += "\n\r"
	}
	genConfigCmd.Flags().StringVarP(&hypervisorPKs, "hvpks", "j", scriptExecArray("${HYPERVISORPKS[@]}"), msg)
	msg = "add dmsgpty whitelist PKs"
	if scriptExecArray("${DMSGPTYPKS[@]}") != "" {
		msg += "\n\r"
	}
	genConfigCmd.Flags().StringVar(&dmsgptywlPKs, "dmsgpty", scriptExecArray("${DMSGPTYPKS[@]}"), msg)
	msg = "add survey whitelist PKs"
	if scriptExecArray("${SURVEYPKS[@]}") != "" {
		msg += "\n\r"
	}

	genConfigCmd.Flags().StringVar(&surveywhitelistPks, "survey", scriptExecArray("${SURVEYPKS[@]}"), msg)
	gHiddenFlags = append(gHiddenFlags, "survey")
	msg = "add route setup node PKs"
	if scriptExecArray("${ROUTESETUPPKS[@]}") != "" {
		msg += "\n\r"
	}
	genConfigCmd.Flags().StringVar(&routesetupnodePks, "routesetup", scriptExecArray("${ROUTESETUPPKS[@]}"), msg)
	gHiddenFlags = append(gHiddenFlags, "routesetup")
	msg = "add transport setup node PKs"
	if scriptExecArray("${ROUTESETUPPKS[@]}") != "" {
		msg += "\n\r"
	}
	genConfigCmd.Flags().StringVar(&transportsetupnodePks, "tpsetup", scriptExecArray("${ROUTESETUPPKS[@]}"), msg)
	gHiddenFlags = append(gHiddenFlags, "tpsetup")

	genConfigCmd.Flags().StringVarP(&selectedOS, "os", "k", visorconfig.OS, "(linux / mac / win) paths\033[0m")
	gHiddenFlags = append(gHiddenFlags, "os")
	genConfigCmd.Flags().BoolVarP(&isDisplayNodeIP, "publicip", "l", scriptExecBool("${DISPLAYNODEIP:-false}"), "allow display node ip in services\033[0m")
	gHiddenFlags = append(gHiddenFlags, "publicip")
	genConfigCmd.Flags().BoolVarP(&addExampleApps, "example-apps", "m", false, "add example apps to the config\033[0m")
	gHiddenFlags = append(gHiddenFlags, "example-apps")
	genConfigCmd.Flags().BoolVarP(&isStdout, "stdout", "n", false, "write config to stdout\033[0m")
	gHiddenFlags = append(gHiddenFlags, "stdout")
	msg = "output config"
	if scriptExecString("${OUTPUT}") == "" {
		msg += ": " + visorconfig.ConfigName
	}
	genConfigCmd.Flags().StringVarP(&output, "out", "o", scriptExecString("${OUTPUT}"), msg+"\033[0m")
	if visorconfig.OS == "win" {
		pText = "use .msi installation path: "
	}
	if visorconfig.OS == "linux" {
		pText = "use path for package: "
	}
	if visorconfig.OS == "mac" {
		pText = "use mac installation path: "
	}
	genConfigCmd.Flags().BoolVarP(&isPkgEnv, "pkg", "p", scriptExecBool("${PKGENV:-false}"), pText+visorconfig.SkywirePath+"\033[0m")
	homepath := visorconfig.HomePath()
	if homepath != "" {

		genConfigCmd.Flags().BoolVarP(&isUsrEnv, "user", "u", scriptExecBool("${USRENV:-false}"), "use paths for user space: "+homepath+"\033[0m")
	}
	genConfigCmd.Flags().BoolVarP(&isRegen, "regen", "r", false, "re-generate existing config & retain keys")
	if scriptExecString("${SK:-0000000000000000000000000000000000000000000000000000000000000000}") != "0000000000000000000000000000000000000000000000000000000000000000" {
		sk.Set(scriptExecString("${SK:-0000000000000000000000000000000000000000000000000000000000000000}")) //nolint
	}
	genConfigCmd.Flags().VarP(&sk, "sk", "s", "a random key is generated if unspecified\n\r")
	gHiddenFlags = append(gHiddenFlags, "sk")
	genConfigCmd.Flags().BoolVarP(&isTestEnv, "testenv", "t", scriptExecBool("${TESTENV:-false}"), "use test deployment "+testConf+"\033[0m")
	gHiddenFlags = append(gHiddenFlags, "testenv")
	genConfigCmd.Flags().BoolVarP(&isVpnServerEnable, "servevpn", "v", scriptExecBool("${SERVEVPN:-false}"), "enable vpn server\033[0m")
	gHiddenFlags = append(gHiddenFlags, "servevpn")
	genConfigCmd.Flags().BoolVarP(&isHide, "hide", "w", false, "dont print the config to the terminal OR show errors with -n flag\033[0m")
	gHiddenFlags = append(gHiddenFlags, "hide")
	genConfigCmd.Flags().BoolVarP(&isRetainHypervisors, "retainhv", "x", false, "retain existing hypervisors with regen\033[0m")
	gHiddenFlags = append(gHiddenFlags, "retainhv")
	genConfigCmd.Flags().BoolVarP(&disablePublicAutoConn, "autoconn", "y", scriptExecBool("${DISABLEPUBLICAUTOCONN:-false}"), "disable autoconnect to public visors\033[0m")
	gHiddenFlags = append(gHiddenFlags, "hide")
	genConfigCmd.Flags().BoolVarP(&isPublic, "public", "z", scriptExecBool("${VISORISPUBLIC:-false}"), "publicize visor in service discovery\033[0m")
	gHiddenFlags = append(gHiddenFlags, "public")
	genConfigCmd.Flags().StringVar(&ver, "version", scriptExecString("${VERSION}"), "custom version testing override\033[0m")
	gHiddenFlags = append(gHiddenFlags, "version")
	genConfigCmd.Flags().BoolVar(&isAll, "all", false, "show all flags")
	genConfigCmd.Flags().StringVar(&binPath, "binpath", scriptExecString("${BINPATH}"), "set bin_path\033[0m")
	gHiddenFlags = append(gHiddenFlags, "binpath")
	genConfigCmd.Flags().IntVar(&stcprPort, "stcpr", scriptExecInt("${STCPRPORT:-0}"), "set tcp transport listening port - 0 for random\033[0m")
	gHiddenFlags = append(gHiddenFlags, "stcpr")
	genConfigCmd.Flags().IntVar(&sudphPort, "sudph", scriptExecInt("${SUDPHPORT:-0}"), "set udp transport listening port - 0 for random\033[0m")
	gHiddenFlags = append(gHiddenFlags, "sudph")
	genConfigCmd.Flags().BoolVarP(&isEnvs, "envs", "q", false, "show the environmental variable settings")
	gHiddenFlags = append(gHiddenFlags, "envs")
	genConfigCmd.Flags().BoolVar(&noFetch, "nofetch", false, "do not fetch the services from the service conf url")
	gHiddenFlags = append(gHiddenFlags, "nofetch")
	genConfigCmd.Flags().BoolVar(&noDefaults, "nodefaults", false, "do not use hardcoded defaults for production / test services")
	gHiddenFlags = append(gHiddenFlags, "nodefaults")

	//show all flags on help
	if os.Getenv("UNHIDEFLAGS") != "1" {
		for _, j := range gHiddenFlags {
			genConfigCmd.Flags().MarkHidden(j) //nolint
		}
	}
}

// TODO: adapt for windows
func scriptExecString(s string) string {
	if visorconfig.OS == "windows" {
		cmd := fmt.Sprintf(`cmd /V /C "setlocal enabledelayedexpansion && set SKYENV=%s && if not [!SKYENV!]==[] if exist !SKYENV! (for /f "usebackq delims=" %%i in (`+"\"!SKYENV!\""+`) do set "SKYENV=%%i") && echo !%s!"`, skyenvfile, s)
		out, _ := script.Exec(cmd).String()
		if out == "" {
			defaultValue := s[strings.Index(s, ":-")+2 : strings.Index(s, "}")]
			return defaultValue
		}
		return strings.TrimSpace(out)
	}

	z, _ := script.Exec(fmt.Sprintf(`bash -c 'SKYENV=%s ; if [[ $SKYENV != "" ]] && [[ -f $SKYENV ]] ; then source $SKYENV ; fi ; printf "%s"'`, skyenvfile, s)).String()
	return strings.TrimSpace(z)
}

func scriptExecBool(s string) bool {
	if visorconfig.OS == "windows" {
		cmd := fmt.Sprintf(`cmd /V /C "setlocal enabledelayedexpansion && set SKYENV=%s && if not [!SKYENV!]==[] if exist !SKYENV! (for /f "usebackq delims=" %%i in (`+"\"!SKYENV!\""+`) do set "SKYENV=%%i") && echo !%s!"`, skyenvfile, s)
		out, _ := script.Exec(cmd).String()
		b, err := strconv.ParseBool(strings.TrimSpace(out))
		if err == nil {
			return b
		}
		return false
	}

	z, _ := script.Exec(fmt.Sprintf(`bash -c 'SKYENV=%s ; if [[ $SKYENV != "" ]] && [[ -f $SKYENV ]] ; then source $SKYENV ; fi ; printf "%s"'`, skyenvfile, s)).String()
	b, err := strconv.ParseBool(z)
	if err == nil {
		return b
	}
	return false
}

func scriptExecArray(s string) string {
	if visorconfig.OS == "windows" {
		cmd := fmt.Sprintf(`cmd /V /C "setlocal enabledelayedexpansion && set SKYENV=%s && if not [!SKYENV!]==[] if exist !SKYENV! (for /f "usebackq delims=" %%i in (`+"\"!SKYENV!\""+`) do set "SKYENV=%%i") && for %%i in (%s) do echo %%i"`, skyenvfile, s)
		out, _ := script.Exec(cmd).Slice()
		return strings.Join(out, ",")
	}

	y, _ := script.Exec(fmt.Sprintf(`bash -c 'SKYENV=%s ; if [[ $SKYENV != "" ]] && [[ -f $SKYENV ]] ; then source $SKYENV ; fi ; for _i in %s ; do echo "$_i" ; done'`, skyenvfile, s)).Slice()
	return strings.Join(y, ",")
}

func scriptExecInt(s string) int {
	if visorconfig.OS == "windows" {
		cmd := fmt.Sprintf(`cmd /V /C "setlocal enabledelayedexpansion && set SKYENV=%s && if not [!SKYENV!]==[] if exist !SKYENV! (for /f "usebackq delims=" %%i in (`+"\"!SKYENV!\""+`) do set "SKYENV=%%i") && echo !%s!"`, skyenvfile, s)
		out, _ := script.Exec(cmd).String()
		i, _ := strconv.Atoi(strings.TrimSpace(out))
		return i
	}

	z, _ := script.Exec(fmt.Sprintf(`bash -c 'SKYENV=%s ; if [[ $SKYENV != "" ]] && [[ -f $SKYENV ]] ; then source $SKYENV ; fi ; printf "%s"'`, skyenvfile, s)).String()
	i, _ := strconv.Atoi(z)
	return i
}

var genConfigCmd = &cobra.Command{
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
		if isEnvs {

			if visorconfig.OS != "linux" {
				pText = "this feature does not yet support this platform"
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

		// Initialize Viper
		v := viper.New()
		// Set the name of the configuration file (without the extension)
		v.SetConfigName(configName)
		// Set the path where the configuration file is located
		v.AddConfigPath(confPath)
		// Set the configuration file type
		v.SetConfigType("json")

		mLog := logging.NewMasterLogger()
		mLog.SetLevel(logrus.InfoLevel)
		log := mLog

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
				Timeout: time.Second * 30, // Timeout after 30 seconds
			}
			//create the http request
			req, err := http.NewRequest(http.MethodGet, fmt.Sprint(serviceConfURL), nil)
			if err != nil {
				mLog.WithError(err).Fatal("Failed to create http request\n")
			}
			req.Header.Add("Cache-Control", "no-cache")
			//check for errors in the response
			res, err := client.Do(req)
			if err != nil {
				//silence errors for stdout
				if !isStdout {
					mLog.WithError(err).Error("Failed to fetch servers\n")
					mLog.Warn("Falling back on hardcoded servers")
				}
			} else {
				// nil error from client.Do(req)
				if res.Body != nil {
					defer res.Body.Close() //nolint
				}
				body, err := io.ReadAll(res.Body)
				if err != nil {
					mLog.WithError(err).Fatal("Failed to read response\n")
				}
				//fill in services struct with the response
				err = json.Unmarshal(body, &services)
				if err != nil {
					mLog.WithError(err).Fatal("Failed to unmarshal json response\n")
				}
				if !isStdout {
					mLog.Infof("Fetched service endpoints from '%s'", serviceConfURL)
				}
			}
			// reset the state of isStdout
			isStdout = wasStdout
		}

		// Read in old config and obtain old secret key or generate a new random secret key
		// and obtain old hypervisors (if any)
		var oldConf visorconfig.V1
		if !isStdout || isRegen {
			err := v.ReadInConfig()
			if err != nil {
				log.Fatalf("Failed to read old file: %v", err)
			}
			// Unmarshal the configuration into your struct
			err = v.Unmarshal(&oldConf)
			if err != nil {
				_, sk = cipher.GenerateKeyPair()
			} else {
				sk = oldConf.SK
				if isRetainHypervisors {
					for _, j := range oldConf.Hypervisors {
						hypervisorPKs = hypervisorPKs + "," + fmt.Sprintf("\t%s\n", j)
					}
					for _, j := range oldConf.Dmsgpty.Whitelist {
						dmsgptywlPKs = dmsgptywlPKs + "," + fmt.Sprintf("\t%s\n", j)
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

		var dmsgHTTPServersList *visorconfig.DmsgHTTPServers
		dnsServer := utilenv.DNSServer
		if services != nil {
			if services.DNSServer != "" {
				dnsServer = services.DNSServer
			}
		}
		if isDmsgHTTP {
			dmsgHTTPPath := visorconfig.DMSGHTTPName
			if isPkg {
				dmsgHTTPPath = visorconfig.SkywirePath + "/" + visorconfig.DMSGHTTPName
			}
			// TODO
			//if usrEnv {
			//	dmsgHTTPPath = homepath + "/" + visorconfig.DMSGHTTPName
			//}
			// Initialize Viper
			d := viper.New()
			// Set the name of the configuration file (without the extension)
			d.SetConfigName(strings.TrimSuffix(dmsgHTTPPath, ".json"))
			// Set the path where the configuration file is located
			d.AddConfigPath(filepath.Clean(dmsgHTTPPath))
			// Set the configuration file type
			d.SetConfigType("json")

			err := d.ReadInConfig()
			if err != nil {
				log.Fatalf("Failed to read dmsghttp-config.json file: %v", err)
			}

			err = d.Unmarshal(&dmsgHTTPServersList)
			if err != nil {
				mLog.WithError(err).Fatal("Failed to unmarshal json response\n")
			}
		}

		//TODO: handle partial sercice conf / service conf for less than the whole set of services
		//fall back on  defaults
		var routeSetupPKs cipher.PubKeys
		var tpSetupPKs cipher.PubKeys
		var surveyWhitelistPKs cipher.PubKeys
		if services.SurveyWhitelist == nil {
			if surveywhitelistPks != "" {
				if err := surveyWhitelistPKs.Set(surveywhitelistPks); err != nil {
					log.Fatalf("bad key set for survey whitelist flag: %v", err)
				}
				services.SurveyWhitelist = surveyWhitelistPKs
				surveyWhitelistPKs = cipher.PubKeys{}
			}
			if !noDefaults {
				if err := surveyWhitelistPKs.Set(utilenv.SurveyWhitelistPKs); err != nil {
					log.Fatalf("Failed to unmarshal survey whitelist public keys: %v", err)
				}
				services.SurveyWhitelist = append(services.SurveyWhitelist, surveyWhitelistPKs...)
			}
		}
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
			if services.RouteSetupNodes == nil {
				if routesetupnodePks != "" {
					if err := routeSetupPKs.Set(routesetupnodePks); err != nil {
						log.Fatalf("bad key set for route setup node flag: %v", err)
					}
					services.RouteSetupNodes = routeSetupPKs
					routeSetupPKs = cipher.PubKeys{}
				}
				if !noDefaults {
					if err := routeSetupPKs.Set(utilenv.RouteSetupPKs); err != nil {
						log.Fatalf("Failed to unmarshal route setup-node public keys: %v", err)
					}
					services.RouteSetupNodes = append(services.RouteSetupNodes, routeSetupPKs...)
				}
			}
			if services.TransportSetupNodes == nil {
				if transportsetupnodePks != "" {
					if err := tpSetupPKs.Set(transportsetupnodePks); err != nil {
						log.Fatalf("bad key set for transport setup node flag: %v", err)
					}
					services.TransportSetupNodes = routeSetupPKs
					routeSetupPKs = cipher.PubKeys{}
				}
			}
			if !noDefaults {
				if err := tpSetupPKs.Set(utilenv.TPSetupPKs); err != nil {
					log.Fatalf("Failed to unmarshal transport setup-node public keys: %v", err)
				}
				services.TransportSetupNodes = append(services.TransportSetupNodes, tpSetupPKs...)
			}
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
			if services.RouteSetupNodes == nil {
				if routesetupnodePks != "" {
					if err := routeSetupPKs.Set(routesetupnodePks); err != nil {
						log.Fatalf("bad key set for route setup node flag: %v", err)
					}
					services.RouteSetupNodes = routeSetupPKs
					routeSetupPKs = cipher.PubKeys{}
				}
				if err := routeSetupPKs.Set(utilenv.TestRouteSetupPKs); err != nil {
					log.Fatalf("Failed to unmarshal route setup-node public keys: %v", err)
				}
				services.RouteSetupNodes = append(services.RouteSetupNodes, routeSetupPKs...)
			}
			if services.TransportSetupNodes == nil {
				if transportsetupnodePks != "" {
					if err := tpSetupPKs.Set(transportsetupnodePks); err != nil {
						log.Fatalf("bad key set for transport setup node flag: %v", err)
					}
				}
				if err := tpSetupPKs.Set(utilenv.TestTPSetupPKs); err != nil {
					log.Fatalf("Failed to unmarshal transport setup-node public keys: %v", err)
				}
				services.TransportSetupNodes = append(services.TransportSetupNodes, tpSetupPKs...)
			}
		}

		conf.Dmsg = &dmsgc.DmsgConfig{
			Discovery:     services.DmsgDiscovery,
			SessionsCount: 1,
			Servers:       []*disc.Entry{},
		}
		conf.Transport = &visorconfig.Transport{
			Discovery:           services.TransportDiscovery, //utilenv.TpDiscAddr,
			AddressResolver:     services.AddressResolver,    //utilenv.AddressResolverAddr,
			PublicAutoconnect:   visorconfig.PublicAutoconnect,
			TransportSetupNodes: services.TransportSetupNodes,
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
		if isHypervisor {
			config := visorconfig.GenerateWorkDirConfig(false)
			conf.Hypervisor = &config
		}

		// Manipulate dmsgpty whitelist PKs
		conf.Dmsgpty.Whitelist = make([]cipher.PubKey, 0)
		if dmsgptywlPKs != "" {
			keys := strings.Split(dmsgptywlPKs, ",")
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

		conf.SurveyWhitelist = services.SurveyWhitelist

		if isPkg {
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

		//edit the conf

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
		if isDisplayNodeIP {
			conf.Launcher.DisplayNodeIP = true
		}
		//don't write file with stdout
		if !isStdout {
			// Save config to file.
			// Marshal the struct to JSON and save to file
			err := v.WriteConfig()
			if err != nil {
				log.Fatalf("Failed to write the configuration file: %v", err)
			}
			//			if err := conf.Flush(); err != nil {
			//				logger.WithError(err).Fatal("Failed to flush config to file.")
			//			}
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

var envfileLinux = `
#
# /etc/skywire.conf
#
#########################################################################
#	SKYWIRE CONFIG TEMPLATE
#		Defaults for booleans are false
#		Uncomment to change default value
#########################################################################

#--	Other Visors will automatically establish transports to this visor
#	requires port forwarding or public ip
#VISORISPUBLIC=true

#--	Autostart vpn server for this visor
#VPNSERVER=true

#--	Use test deployment
#TESTENV=true

#--	Automatically determine the best protocol (dmsg or http)
#	based on location to connect to the deployment servers
#BESTPROTO=true

#--	Set custom service conf URLs
#SVCCONFADDR=('')

#--	Set visor runtime log level.
#	Default is info ; uncomment for debug logging
#LOGLVL=debug

#--	Use dmsghttp to connect to the production deployment
#DMSGHTTP=true

#--	Start the hypervisor interface for this visor
#ISHYPERVISOR=true

#--	Output path of the config file
#OUTPUT='./skywire-config.json'

#--	Display the node ip in the service discovery
#	for any public services this visor is running
#DISPLAYNODEIP=true

#--	Set remote hypervisor public keys
#HYPERVISORPKS=('')

#--	Default config paths for the installer or package (system paths)
#PKGENV=true

#--	Default config paths for the current userspace
#USRENV=true

#--	Set secret key
#SK=''

#--	Disable auto-transports to public visors
#DISABLEPUBLICAUTOCONN=true

#--	Custom config version override
#VERSION=''

#--	Set app bin_path
#BINPATH='./apps'

`
