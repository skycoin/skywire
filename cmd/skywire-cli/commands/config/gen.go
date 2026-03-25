// Package cliconfig cmd/skywire-cli/commands/config/gen.go
package cliconfig

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/bitfield/script"
	"github.com/skycoin/dmsg/pkg/disc"
	"github.com/skycoin/dmsg/pkg/dmsgpty"
	coinCipher "github.com/skycoin/skycoin/src/cipher"
	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/dmsgc"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/netutil"
	"github.com/skycoin/skywire/pkg/transport/network"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// Default timeouts
const (
	servicesFetchTimeout = 15 * time.Second
)

// Default file permissions
const (
	configFilePerms = 0600
)

// RootCmd contains commands that interact with the config of local skywire-visor
var checkPKCmd = &cobra.Command{
	Use:   "check-pk <public-key>",
	Short: "check a skywire public key",
	Args:  cobra.ExactArgs(1), // Require exactly one argument
	Run: func(_ *cobra.Command, args []string) {
		if len(args) == 0 {
			return
		}
		var checkKey cipher.PubKey
		err := checkKey.Set(args[0])
		if err != nil {
			logger.WithError(err).Fatal("invalid public key ")
		}
		logger.Info("Valid public key: ", checkKey.String())
	},
}

// RootCmd contains commands that interact with the config of local skywire-visor
var genKeysCmd = &cobra.Command{
	Use:   "gen-keys",
	Short: "generate public / secret keypair",
	Run: func(_ *cobra.Command, _ []string) {
		pk, sk := cipher.GenerateKeyPair()
		fmt.Println(pk)
		fmt.Println(sk)
	},
}

var (
	isEnvs     bool
	skyenvfile = os.Getenv("SKYENV")
)
var envfile string

