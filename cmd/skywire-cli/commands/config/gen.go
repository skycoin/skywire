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

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/netutil"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/dmsgc"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport/network"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
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
			logger.WithError(err).Fatal("invalid public key ") //nolint
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

	genConfigCmd.Flags().StringVarP(&serviceConfURL, "url", "a", scriptExecArray(fmt.Sprintf("${SVCCONFADDR[@]-%s}", serviceConfURL)), "services conf url\033[0m\n\r")
	gHiddenFlags = append(gHiddenFlags, "url")
	genConfigCmd.Flags().StringVar(&logLevel, "loglvl", scriptExecString("${LOGLVL:-info}"), "level of logging in config\033[0m")
	gHiddenFlags = append(gHiddenFlags, "loglvl")
	genConfigCmd.Flags().BoolVarP(&isBestProtocol, "bestproto", "b", scriptExecBool("${BESTPROTO:-false}"), "best protocol (dmsg | direct) based on location\033[0m") //this will also disable public autoconnect based on location
	genConfigCmd.Flags().BoolVarP(&isDisableAuth, "noauth", "c", false, "disable authentication for hypervisor UI\033[0m")
	gHiddenFlags = append(gHiddenFlags, "noauth")
	genConfigCmd.Flags().BoolVarP(&isDmsgHTTP, "dmsghttp", "d", scriptExecBool("${DMSGHTTP:-false}"), "use dmsg connection to skywire services\033[0m")
	gHiddenFlags = append(gHiddenFlags, "dmsghttp")
	genConfigCmd.Flags().StringVarP(&dmsgHTTPPath, "dmsgconf", "D", scriptExecString(fmt.Sprintf("${DMSGCONF:-%s}", visorconfig.DMSGHTTPName)), "dmsghttp-config path\033[0m")
	gHiddenFlags = append(gHiddenFlags, "dmsgconf")
	genConfigCmd.Flags().IntVar(&minDmsgSess, "minsess", scriptExecInt("${MINDMSGSESS:-2}"), "number of dmsg servers to connect to (0 = unlimited)\033[0m")
	gHiddenFlags = append(gHiddenFlags, "minsess")
	genConfigCmd.Flags().BoolVarP(&isEnableAuth, "auth", "e", false, "enable auth on hypervisor UI\033[0m")
	gHiddenFlags = append(gHiddenFlags, "auth")
	genConfigCmd.Flags().BoolVarP(&isForce, "force", "f", false, "remove pre-existing config\033[0m")
	gHiddenFlags = append(gHiddenFlags, "force")
	genConfigCmd.Flags().StringVarP(&disableApps, "disableapps", "g", "", "comma separated list of apps to disable\033[0m")
	gHiddenFlags = append(gHiddenFlags, "disableapps")
	genConfigCmd.Flags().BoolVarP(&isHypervisor, "ishv", "i", scriptExecBool("${ISHYPERVISOR:-false}"), "local hypervisor configuration\033[0m")
	msg = "list of public keys to add as hypervisor\033[0m"
	if scriptExecArray("${HYPERVISORPKS[@]}") != "" {
		msg += "\n\r"
	}
	genConfigCmd.Flags().StringVarP(&hypervisorPKs, "hvpks", "j", scriptExecArray("${HYPERVISORPKS[@]}"), msg)
	msg = "add dmsgpty whitelist PKs\033[0m"
	if scriptExecArray("${DMSGPTYPKS[@]}") != "" {
		msg += "\n\r"
	}
	genConfigCmd.Flags().StringVar(&dmsgptyWlPKs, "dmsgpty", scriptExecArray("${DMSGPTYPKS[@]}"), msg)
	msg = "add survey whitelist PKs\033[0m"
	if scriptExecArray("${SURVEYPKS[@]}") != "" {
		msg += "\n\r"
	}

	genConfigCmd.Flags().StringVar(&surveyWhitelistPKs, "survey", scriptExecArray("${SURVEYPKS[@]}"), msg)
	gHiddenFlags = append(gHiddenFlags, "survey")
	msg = "add route setup node PKs\033[0m"
	if scriptExecArray("${ROUTESETUPPKS[@]}") != "" {
		msg += "\n\r"
	}
	genConfigCmd.Flags().StringVar(&routeSetupNodes, "routesetup", scriptExecArray("${ROUTESETUPPKS[@]}"), msg)
	gHiddenFlags = append(gHiddenFlags, "routesetup")
	msg = "add transport setup node PKs\033[0m"
	if scriptExecArray("${TPSETUPPKS[@]}") != "" {
		msg += "\n\r"
	}
	genConfigCmd.Flags().StringVar(&transportSetupPKs, "tpsetup", scriptExecArray("${TPSETUPPKS[@]}"), msg)
	gHiddenFlags = append(gHiddenFlags, "tpsetup")

	genConfigCmd.Flags().StringVarP(&selectedOS, "os", "k", visorconfig.OS, "(linux / mac / win) paths\033[0m")
	gHiddenFlags = append(gHiddenFlags, "os")
	genConfigCmd.Flags().BoolVarP(&isDisplayNodeIP, "publicip", "l", scriptExecBool("${DISPLAYNODEIP:-false}"), "display visor ip in service discovery\033[0m")
	gHiddenFlags = append(gHiddenFlags, "publicip")
	genConfigCmd.Flags().BoolVarP(&addExampleApps, "example-apps", "m", false, "add example apps to the config\033[0m")
	gHiddenFlags = append(gHiddenFlags, "example-apps")
	genConfigCmd.Flags().BoolVarP(&isStdout, "stdout", "n", false, "write config to stdout\033[0m")
	gHiddenFlags = append(gHiddenFlags, "stdout")
	genConfigCmd.Flags().BoolVarP(&isSquash, "squash", "N", false, "output config without whitespace or newlines\033[0m")
	gHiddenFlags = append(gHiddenFlags, "squash")
	genConfigCmd.Flags().BoolVarP(&isEnvs, "envs", "q", false, "show the environmental variable settings\033[0m")
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
	genConfigCmd.Flags().BoolVarP(&isRegen, "regen", "r", false, "re-generate existing config & retain keys\033[0m")
	if scriptExecString("${SK:-0000000000000000000000000000000000000000000000000000000000000000}") != "0000000000000000000000000000000000000000000000000000000000000000" {
		sk.Set(scriptExecString("${SK:-0000000000000000000000000000000000000000000000000000000000000000}")) //nolint
	}
	genConfigCmd.Flags().VarP(&sk, "sk", "s", "a random key is generated if unspecified\033[0m\n\r")
	gHiddenFlags = append(gHiddenFlags, "sk")
	genConfigCmd.Flags().BoolVarP(&isTestEnv, "testenv", "t", scriptExecBool("${TESTENV:-false}"), "use test deployment\033[0m")
	gHiddenFlags = append(gHiddenFlags, "testenv")
	genConfigCmd.Flags().BoolVarP(&isVpnServerEnable, "servevpn", "v", scriptExecBool("${VPNSERVER:-false}"), "enable vpn server\033[0m")
	gHiddenFlags = append(gHiddenFlags, "servevpn")
	genConfigCmd.Flags().BoolVarP(&isHide, "hide", "w", false, "dont print the config to the terminal :: show errors with -n flag\033[0m")
	gHiddenFlags = append(gHiddenFlags, "hide")
	genConfigCmd.Flags().BoolVarP(&isRetainHypervisors, "retainhv", "x", false, "retain existing hypervisors with regen\033[0m")
	gHiddenFlags = append(gHiddenFlags, "retainhv")
	genConfigCmd.Flags().BoolVarP(&disablePublicAutoConn, "autoconn", "y", scriptExecBool("${DISABLEPUBLICAUTOCONN:-false}"), "disable autoconnect to public visors\033[0m")
	gHiddenFlags = append(gHiddenFlags, "hide")
	genConfigCmd.Flags().BoolVarP(&isPublic, "public", "z", scriptExecBool("${VISORISPUBLIC:-false}"), "publicize visor in service discovery\033[0m")
	gHiddenFlags = append(gHiddenFlags, "public")
	genConfigCmd.Flags().IntVar(&stcprPort, "stcpr", scriptExecInt("${STCPRPORT:-0}"), "set tcp transport listening port - 0 for random\033[0m")
	gHiddenFlags = append(gHiddenFlags, "stcpr")
	genConfigCmd.Flags().IntVar(&sudphPort, "sudph", scriptExecInt("${SUDPHPORT:-0}"), "set udp transport listening port - 0 for random\033[0m")
	gHiddenFlags = append(gHiddenFlags, "sudph")
	genConfigCmd.Flags().StringVar(&binPath, "binpath", scriptExecString("${BINPATH}"), "set bin_path for visor native apps\033[0m")
	gHiddenFlags = append(gHiddenFlags, "binpath")
	genConfigCmd.Flags().StringVar(&addSkysocksClientSrv, "proxyclientpk", scriptExecString("${PROXYCLIENTPK}"), "set server public key for proxy client\033[0m")
	gHiddenFlags = append(gHiddenFlags, "proxyclientpk")
	genConfigCmd.Flags().BoolVar(&enableProxyClientAutostart, "startproxyclient", scriptExecBool("${STARTPROXYCLIENT:-false}"), "autostart proxy client\033[0m")
	gHiddenFlags = append(gHiddenFlags, "startproxyclient")
	genConfigCmd.Flags().BoolVar(&disableProxyServerAutostart, "noproxyserver", scriptExecBool("${NOPROXYSERVER:-false}"), "disable autostart of proxy server\033[0m")
	gHiddenFlags = append(gHiddenFlags, "noproxyserver")
	genConfigCmd.Flags().StringVar(&proxyServerPass, "proxyserverpass", scriptExecString("${PROXYSEVERPASS}"), "set proxy server password\033[0m")
	gHiddenFlags = append(gHiddenFlags, "proxyserverpass")
	genConfigCmd.Flags().StringVar(&proxyClientPass, "proxyclientpass", scriptExecString("${PROXYCLIENTPASS}"), "password for the proxy client to access the server (if needed)\033[0m")
	gHiddenFlags = append(gHiddenFlags, "proxyclientpass")
	// TODO: Password for accessing proxy client
	// TODO: VPN client killswitch should be handled as boolean, not string
	genConfigCmd.Flags().StringVar(&setVPNClientKillswitch, "killsw", scriptExecString("${VPNKS}"), "vpn client killswitch\033[0m")
	gHiddenFlags = append(gHiddenFlags, "killsw")
	genConfigCmd.Flags().StringVar(&addVPNClientSrv, "addvpn", scriptExecString("${ADDVPNPK}"), "set vpn server public key for vpn client\033[0m")
	gHiddenFlags = append(gHiddenFlags, "addvpn")
	genConfigCmd.Flags().StringVar(&addVPNClientPasscode, "vpnpass", scriptExecString("${VPNCLIENTPASS}"), "password for vpn client to access the vpn server (if needed)\033[0m")
	gHiddenFlags = append(gHiddenFlags, "vpnpass")
	genConfigCmd.Flags().StringVar(&addVPNServerPasscode, "vpnserverpass", scriptExecString("${VPNSEVERPASS}"), "set password to the vpn server\033[0m")
	gHiddenFlags = append(gHiddenFlags, "vpnserverpass")
	genConfigCmd.Flags().StringVar(&setVPNServerSecure, "secure", scriptExecString("${VPNSEVERSECURE}"), "change secure mode status of vpn server\033[0m")
	gHiddenFlags = append(gHiddenFlags, "secure")
	genConfigCmd.Flags().StringVar(&setVPNServerNetIfc, "netifc", scriptExecString("${VPNSEVERNETIFC}"), "VPN Server network interface (detected: "+getInterfaceNames()+")\033[0m")
	gHiddenFlags = append(gHiddenFlags, "netifc")
	genConfigCmd.Flags().BoolVar(&noFetch, "nofetch", false, "do not fetch the services from the service conf url\033[0m")
	gHiddenFlags = append(gHiddenFlags, "nofetch")
	//TODO: visorconfig.SvcConfName
	genConfigCmd.Flags().StringVarP(&configServicePath, "svcconf", "S", scriptExecString(fmt.Sprintf("${SVCCONF:-%s}", visorconfig.SERVICESName)), "fallback service configuration file\033[0m")
	gHiddenFlags = append(gHiddenFlags, "svcconf")
	genConfigCmd.Flags().BoolVar(&noDefaults, "nodefaults", false, "do not use hardcoded defaults for services\033[0m")
	gHiddenFlags = append(gHiddenFlags, "nodefaults")
	genConfigCmd.Flags().BoolVar(&snConfig, "sn", false, "generate config for route setup node\033[0m")
	gHiddenFlags = append(gHiddenFlags, "sn")
	genConfigCmd.Flags().StringVar(&ver, "version", scriptExecString("${VERSION}"), "custom version testing override\033[0m")
	gHiddenFlags = append(gHiddenFlags, "version")
	genConfigCmd.Flags().BoolVar(&isAll, "all", false, "show all flags\033[0m")

	//show all flags on help
	if os.Getenv("UNHIDEFLAGS") != "1" {
		for _, j := range gHiddenFlags {
			genConfigCmd.Flags().MarkHidden(j) //nolint
		}
	}
}