func init() {
	var msg string
	//disable sorting, flags appear in the order shown here
	genConfigCmd.Flags().SortFlags = false
	RootCmd.AddCommand(genConfigCmd, genKeysCmd, checkPKCmd)

	// Output flags
	genConfigCmd.Flags().BoolVarP(&isStdout, "stdout", "n", false, "write config to stdout")
	gHiddenFlags = append(gHiddenFlags, "stdout")
	genConfigCmd.Flags().BoolVarP(&isSquash, "squash", "N", false, "output config without whitespace or newlines")
	gHiddenFlags = append(gHiddenFlags, "squash")
	msg = "output config"
	if scriptExecString("${OUTPUT}") == "" {
		msg += ": " + skyenv.ConfigName
	}
	genConfigCmd.Flags().StringVarP(&output, "out", "o", scriptExecString("${OUTPUT}"), msg+"")
	genConfigCmd.Flags().BoolVarP(&isHide, "hide", "w", false, "dont print the config to the terminal :: show errors with -n flag")
	gHiddenFlags = append(gHiddenFlags, "hide")
	genConfigCmd.Flags().BoolVarP(&isEnvs, "envs", "q", false, "show the environmental variable settings")

	// Config generation flags
	genConfigCmd.Flags().BoolVarP(&isForce, "force", "f", false, "remove pre-existing config")
	gHiddenFlags = append(gHiddenFlags, "force")
	genConfigCmd.Flags().BoolVarP(&isRegen, "regen", "r", false, "re-generate existing config & retain keys")
	genConfigCmd.Flags().BoolVarP(&isRetainHypervisors, "retainhv", "x", false, "retain existing hypervisors with regen")
	gHiddenFlags = append(gHiddenFlags, "retainhv")

	// Network and deployment flags
	genConfigCmd.Flags().StringVarP(&serviceConfURL, "url", "a", scriptExecArray(fmt.Sprintf("${SVCCONFADDR[@]-%s}", serviceConfURL)), "services conf url\n\r")
	gHiddenFlags = append(gHiddenFlags, "url")
	genConfigCmd.Flags().BoolVarP(&isTestEnv, "testenv", "t", scriptExecBool("${TESTENV:-false}"), "use test deployment")
	gHiddenFlags = append(gHiddenFlags, "testenv")
	genConfigCmd.Flags().BoolVarP(&isDmsgHTTP, "dmsghttp", "d", scriptExecBool("${DMSGHTTP:-false}"), "use only dmsg connection to skywire services (no http fallback)")
	genConfigCmd.Flags().BoolVar(&isHTTPOnly, "http", false, "use only http connection to skywire services (no dmsg)")
	genConfigCmd.MarkFlagsMutuallyExclusive("dmsghttp", "http")
	gHiddenFlags = append(gHiddenFlags, "dmsghttp")
	genConfigCmd.Flags().StringVarP(&dmsgHTTPPath, "dmsgconf", "D", scriptExecString("${DMSGCONF}"), "dmsghttp-config path")
	gHiddenFlags = append(gHiddenFlags, "dmsgconf")
	genConfigCmd.Flags().BoolVarP(&isBestProtocol, "bestproto", "b", scriptExecBool("${BESTPROTO:-false}"), "best protocol (dmsg | direct) based on location") //this will also disable public autoconnect based on location
	genConfigCmd.Flags().BoolVar(&noFetch, "nofetch", false, "do not fetch the services from the service conf url")
	gHiddenFlags = append(gHiddenFlags, "nofetch")
	//TODO: visorconfig.SvcConfName
	genConfigCmd.Flags().StringVarP(&configServicePath, "svcconf", "S", scriptExecString("${SVCCONF}"), "fallback service configuration file")
	gHiddenFlags = append(gHiddenFlags, "svcconf")
	genConfigCmd.Flags().BoolVar(&noDefaults, "nodefaults", false, "do not use hardcoded defaults for services")
	gHiddenFlags = append(gHiddenFlags, "nodefaults")

	// DMSG flags
	genConfigCmd.Flags().IntVar(&minDmsgSess, "minsess", scriptExecInt("${MINDMSGSESS:-2}"), "number of dmsg servers to connect to (0 = unlimited)")
	gHiddenFlags = append(gHiddenFlags, "minsess")

	// Transport flags
	genConfigCmd.Flags().BoolVarP(&disablePublicAutoConn, "autoconn", "y", scriptExecBool("${DISABLEPUBLICAUTOCONN:-false}"), "disable autoconnect to public visors")
	gHiddenFlags = append(gHiddenFlags, "hide")
	genConfigCmd.Flags().BoolVarP(&isPublic, "public", "z", scriptExecBool("${VISORISPUBLIC:-false}"), "publicize visor in service discovery")
	gHiddenFlags = append(gHiddenFlags, "public")
	genConfigCmd.Flags().IntVar(&stcprPort, "stcpr", scriptExecInt("${STCPRPORT:-0}"), "set tcp transport listening port - 0 for random")
	gHiddenFlags = append(gHiddenFlags, "stcpr")
	genConfigCmd.Flags().IntVar(&sudphPort, "sudph", scriptExecInt("${SUDPHPORT:-0}"), "set udp transport listening port - 0 for random")
	gHiddenFlags = append(gHiddenFlags, "sudph")
	genConfigCmd.Flags().BoolVar(&enableSyncTPDData, "sync-tpd-data", scriptExecBool("${SYNCTPDDATA:-false}"), "enable transport discovery data sync (bandwidth/latency)")
	gHiddenFlags = append(gHiddenFlags, "sync-tpd-data")

	// Routing flags
	msg = "add route setup node PKs"
	if scriptExecArray("${ROUTESETUPPKS[@]}") != "" {
		msg += "\n\r"
	}
	genConfigCmd.Flags().StringVar(&routeSetupNodes, "routesetup", scriptExecArray("${ROUTESETUPPKS[@]}"), msg)
	gHiddenFlags = append(gHiddenFlags, "routesetup")
	msg = "add transport setup node PKs"
	if scriptExecArray("${TPSETUPPKS[@]}") != "" {
		msg += "\n\r"
	}
	genConfigCmd.Flags().StringVar(&transportSetupPKs, "tpsetup", scriptExecArray("${TPSETUPPKS[@]}"), msg)
	gHiddenFlags = append(gHiddenFlags, "tpsetup")
	genConfigCmd.Flags().BoolVar(&snConfig, "sn", false, "generate config for route setup node")
	gHiddenFlags = append(gHiddenFlags, "sn")
	genConfigCmd.Flags().BoolVar(&enableCalculateRoutes, "calculate-routes", scriptExecBool("${CALCULATEROUTES:-false}"), "enable local route calculation")
	gHiddenFlags = append(gHiddenFlags, "calculate-routes")

	// Hypervisor and security flags
	genConfigCmd.Flags().BoolVarP(&isHypervisor, "ishv", "i", scriptExecBool("${ISHYPERVISOR:-false}"), "local hypervisor configuration")
	msg = "list of public keys to add as hypervisor"
	if scriptExecArray("${HYPERVISORPKS[@]}") != "" {
		msg += "\n\r"
	}
	genConfigCmd.Flags().StringVarP(&hypervisorPKs, "hvpks", "j", scriptExecArray("${HYPERVISORPKS[@]}"), msg)
	genConfigCmd.Flags().BoolVarP(&isDisableAuth, "noauth", "c", false, "disable authentication for hypervisor UI")
	gHiddenFlags = append(gHiddenFlags, "noauth")
	genConfigCmd.Flags().BoolVarP(&isEnableAuth, "auth", "e", false, "enable auth on hypervisor UI")
	gHiddenFlags = append(gHiddenFlags, "auth")

	// Dmsgpty and survey whitelist flags
	msg = "add dmsgpty whitelist PKs"
	if scriptExecArray("${DMSGPTYPKS[@]}") != "" {
		msg += "\n\r"
	}
	genConfigCmd.Flags().StringVar(&dmsgptyWlPKs, "dmsgpty", scriptExecArray("${DMSGPTYPKS[@]}"), msg)
	msg = "add survey whitelist PKs"
	if scriptExecArray("${SURVEYPKS[@]}") != "" {
		msg += "\n\r"
	}

	genConfigCmd.Flags().StringVar(&surveyWhitelistPKs, "survey", scriptExecArray("${SURVEYPKS[@]}"), msg)
	gHiddenFlags = append(gHiddenFlags, "survey")

	// App flags
	genConfigCmd.Flags().BoolVarP(&isDisplayNodeIP, "publicip", "l", scriptExecBool("${DISPLAYNODEIP:-false}"), "display visor ip in service discovery")
	gHiddenFlags = append(gHiddenFlags, "publicip")
	genConfigCmd.Flags().BoolVarP(&addExampleApps, "example-apps", "m", false, "add example apps to the config")
	gHiddenFlags = append(gHiddenFlags, "example-apps")
	genConfigCmd.Flags().BoolVar(&externalApps, "external-apps", false, "configure launcher apps as external processes")
	gHiddenFlags = append(gHiddenFlags, "external-apps")
	genConfigCmd.Flags().StringVarP(&disableApps, "disableapps", "g", "", "comma separated list of apps to disable")
	gHiddenFlags = append(gHiddenFlags, "disableapps")
	genConfigCmd.Flags().StringVar(&binPath, "binpath", scriptExecString("${BINPATH}"), "set bin_path for visor native apps")
	gHiddenFlags = append(gHiddenFlags, "binpath")
	genConfigCmd.Flags().BoolVarP(&isVpnServerEnable, "servevpn", "v", scriptExecBool("${VPNSERVER:-true}"), "autostart vpn server (default: true)")
	gHiddenFlags = append(gHiddenFlags, "servevpn")

	// VPN flags
	// TODO: VPN client killswitch should be handled as boolean, not string
	genConfigCmd.Flags().StringVar(&setVPNClientKillswitch, "killsw", scriptExecString("${VPNKS}"), "vpn client killswitch")
	gHiddenFlags = append(gHiddenFlags, "killsw")
	genConfigCmd.Flags().StringVar(&addVPNClientSrv, "addvpn", scriptExecString("${ADDVPNPK}"), "set vpn server public key for vpn client")
	gHiddenFlags = append(gHiddenFlags, "addvpn")
	genConfigCmd.Flags().StringVar(&addVPNServerWhitelist, "vpnwl", scriptExecString("${VPNSERVERWL}"), "comma-separated list of public keys allowed to connect to vpn server (empty = allow all)")
	gHiddenFlags = append(gHiddenFlags, "vpnwl")
	genConfigCmd.Flags().StringVar(&setVPNServerSecure, "secure", scriptExecString("${VPNSEVERSECURE}"), "change secure mode status of vpn server")
	gHiddenFlags = append(gHiddenFlags, "secure")
	genConfigCmd.Flags().StringVar(&setVPNServerNetIfc, "netifc", scriptExecString("${VPNSEVERNETIFC}"), "VPN Server network interface (detected: "+getInterfaceNames()+")")
	gHiddenFlags = append(gHiddenFlags, "netifc")

	// Proxy flags
	genConfigCmd.Flags().StringVar(&addSkysocksClientSrv, "proxyclientpk", scriptExecString("${PROXYCLIENTPK}"), "set server public key for proxy client")
	gHiddenFlags = append(gHiddenFlags, "proxyclientpk")
	genConfigCmd.Flags().BoolVar(&enableProxyClientAutostart, "startproxyclient", scriptExecBool("${STARTPROXYCLIENT:-false}"), "autostart proxy client")
	gHiddenFlags = append(gHiddenFlags, "startproxyclient")
	genConfigCmd.Flags().BoolVar(&disableProxyServerAutostart, "noproxyserver", scriptExecBool("${NOPROXYSERVER:-false}"), "disable autostart of proxy server")
	gHiddenFlags = append(gHiddenFlags, "noproxyserver")
	genConfigCmd.Flags().StringVar(&proxyServerWhitelist, "proxywl", scriptExecString("${PROXYSERVERWL}"), "comma-separated list of public keys allowed to connect to proxy server (empty = allow all)")
	gHiddenFlags = append(gHiddenFlags, "proxywl")

	// Path and environment flags
	genConfigCmd.Flags().StringVarP(&selectedOS, "os", "k", skyenv.OS, "(linux / mac / win) paths")
	gHiddenFlags = append(gHiddenFlags, "os")
	if skyenv.OS == "win" {
		pText = "use .msi installation path: "
	}
	if skyenv.OS == "linux" {
		pText = "use path for package: "
	}
	if skyenv.OS == "mac" {
		pText = "use mac installation path: "
	}
	genConfigCmd.Flags().BoolVarP(&isPkgEnv, "pkg", "p", scriptExecBool("${PKGENV:-false}"), pText+skyenv.SkywirePath+"")
	homepath := visorconfig.HomePath()
	if homepath != "" {

		genConfigCmd.Flags().BoolVarP(&isUsrEnv, "user", "u", scriptExecBool("${USRENV:-false}"), "use paths for user space: "+homepath+"")
	}
	genConfigCmd.Flags().StringVar(&logLevel, "loglvl", scriptExecString("${LOGLVL:-info}"), "level of logging in config")
	gHiddenFlags = append(gHiddenFlags, "loglvl")

	// Secret key flag
	if scriptExecString("${SK:-0000000000000000000000000000000000000000000000000000000000000000}") != "0000000000000000000000000000000000000000000000000000000000000000" {
		//nolint:errcheck,gosec
		sk.Set(scriptExecString("${SK:-0000000000000000000000000000000000000000000000000000000000000000}"))
	}
	genConfigCmd.Flags().VarP(&sk, "sk", "s", "a random key is generated if unspecified\n\r")
	gHiddenFlags = append(gHiddenFlags, "sk")

	// Version and misc flags
	genConfigCmd.Flags().StringVar(&ver, "version", scriptExecString("${VERSION}"), "custom version testing override")
	gHiddenFlags = append(gHiddenFlags, "version")
	genConfigCmd.Flags().BoolVar(&isAll, "all", false, "show all flags")

	//show all flags on help
	if os.Getenv("UNHIDEFLAGS") != "1" {
		for _, j := range gHiddenFlags {
			genConfigCmd.Flags().MarkHidden(j) //nolint:errcheck,gosec
		}
	}
}

var genConfigCmd = &cobra.Command{
	Use:   "gen",
	Short: "Generate a config file",
	Long: func() string {
		if skyenv.OS == "linux" {
			if skyenvfile == "" {
				return `Generate a config file

	Config defaults file may also be specified with:
	SKYENV=/path/to/skywire.conf skywire-cli config gen
	print the SKYENV file template with:
	skywire-cli config gen -q`
			}
			if _, err := os.Stat(skyenvfile); err == nil {
				return `Generate a config file

	skyenv file detected: ` + skyenvfile
			}
			return `Generate a config file

	Config defaults file may also be specified with
	SKYENV=/path/to/skywire.conf skywire-cli config gen
	print the SKYENV file template with:
	skywire-cli config gen -q`
		}
		return `Generate a config file`

	}(),
	PreRun: func(cmd *cobra.Command, _ []string) {
		log := logger
		if isEnvs {
			if skyenv.OS == "windows" {
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
				f := cmd.Flags().Lookup(j)
				f.Hidden = false
			}
			cmd.Flags().MarkHidden("all") //nolint:errcheck,gosec
			internal.Catch(cmd.Flags(), cmd.Help())
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
		//use test deployment
		if isTestEnv {
			serviceConfURL = testServiceConfURL
		}
		var err error
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
				configName = skyenv.ConfigJSON
				confPath = visorconfig.SkywireConfig()
				output = confPath
			}
			if isUsrEnv {
				confPath = visorconfig.HomePath() + "/" + skyenv.ConfigName
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
			if skyenv.OS == "linux" {
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
		if isPkgEnv && configServicePath == skyenv.SERVICESName {
			configServicePath = skyenv.SkywirePath + "/" + skyenv.SERVICESName
		}
	},
	Run: func(_ *cobra.Command, _ []string) {

		log := logger
		wasStdout := isStdout
		var err error
		// enable errors from service conf fetch from the combination of these flags
		if isStdout && isHide {
			isStdout = false
		}

		//determine best protocol
		if isBestProtocol && netutil.LocalProtocol() {
			disablePublicAutoConn = true
			isDmsgHTTP = true
		}

		fetchServiceConfig(log)

		// reset the state of isStdout
		isStdout = wasStdout

		readExistingConfig(log)

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

		if services.DNSServer != "" {
			dnsServer = services.DNSServer
		}

		configureDMSGHTTP(log, err)

		configureServices(log)

		configureDMSG()

		configureTransports()

		configureRouting()

		configureLauncher(log)

		configureHypervisor(log)

		configureApps(log)

		applyOverrides()

		writeConfigOutput(log)
	},
}

// logIfNotStdout logs an error with a message only when output is not stdout.
func logIfNotStdout(log *logging.Logger, err error, msg string) {
	if !isStdout {
		log.WithError(err).Error(msg)
	}
}

// fetchServiceConfig fetches service endpoints from the configured URL
// or falls back to a local file or hardcoded defaults.
func fetchServiceConfig(log *logging.Logger) {
	var err error
	if !noFetch && !isDmsgHTTP {
		client := http.Client{Timeout: servicesFetchTimeout}
		if serviceConfURL == "" {
			serviceConfURL = "http://"
		}
		if !isStdout {
			log.Infof("Fetching service endpoints from %s", serviceConfURL)
		}
		res, err := client.Get(serviceConfURL)
		if err != nil {
			logIfNotStdout(log, err, "Failed to fetch servers")
			if !isStdout {
				log.Warn("Falling back on services-config.json")
			}
			loadServicesFromFile(log)
			return
		}
		defer res.Body.Close() //nolint:errcheck,gosec
		body, err := io.ReadAll(res.Body)
		if err != nil {
			log.WithError(err).Error("Failed to read HTTP response")
			return
		}
		if err := json.Unmarshal(body, &services); err != nil {
			logIfNotStdout(log, err, "Failed to unmarshal JSON response to services struct")
			if !isStdout {
				log.Warn("Falling back on hardcoded servers")
			}
			return
		} else if !isStdout {
			log.Infof("Fetched service endpoints from '%s'", serviceConfURL)
		}
	} else {
		body := deployment.ServicesJSON
		if configServicePath != "" {
			body, err = os.ReadFile(configServicePath)
			if err != nil {
				logIfNotStdout(log, err, "Failed to read config service from file")
				if !isStdout {
					log.Warn("Falling back on hardcoded servers")
				}
				return
			}
		}
		if err := json.Unmarshal(body, &servicesConfig); err != nil {
			logIfNotStdout(log, err, "Failed to unmarshal services-config.json file")
			if !isStdout {
				log.Warn("Falling back on hardcoded servers")
			}
			return
		}
		services = servicesConfig.Prod
		if isTestEnv {
			services = servicesConfig.Test
		}
	}
}

// loadServicesFromFile reads service config from a file or embedded defaults.
func loadServicesFromFile(log *logging.Logger) {
	body := deployment.ServicesJSON
	if configServicePath != "" {
		var err error
		body, err = os.ReadFile(configServicePath)
		if err != nil {
			logIfNotStdout(log, err, "Failed to read config service from file")
			if !isStdout {
				log.Warn("Falling back on hardcoded servers")
			}
			return
		}
	}
	if err := json.Unmarshal(body, &servicesConfig); err != nil {
		logIfNotStdout(log, err, "Failed to unmarshal services-config.json file")
		if !isStdout {
			log.Warn("Falling back on hardcoded servers")
		}
		return
	}
	services = servicesConfig.Prod
	if isTestEnv {
		services = servicesConfig.Test
	}
}

// readExistingConfig reads an old config file when regenerating, preserving
// the secret key and optionally hypervisor/dmsgpty whitelist keys.
func readExistingConfig(log *logging.Logger) {
	var oldConf visorconfig.V1
	if isRegen {
		// Read the JSON configuration file
		oldConfJSON, err := os.ReadFile(confPath)
		if err != nil {
			if !isStdout || isStdout && isHide {
				log.Errorf("Failed to read config file: %v", err)
			}
		} else {
			// Decode JSON data
			err = json.Unmarshal(oldConfJSON, &oldConf)
			if err != nil {
				if !isStdout || isStdout && isHide {
					log.WithError(err).Fatal("Failed to unmarshal old config json")
				}
				_, sk = cipher.GenerateKeyPair()
			} else {
				sk = oldConf.SK
				if isRetainHypervisors {
					for _, j := range oldConf.Hypervisors {
						hypervisorPKs = hypervisorPKs + "," + fmt.Sprintf("\t%s\n", j)
					}
					for _, j := range oldConf.Dmsgpty.Whitelist {
						dmsgptyWlPKs = dmsgptyWlPKs + "," + fmt.Sprintf("\t%s\n", j)
					}
				}
			}
		}
	}
	// Store oldConf for later use in configureRouting and configureDMSG
	oldConfCache = &oldConf
}

// oldConfCache holds the previously-read config for use across configure* functions.
var oldConfCache *visorconfig.V1

// configureDMSGHTTP loads dmsghttp server list when dmsg URLs are needed.
// This runs for both --dmsghttp (DMSG-only) and default (dual) modes.
func configureDMSGHTTP(log *logging.Logger, outerErr error) {
	_ = outerErr
	if isDmsgHTTP || !isHTTPOnly {
		// TODO
		//if isUsrEnv {
		//	dmsgHTTPPath = homepath + "/" + skyenv.DMSGHTTPName
		//}
		dmsghttpConfigData := deployment.DmsghttpJSON
		if dmsgHTTPPath != "" {
			var err error
			// Read the JSON configuration file
			dmsghttpConfigData, err = os.ReadFile(dmsgHTTPPath)
			if err != nil {
				log.Fatalf("Failed to read config file: %v", err)
			}
		}

		// Decode JSON data
		err := json.Unmarshal(dmsghttpConfigData, &dmsgHTTPServersList)
		if err != nil {
			log.WithError(err).Fatal("Failed to unmarshal " + skyenv.DMSGHTTPName)
		}
	}
}

// configureServices validates and merges service endpoints from flags and
// fetched data (survey whitelist, route setup nodes, transport setup PKs).
func configureServices(log *logging.Logger) {
	//fall back on  defaults
	var routeSetupPKs cipher.PubKeys
	var tpSetupPKs cipher.PubKeys
	var surveyWlPKs cipher.PubKeys
	// If nothing was fetched
	if services.SurveyWhitelist == nil {
		// By default
		log.Error("Services were not fetched from default conf service URL")

	}
	//if the flag is not empty
	if surveyWhitelistPKs != "" {
		// validate public keys set via flag / fail explicitly on errors
		if err := surveyWlPKs.Set(surveyWhitelistPKs); err != nil {
			log.Fatalf("bad key set for survey whitelist flag: %v", err)
		}
	}
	services.SurveyWhitelist = append(services.SurveyWhitelist, surveyWlPKs...)

	if services.DmsgDiscovery == "" {
		log.Fatalf("Dmsg Discovery not set")
	}
	if services.TransportDiscovery == "" {
		log.Fatalf("Transport Discovery not set")
	}
	if routeSetupNodes != "" {
		if err := routeSetupPKs.Set(routeSetupNodes); err != nil {
			log.Fatalf("bad key set for route setup node flag: %v", err)
		}
	}
	services.RouteSetupNodes = append(services.RouteSetupNodes, routeSetupPKs...)
	if services.RouteSetupNodes == nil {
		log.Fatalf("Route Setup node not set")
	}
	if transportSetupPKs != "" {
		if err := tpSetupPKs.Set(transportSetupPKs); err != nil {
			log.Fatalf("bad key set for transport setup node flag: %v", err)
		}
	}
	services.TransportSetupPKs = append(services.TransportSetupPKs, tpSetupPKs...)
	if services.TransportSetupPKs == nil {
		log.Fatalf("Route Setup node not set")
	}
}

// configureDMSG sets up the DMSG configuration on conf, preserving protocol
// from a previous config if regenerating.
func configureDMSG() {
	conf.Dmsg = &dmsgc.DmsgConfig{
		Discovery:            services.DmsgDiscovery,
		SessionsCount:        minDmsgSess,
		Servers:              []*disc.Entry{},
		ConnectedServersType: "all",
		Protocol:             "yamux",
	}
	if oldConfCache != nil && oldConfCache.Dmsg != nil {
		if oldConfCache.Dmsg.Protocol != "" {
			conf.Dmsg.Protocol = oldConfCache.Dmsg.Protocol
		}
	}
}

// configureTransports sets up transport configuration on conf.
func configureTransports() {
	conf.Transport = &visorconfig.Transport{
		Discovery:         services.TransportDiscovery, //utilenv.TpDiscAddr,
		AddressResolver:   services.AddressResolver,    //utilenv.AddressResolverAddr,
		PublicAutoconnect: skyenv.PublicAutoconnect,
		TransportSetupPKs: services.TransportSetupPKs,
		LogStore: &visorconfig.LogStore{
			Type:             visorconfig.FileLogStore,
			Location:         skyenv.LocalPath + "/" + skyenv.TpLogStore,
			RotationInterval: visorconfig.DefaultLogRotationInterval,
		},
		SudphPort:   sudphPort,
		StcprPort:   stcprPort,
		SyncTPDData: enableSyncTPDData,
	}
}

// configureRouting sets up routing configuration on conf, preserving MinHops
// from a previous config if regenerating.
func configureRouting() {
	conf.Routing = &visorconfig.Routing{
		RouteFinder:        services.RouteFinder,     //utilenv.RouteFinderAddr,
		RouteSetupNodes:    services.RouteSetupNodes, //[]cipher.PubKey{utilenv.MustPK(utilenv.SetupPK)},
		RouteFinderTimeout: visorconfig.DefaultTimeout,
		MinHops:            1,
		CalculateRoutes:    enableCalculateRoutes,
	}

	if oldConfCache != nil && oldConfCache.Routing != nil {
		if oldConfCache.Routing.MinHops != 0 {
			conf.Routing.MinHops = oldConfCache.Routing.MinHops
		}
	}
}

// configureLauncher sets up the app launcher, dmsgpty, STCP, UI server,
// log server, and other base config fields. It also applies dmsghttp
// overrides if enabled.
func configureLauncher(log *logging.Logger) {
	_ = log
	conf.Launcher = &visorconfig.Launcher{
		ServiceDisc:   services.ServiceDiscovery, //utilenv.ServiceDiscAddr,
		Apps:          nil,
		ServerAddr:    skyenv.AppSrvAddr,
		BinPath:       skyenv.AppBinPath,
		DisplayNodeIP: isDisplayNodeIP,
	}
	conf.UptimeTracker = &visorconfig.UptimeTracker{
		Addr: services.UptimeTracker, //utilenv.UptimeTrackerAddr,
	}
	conf.CLIAddr = skyenv.RPCAddr
	conf.LogLevel = logLevel
	conf.LocalPath = skyenv.LocalPath
	conf.DmsgHTTPServerPath = skyenv.LocalPath + "/" + skyenv.Custom
	conf.StunServers = services.StunServers //utilenv.GetStunServers()
	conf.ShutdownTimeout = visorconfig.DefaultTimeout
	conf.GeoIP = skyenv.GeoIP

	conf.Dmsgpty = &visorconfig.Dmsgpty{
		DmsgPort: skyenv.DmsgPtyPort,
		CLINet:   skyenv.DmsgPtyCLINet,
		CLIAddr:  dmsgpty.DefaultCLIAddr(),
	}

	conf.STCP = &network.STCPConfig{
		ListeningAddress: skyenv.STCPAddr,
		PKTable:          nil,
	}

	// UI Server configuration (disabled by default)
	conf.UIServer = &visorconfig.UIServer{
		Enable:    false,
		LocalAddr: "localhost:8081",
		DmsgPort:  81,
	}

	// Log Server configuration (localhost serving disabled by default)
	// Set local_addr to e.g. "localhost:8002" to enable localhost serving without auth
	conf.LogServer = &visorconfig.LogServer{
		LocalAddr: "",
	}

	// Configure service URLs based on connection mode:
	// --dmsghttp: DMSG-only (overwrite HTTP URLs with DMSG URLs)
	// --http: HTTP-only (no DMSG fields, default HTTP URLs kept)
	// neither: dual mode (HTTP URLs + DMSG URLs in _dmsg fields)
	if !isHTTPOnly && dmsgHTTPServersList != nil {
		var dmsgConf *visorconfig.DmsgHTTPServersData
		if isTestEnv {
			dmsgConf = &dmsgHTTPServersList.Test
		} else {
			dmsgConf = &dmsgHTTPServersList.Prod
		}

		if dmsgConf != nil {
			conf.Dmsg.Servers = dmsgConf.DMSGServers
			conf.Dmsg.Discovery = dmsgConf.DMSGDiscovery

			if isDmsgHTTP {
				// DMSG-only: overwrite HTTP URLs
				conf.Transport.AddressResolver = dmsgConf.AddressResolver
				conf.Transport.Discovery = dmsgConf.TransportDiscovery
				conf.UptimeTracker.Addr = dmsgConf.UptimeTracker
				conf.Routing.RouteFinder = dmsgConf.RouteFinder
				conf.Launcher.ServiceDisc = dmsgConf.ServiceDiscovery
			} else {
				// Dual mode: keep HTTP URLs, add DMSG URLs as fallback
				conf.Transport.AddressResolverDmsg = dmsgConf.AddressResolver
				conf.Transport.DiscoveryDmsg = dmsgConf.TransportDiscovery
				conf.Launcher.ServiceDiscDmsg = dmsgConf.ServiceDiscovery
			}
		}
	}

	// Configure public visor
	conf.IsPublic = isPublic
	if isPublic {
		conf.PublicVisorConfig = &visorconfig.PublicVisorConfig{
			RegistrationTimeout: visorconfig.Duration(skyenv.PublicVisorRegistrationTimeout),
			MaxTransports:       visorconfig.PublicVisorMaxTransports,
		}
	}
}

// configureHypervisor sets up hypervisor PKs, dmsgpty whitelist, survey
// whitelist, and package/user-specific config paths.
func configureHypervisor(log *logging.Logger) {
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
	// Local hypervisor setting
	if isHypervisor {
		config := visorconfig.GenerateWorkDirConfig(false)
		conf.Hypervisor = &config
	}

	// Manipulate dmsgpty whitelist PKs
	conf.Dmsgpty.Whitelist = make([]cipher.PubKey, 0)
	if dmsgptyWlPKs != "" {
		keys := strings.Split(dmsgptyWlPKs, ",")
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
	// set survey collection whitelist - will include by default hypervisors & dmsgpty whitelisted keys
	conf.SurveyWhitelist = services.SurveyWhitelist
	// set package-specific config paths
	if isPkgEnv {
		pkgConfig := visorconfig.PackageConfig()
		conf.LocalPath = pkgConfig.LocalPath
		conf.DmsgHTTPServerPath = pkgConfig.LocalPath + "/" + skyenv.Custom
		conf.Launcher.BinPath = pkgConfig.LauncherBinPath
		conf.Transport.LogStore.Location = pkgConfig.LocalPath + "/" + skyenv.TpLogStore
		if conf.Hypervisor != nil {
			conf.Hypervisor.EnableAuth = pkgConfig.Hypervisor.EnableAuth
			conf.Hypervisor.DBPath = pkgConfig.Hypervisor.DbPath
		}
	}
	// set config paths for the user space
	if isUsr {
		usrConfig := visorconfig.UserConfig()
		conf.LocalPath = usrConfig.LocalPath
		conf.DmsgHTTPServerPath = usrConfig.LocalPath + "/" + skyenv.Custom
		conf.Launcher.BinPath = usrConfig.LauncherBinPath
		conf.Transport.LogStore.Location = usrConfig.LocalPath + "/" + skyenv.TpLogStore
		if conf.Hypervisor != nil {
			conf.Hypervisor.EnableAuth = usrConfig.Hypervisor.EnableAuth
			conf.Hypervisor.DBPath = usrConfig.Hypervisor.DbPath
		}
	}
}

// configureApps sets up launcher app configurations (internal or external),
// handles app disable/enable flags, and configures VPN/proxy app settings.
func configureApps(log *logging.Logger) {
	// App config settings
	if externalApps {
		// External apps configuration (apps run as separate processes)
		conf.Launcher.Apps = []appserver.AppConfig{
			{
				Name:      skyenv.VPNClientName,
				Binary:    "skywire",
				AutoStart: false,
				Port:      routing.Port(skyenv.VPNClientPort),
				Args:      append([]string{"app", "vpn-client"}, "--dns", dnsServer),
			},
			{
				Name:      skyenv.SkychatName,
				Binary:    "skywire",
				AutoStart: true,
				Port:      routing.Port(skyenv.SkychatPort),
				Args:      append([]string{"app", "skychat"}, "--addr", skyenv.SkychatAddr),
			},
			{
				Name:      skyenv.SkysocksName,
				Binary:    "skywire",
				AutoStart: true,
				Port:      routing.Port(skyenv.SkysocksPort),
				Args:      []string{"app", "skysocks"},
			},
			{
				Name:      skyenv.SkysocksClientName,
				Binary:    "skywire",
				AutoStart: false,
				Port:      routing.Port(skyenv.SkysocksClientPort),
				Args:      append([]string{"app", "skysocks-client"}, "--addr", skyenv.SkysocksClientAddr),
			},
			{
				Name:      skyenv.VPNServerName,
				Binary:    "skywire",
				AutoStart: isVpnServerEnable,
				Port:      routing.Port(skyenv.VPNServerPort),
				Args:      []string{"app", "vpn-server"},
			},
		}
	} else {
		// Internal apps configuration (default - apps run within visor process)
		conf.Launcher.Apps = []appserver.AppConfig{
			{
				Name:      skyenv.VPNClientName,
				AutoStart: false,
				Port:      routing.Port(skyenv.VPNClientPort),
				Args:      []string{"--dns", dnsServer},
			},
			{
				Name:      skyenv.SkychatName,
				AutoStart: true,
				Port:      routing.Port(skyenv.SkychatPort),
				Args:      []string{"--addr", skyenv.SkychatAddr},
			},
			{
				Name:      skyenv.SkysocksName,
				AutoStart: true,
				Port:      routing.Port(skyenv.SkysocksPort),
				Args:      []string{},
			},
			{
				Name:      skyenv.SkysocksClientName,
				AutoStart: false,
				Port:      routing.Port(skyenv.SkysocksClientPort),
				Args:      []string{"--addr", skyenv.SkysocksClientAddr},
			},
			{
				Name:      skyenv.VPNServerName,
				AutoStart: isVpnServerEnable,
				Args:      []string{},
				Port:      routing.Port(skyenv.VPNServerPort),
			},
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
	// add example applications to the config
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

	if addVPNServerWhitelist != "" {
		changeAppsConfig(conf, "vpn-server", "--whitelist", addVPNServerWhitelist)
	}
	if setVPNServerNetIfc != "" {
		changeAppsConfig(conf, "vpn-server", "--netifc", setVPNServerNetIfc)
	}
	switch setVPNServerSecure {
	case "true":
		changeAppsConfig(conf, "vpn-server", "--secure", setVPNServerSecure)
	case "false":
		changeAppsConfig(conf, "vpn-server", "--secure", setVPNServerSecure)
	}
	switch setVPNServerAutostart {
	case "true":
		for i, app := range conf.Launcher.Apps {
			if app.Name == "vpn-server" {
				conf.Launcher.Apps[i].AutoStart = true
			}
		}
	case "false":
		for i, app := range conf.Launcher.Apps {
			if app.Name == "vpn-server" {
				conf.Launcher.Apps[i].AutoStart = false
			}
		}
	}

	switch setVPNClientKillswitch {
	case "true":
		changeAppsConfig(conf, "vpn-client", "--killswitch", setVPNClientKillswitch)
	case "false":
		changeAppsConfig(conf, "vpn-client", "--killswitch", setVPNClientKillswitch)
	}
	if addVPNClientSrv != "" {
		keyParsed, err := coinCipher.PubKeyFromHex(strings.TrimSpace(addVPNClientSrv))
		if err != nil {
			log.WithError(err).Fatalf("Failed to parse hypervisor private key: %s.", addVPNClientSrv)
		}
		changeAppsConfig(conf, "vpn-client", "--srv", keyParsed.Hex())
	}
	if addSkysocksClientSrv != "" {
		keyParsed, err := coinCipher.PubKeyFromHex(strings.TrimSpace(addSkysocksClientSrv))
		if err != nil {
			logger.WithError(err).Fatalf("Failed to parse public key: %s.", addSkysocksClientSrv)
		}
		changeAppsConfig(conf, "skysocks-client", "--srv", keyParsed.Hex())
	}
	if proxyServerWhitelist != "" {
		changeAppsConfig(conf, "skysocks", "--whitelist", proxyServerWhitelist)
	}

	if disableProxyServerAutostart {
		for i, app := range conf.Launcher.Apps {
			if app.Name == "skysocks" {
				conf.Launcher.Apps[i].AutoStart = false
			}
		}
	}
	if enableProxyClientAutostart {
		for i, app := range conf.Launcher.Apps {
			if app.Name == "skysocks-client" {
				conf.Launcher.Apps[i].AutoStart = true
			}
		}
	}
	if isHypervisor {
		// Disable hypervisor UI authentication --disable-auth flag
		if isDisableAuth {
			conf.Hypervisor.EnableAuth = false
		}
		// Enable hypervisor UI authentication --enable-auth flag
		if isEnableAuth {
			conf.Hypervisor.EnableAuth = true
		}
	}
	// Enable hypervisor UI authentication on windows & macos
	if (selectedOS == "win") || (selectedOS == "mac") {
		if isHypervisor {
			conf.Hypervisor.EnableAuth = true
		}
	}
}

// applyOverrides applies final flag-driven overrides to the config
// (bin_path, version, autoconnect, display IP).
func applyOverrides() {
	// set bin_path for apps from flag
	if binPath != "" {
		conf.Launcher.BinPath = binPath
	}
	// set version of the config file from flag - testing override
	if ver != "" {
		conf.Common.Version = ver
	}
	// Disable autoconnect to public visors
	if disablePublicAutoConn {
		conf.Transport.PublicAutoconnect = false
	}
	// Enable the display of the visor's ip address in service discovery services
	if isDisplayNodeIP {
		conf.Launcher.DisplayNodeIP = true
	}
}

// writeConfigOutput marshals the config to JSON and writes it to a file
// or stdout depending on flags.
func writeConfigOutput(log *logging.Logger) {
	//don't write file with stdout
	if !isStdout {
		// Marshal the modified config to JSON with indentation
		jsonData, err := json.MarshalIndent(conf, "", "  ")
		if err != nil {
			log.WithError(err).Fatal("Failed to marshal config to indented JSON")
		}
		if snConfig {
			jsonData, err = script.Echo(string(jsonData)).JQ("{public_key: .pk, secret_key: .sk, dmsg: {discovery: .dmsg.discovery, sessions_count: .dmsg.sessions_count, servers: .dmsg.servers}, transport_discovery: .transport.discovery, log_level: .log_level}").Bytes()
			if err != nil {
				log.Fatalf("Failed to convert config to setup-node config format: %v", err)
			}
		}
		// Write the JSON data back to the file
		err = os.WriteFile(confPath, jsonData, configFilePerms)
		if err != nil {
			log.Fatalf("Failed to write config file: %v", err)
		}
	}
	// Print results.
	j, err := json.MarshalIndent(conf, "", "\t")
	if err != nil {
		log.WithError(err).Fatal("Failed to marshal config to indented JSON")
	}
	if snConfig {
		j, err = script.Echo(string(j)).JQ("{public_key: .pk, secret_key: .sk, dmsg: {discovery: .dmsg.discovery, sessions_count: .dmsg.sessions_count, servers: .dmsg.servers}, transport_discovery: .transport.discovery, log_level: .log_level}").Bytes()
		if err != nil {
			log.Fatalf("Failed to convert config to setup-node config format: %v", err)
		}
		var data any
		if err = json.Unmarshal(j, &data); err != nil {
			log.Fatalf("Failed to convert config to setup-node config format: %v", err)
		}
		j, err = json.MarshalIndent(data, "", "    ")
		if err != nil {
			log.WithError(err).Fatal("Failed to marshal config to indented JSON")
		}
	}
	//print config to stdout, omit logging messages, exit
	if isStdout {
		if isSquash {
			script.Echo(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(string(j), " ", ""), "\n", ""), "\t", "")).Stdout() //nolint:errcheck,gosec
			return
		}
		script.Echo(string(j)).Stdout() //nolint:errcheck,gosec
		return
	}
	//hide the printing of the config to the terminal
	if isHide {
		log.Infof("Updated file '%s'\n", output)
		return
	}
	//default behavior
	log.Infof("Updated file '%s' to:\n%s\n", output, j)
}

func getInterfaceNames() string { //nolint Note: pending implementation for config gen
	interfaces, err := net.Interfaces()
	if err != nil {
		fmt.Println("Error:", err)
		return ""
	}

	var interfaceNames []string
	defaultInterface := ""
	for _, iface := range interfaces {
		if iface.Flags&net.FlagLoopback == 0 {
			interfaceNames = append(interfaceNames, iface.Name)
			if iface.Index == 0 && defaultInterface == "" {
				defaultInterface = iface.Name
			}
		}
	}

	if defaultInterface != "" {
		// Move the default interface name to the beginning of the list
		for i, name := range interfaceNames {
			if name == defaultInterface {
				copy(interfaceNames[1:i+1], interfaceNames[:i])
				interfaceNames[0] = defaultInterface
				break
			}
		}
	}

	return strings.Join(interfaceNames, ", ")
}