var genConfigCmd = &cobra.Command{
	Use:   "gen",
	Short: "Generate a config file",
	Long: func() string {
		if visorconfig.OS == "linux" {
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
		//use test deployment
		if isTestEnv {
			serviceConfURL = testServiceConfURL
		}
		var err error
		if isPkgEnv {
			if dmsgHTTPPath == visorconfig.DMSGHTTPName {
				dmsgHTTPPath = visorconfig.SkywirePath + "/" + visorconfig.DMSGHTTPName //nolint
			}
		}
		if isDmsgHTTP {
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
		if isPkgEnv && configServicePath == visorconfig.SERVICESName {
			configServicePath = visorconfig.SkywirePath + "/" + visorconfig.SERVICESName
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

		if !noFetch && !isDmsgHTTP {
			client := http.Client{Timeout: 15 * time.Second}
			if serviceConfURL == "" {
				serviceConfURL = "http://"
			}
			if !isStdout {
				log.Infof("Fetching service endpoints from %s", serviceConfURL)
			}
			res, err := client.Get(serviceConfURL)
			if err != nil {
				if !isStdout {
					log.WithError(err).Error("Failed to fetch servers")
					log.Warn("Falling back on services-config.json")
				}
				body, err := os.ReadFile(configServicePath)
				if err != nil {
					if !isStdout {
						log.WithError(err).Error("Failed to read config service from file")
						log.Warn("Falling back on hardcoded servers")
					}
					return
				}
				if err := json.Unmarshal(body, &servicesConfig); err != nil {
					if !isStdout {
						log.WithError(err).Error("Failed to unmarshal services-config.json file")
						log.Warn("Falling back on hardcoded servers")
					}
					return
				}
				services = servicesConfig.Prod
				if isTestEnv {
					services = servicesConfig.Test
				}
			} else {
				defer res.Body.Close() //nolint
				body, err := io.ReadAll(res.Body)
				if err != nil {
					log.WithError(err).Error("Failed to read HTTP response")
					return
				}
				if err := json.Unmarshal(body, &services); err != nil {
					if !isStdout {
						log.WithError(err).Error("Failed to unmarshal JSON response to services struct")
						log.Warn("Falling back on hardcoded servers")
					}
					return
				} else if !isStdout {
					log.Infof("Fetched service endpoints from '%s'", serviceConfURL)
				}
			}
		} else {
			body, err := os.ReadFile(configServicePath)
			if err != nil {
				if !isStdout {
					log.WithError(err).Error("Failed to read config service from file")
					log.Warn("Falling back on hardcoded servers")
				}
				return
			}
			if err := json.Unmarshal(body, &servicesConfig); err != nil {
				if !isStdout {
					log.WithError(err).Error("Failed to unmarshal services-config.json file")
					log.Warn("Falling back on hardcoded servers")
				}
				return
			}
			services = servicesConfig.Prod
			if isTestEnv {
				services = servicesConfig.Test
			}
		}

		// reset the state of isStdout
		isStdout = wasStdout
		// Read in old config and obtain old secret key or generate a new random secret key
		// and obtain old hypervisors (if any)
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

		if isDmsgHTTP {
			// TODO
			//if isUsrEnv {
			//	dmsgHTTPPath = homepath + "/" + visorconfig.DMSGHTTPName
			//}
			if isPkgEnv {
				dmsgHTTPPath = visorconfig.SkywirePath + "/" + visorconfig.DMSGHTTPName //nolint
			}

			// Read the JSON configuration file
			dmsghttpConfigData, err := os.ReadFile(dmsgHTTPPath) //nolint
			if err != nil {
				log.Fatalf("Failed to read config file: %v", err)
			}

			// Decode JSON data
			err = json.Unmarshal(dmsghttpConfigData, &dmsgHTTPServersList)
			if err != nil {
				log.WithError(err).Fatal("Failed to unmarshal " + visorconfig.DMSGHTTPName)
			}
		}

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

		conf.Dmsg = &dmsgc.DmsgConfig{
			Discovery:            services.DmsgDiscovery,
			SessionsCount:        minDmsgSess,
			Servers:              []*disc.Entry{},
			ConnectedServersType: "all",
		}
		conf.Transport = &visorconfig.Transport{
			Discovery:         services.TransportDiscovery, //utilenv.TpDiscAddr,
			AddressResolver:   services.AddressResolver,    //utilenv.AddressResolverAddr,
			PublicAutoconnect: visorconfig.PublicAutoconnect,
			TransportSetupPKs: services.TransportSetupPKs,
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

		// Configure public visor
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
			conf.DmsgHTTPServerPath = pkgConfig.LocalPath + "/" + visorconfig.Custom
			conf.Launcher.BinPath = pkgConfig.LauncherBinPath
			conf.Transport.LogStore.Location = pkgConfig.LocalPath + "/" + visorconfig.TpLogStore
			if conf.Hypervisor != nil {
				conf.Hypervisor.EnableAuth = pkgConfig.Hypervisor.EnableAuth
				conf.Hypervisor.DBPath = pkgConfig.Hypervisor.DbPath
			}
		}
		// set config paths for the user space
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
		// App config settings
		conf.Launcher.Apps = []appserver.AppConfig{
			{
				Name:      visorconfig.VPNClientName,
				Binary:    "skywire",
				AutoStart: false,
				Port:      routing.Port(skyenv.VPNClientPort),
				Args:      []string{"app", "vpn-client", "--dns", dnsServer},
			},
			{
				Name:      visorconfig.SkychatName,
				Binary:    "skywire",
				AutoStart: true,
				Port:      routing.Port(skyenv.SkychatPort),
				Args:      []string{"app", "skychat", "--addr", visorconfig.SkychatAddr},
			},
			{
				Name:      visorconfig.SkysocksName,
				Binary:    "skywire",
				AutoStart: true,
				Port:      routing.Port(visorconfig.SkysocksPort),
				Args:      []string{"app", "skysocks"},
			},
			{
				Name:      visorconfig.SkysocksClientName,
				Binary:    "skywire",
				AutoStart: false,
				Port:      routing.Port(visorconfig.SkysocksClientPort),
				Args:      []string{"app", "skysocks-client", "--addr", visorconfig.SkysocksClientAddr},
			},
			{
				Name:      visorconfig.VPNServerName,
				Binary:    "skywire",
				AutoStart: isVpnServerEnable,
				Args:      []string{"app", "vpn-server"},
				Port:      routing.Port(visorconfig.VPNServerPort),
			},
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

		if addVPNServerPasscode != "" {
			changeAppsConfig(conf, "vpn-server", "--passcode", addVPNServerPasscode)
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

		if addVPNClientPasscode != "" {
			changeAppsConfig(conf, "vpn-client", "--passcode", addVPNClientPasscode)
		}
		if addSkysocksClientSrv != "" {
			keyParsed, err := coinCipher.PubKeyFromHex(strings.TrimSpace(addSkysocksClientSrv))
			if err != nil {
				logger.WithError(err).Fatalf("Failed to parse public key: %s.", addSkysocksClientSrv)
			}
			changeAppsConfig(conf, "skysocks-client", "--srv", keyParsed.Hex())
		}
		if proxyServerPass != "" {
			changeAppsConfig(conf, "skysocks", "--passcode", proxyServerPass)
		}
		if proxyClientPass != "" {
			changeAppsConfig(conf, "skysocks-client", "--passcode", proxyClientPass)
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
			err = os.WriteFile(confPath, jsonData, 0644) //nolint
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
				script.Echo(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(string(j), " ", ""), "\n", ""), "\t", "")).Stdout() //nolint
				return
			}
			script.Echo(string(j)).Stdout() //nolint
			return
		}
		//hide the printing of the config to the terminal
		if isHide {
			log.Infof("Updated file '%s'\n", output)
			return
		}
		//default behavior
		log.Infof("Updated file '%s' to:\n%s\n", output, j)
	},
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

const envfileLinux = `#
# /etc/skywire.conf
#
#########################################################################
#	SKYWIRE CONFIG TEMPLATE
#		Defaults for booleans are false
#		Uncomment to change default value
#########################################################################

### Installation path ###################################################

#--	Default config paths for the installer or package (system paths)
#PKGENV=true

#--	Default config paths for the current userspace
#USRENV=true

#--	fallback service conf path
#SVCCONF="services-config.json"

#--	dmsghttp config path
#DMSGCONF="dmsghttp-config.json"

#--	Output path of the config file
#OUTPUT='./skywire-config.json'

#--	Set app bin_path
#BINPATH='./apps'

### Deployment ##########################################################

#--	Set custom service conf URLs
#SVCCONFADDR=('')

#--	Use test deployment
#TESTENV=true

#--	Use dmsghttp to connect to the production deployment ; overrides BESTPROTO=true
#DMSGHTTP=true

#--	Number of dmsg serverts to connect to (0 unlimits)
#MINDMSGSESS=8

#--	Automatically determine the best protocol (dmsg or http)
#	based on location to connect to the deployment servers
#BESTPROTO=true

### Transports ##########################################################

#--	Other Visors will automatically establish transports to this visor
#	requires port forwarding or public ip
#VISORISPUBLIC=true

#--	Disable auto-transports to public visors from this visor
#DISABLEPUBLICAUTOCONN=true

#-- Add transport setup public keys
#TPSETUPPKS('')

### Ports ###############################################################

#- set port for UDP connections / SUDPH transports
#SUDPHPORT=0

#- set port for TCP connections / STCPR or STCP transports
#STCPRPORT=0

### Routing #############################################################

#-- Add route setup-node public keys
#ROUTESETUPPKS('')

### Remote Access #######################################################

#--	Set remote hypervisor public keys
#HYPERVISORPKS=('')

#--	Grant access to pseudoterminal (pty) for public keys
#DMSGPTYPKS('')

### Survey Access #######################################################

#--	Grant access for survey collection to these public keys
#SURVEYPKS('')

### Hypervisor UI #######################################################

#--	Start the hypervisor interface for this visor
#ISHYPERVISOR=true

### Apps ################################################################

#--	Display the node ip in the service discovery
#	for any public services this visor is running
#DISPLAYNODEIP=true

#--	Autostart vpn server for this visor
#VPNSERVER=true

#--	Set server public key for proxy client to connect to
#PROXYCLIENTPK=''

#--	Enable autostart of the proxy client
#STARTPROXYCLIENT=true

#--	Disable autostart of proxy server
#NOPROXYSERVER=true

#--	Set a password for the proxy server
#PROXYSEVERPASS=''

#--	Password for the proxy client to access the server
# (if password is set for the server)
#PROXYCLIENTPASS=''

#--	Set VPN client killswitch
#VPNKS=true

#--	Set vpn server public key for the vpn client to use
#ADDVPNPK=''

#--	Password for vpn client to access the server
# (if password is set for the server)
#VPNCLIENTPASS=''

#--	Set password to the vpn server
#VPNSEVERPASS=''

#--	Change secure mode status of vpn server
#VPNSEVERSECURE=''

#--	Set VPN Server network interface - i.e. eth0
#VPNSEVERNETIFC=''

### Miscellaneous #######################################################

#--	Set secret key
#SK=''

#--	Custom config version override
#VERSION=''

#--	Set visor runtime log level.
#	Default is info ; uncomment for debug logging
#LOGLVL=debug

`

const envfileWindows = `#
# C:\ProgramData\skywire.conf
#
#########################################################################
#	SKYWIRE CONFIG TEMPLATE
#		Defaults for booleans are false
#		Uncomment to change default value
#########################################################################

### Installation path ###################################################

#--	Default config paths for the installer or package (system paths)
#$PKGENV=$true

#--	Default config paths for the current userspace
#$USRENV=$true

#--	fallback service conf path
#$SVCCONF="services-config.json"

#--	dmsghttp config path
#$DMSGCONF="dmsghttp-config.json"

#--	Output path of the config file
#$OUTPUT='C:\\ProgramData\\skywire-config.json'

#--	Set app bin_path
#$BINPATH='C:\\ProgramData\\apps'

### Deployment ##########################################################

#--	Set custom service conf URLs
#$SVCCONFADDR=@('')

#--	Use test deployment
#$TESTENV=$true

#--	Use dmsghttp to connect to the production deployment ; overrides BESTPROTO=$true
#$DMSGHTTP=$true

#--	Number of dmsg servers to connect to (0 unlimits)
#$MINDMSGSESS=8

#--	Automatically determine the best protocol (dmsg or http)
#	based on location to connect to the deployment servers
#$BESTPROTO=$true

### Transports ##########################################################

#--	Other Visors will automatically establish transports to this visor
#	requires port forwarding or public IP
#$VISORISPUBLIC=$true

#--	Disable auto-transports to public visors from this visor
#$DISABLEPUBLICAUTOCONN=$true

#--	Add transport setup public keys
#$TPSETUPPKS=@('')

### Ports ###############################################################

#- set port for UDP connections / SUDPH transports
#$SUDPHPORT=0

#- set port for TCP connections / STCPR or STCP transports
#$STCPRPORT=0

### Routing #############################################################

#--	Add route setup-node public keys
#$ROUTESETUPPKS=@('')

### Remote Access #######################################################

#--	Set remote hypervisor public keys
#$HYPERVISORPKS=@('')

#--	Grant access to pseudoterminal (pty) for public keys
#$DMSGPTYPKS=@('')

### Survey Access #######################################################

#--	Grant access for survey collection to these public keys
#$SURVEYPKS=@('')

### Hypervisor UI #######################################################

#--	Start the hypervisor interface for this visor
#$ISHYPERVISOR=$true

### Apps ################################################################

#--	Display the node IP in the service discovery
#	for any public services this visor is running
#$DISPLAYNODEIP=$true

#--	Autostart VPN server for this visor
#$VPNSERVER=$true

#--	Set server public key for proxy client to connect to
#$PROXYCLIENTPK=''

#--	Enable autostart of the proxy client
#$STARTPROXYCLIENT=$true

#--	Disable autostart of proxy server
#$NOPROXYSERVER=$true

#--	Set a password for the proxy server
#$PROXYSEVERPASS=''

#--	Password for the proxy client to access the server (if password is set for the server)
#$PROXYCLIENTPASS=''

#--	Set VPN client killswitch
#$VPNKS=$true

#--	Set VPN server public key for the VPN client to use
#$ADDVPNPK=''

#--	Password for VPN client to access the server (if password is set for the server)
#$VPNCLIENTPASS=''

#--	Set password to the VPN server
#$VPNSEVERPASS=''

#--	Change secure mode status of VPN server
#$VPNSEVERSECURE=''

#--	Set VPN Server network interface, e.g., 'Ethernet'
#$VPNSEVERNETIFC=''

### Miscellaneous #######################################################

#--	Set secret key
#$SK=''

#--	Custom config version override
#$VERSION=''

#--	Set visor runtime log level.
#	Default is info ; uncomment for debug logging
#$LOGLVL='debug'
`
