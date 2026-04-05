// Package cliconfig cmd/skywire-cli/commands/config/gen.go
package cliconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
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
	"github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/dmsg/pkg/dmsghttp"
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
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/netutil"
	"github.com/skycoin/skywire/pkg/transport/network"
	"github.com/skycoin/skywire/pkg/visor/rewardconfig"
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

// testenvPortOffset is added to localhost ports when generating a testenv config
// to avoid conflicts with a production visor running on the same machine.
const testenvPortOffset = 10000

// offsetAddr adds testenvPortOffset to a host:port or :port address string.
// Returns the original address unchanged if parsing fails or testenv is not enabled.
func offsetAddr(addr string) string {
	if !isTestEnv {
		return addr
	}
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return addr
	}
	return net.JoinHostPort(host, strconv.Itoa(port+testenvPortOffset))
}

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

var pkFromSKCmd = &cobra.Command{
	Use:   "pk <secret-key-hex>",
	Short: "derive public key from a secret key",
	Args:  cobra.ExactArgs(1),
	Run: func(_ *cobra.Command, args []string) {
		var sk cipher.SecKey
		if err := sk.Set(args[0]); err != nil {
			fmt.Fprintf(os.Stderr, "invalid secret key: %v\n", err) //nolint:errcheck,gosec
			os.Exit(1)
		}
		pk, err := sk.PubKey()
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to derive public key: %v\n", err) //nolint:errcheck,gosec
			os.Exit(1)
		}
		fmt.Println(pk.Hex())
	},
}

var (
	isEnvs     bool
	skyenvfile = os.Getenv("SKYENV")
)
var envfile string
var envfileOut string

func init() {
	var msg string
	//disable sorting, flags appear in the order shown here
	genConfigCmd.Flags().SortFlags = false
	RootCmd.AddCommand(genConfigCmd, genKeysCmd, pkFromSKCmd, checkPKCmd)

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
	genConfigCmd.Flags().BoolVarP(&isEnvs, "envs", "q", false, "show the conf template (reflects flags passed)")
	genConfigCmd.Flags().StringVarP(&envfileOut, "envout", "Q", "", "write conf template to file (reflects flags passed)")

	// Config generation flags
	genConfigCmd.Flags().BoolVarP(&isForce, "force", "f", false, "remove pre-existing config")
	gHiddenFlags = append(gHiddenFlags, "force")
	genConfigCmd.Flags().BoolVarP(&isRegen, "regen", "r", false, "re-generate existing config & retain keys")
	genConfigCmd.Flags().BoolVarP(&isRetainHypervisors, "retainhv", "x", false, "retain existing hypervisors with regen")
	gHiddenFlags = append(gHiddenFlags, "retainhv")

	// Network and deployment flags
	genConfigCmd.Flags().StringVarP(&serviceConfURL, "url", "a", scriptExecArray(fmt.Sprintf("${SVCCONFADDR[@]-%s}", serviceConfURL)), "services conf url\n\r")
	gHiddenFlags = append(gHiddenFlags, "url")
	genConfigCmd.Flags().BoolVarP(&isTestEnv, "testenv", "t", scriptExecBool("${TESTENV:-false}"), "use test deployment\n\r(ports are offset +10000 to allow running alongside prod)")
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
	// SvcConfName integration not wired in.
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
	// VPN client killswitch is handled as string for cobra flag compatibility.
	genConfigCmd.Flags().StringVar(&setVPNClientKillswitch, "killsw", scriptExecString("${VPNKS}"), "vpn client killswitch")
	gHiddenFlags = append(gHiddenFlags, "killsw")
	genConfigCmd.Flags().StringVar(&addVPNClientSrv, "addvpn", scriptExecString("${ADDVPNPK}"), "set vpn server public key for vpn client")
	gHiddenFlags = append(gHiddenFlags, "addvpn")
	genConfigCmd.Flags().StringVar(&addVPNServerWhitelist, "vpnwl", scriptExecArray("${VPNSERVERWL[@]}"), "comma-separated list of public keys allowed to connect to vpn server (empty = allow all)")
	genConfigCmd.Flags().StringVar(&setVPNServerSecure, "secure", scriptExecString("${VPNSEVERSECURE}"), "change secure mode status of vpn server")
	gHiddenFlags = append(gHiddenFlags, "secure")
	genConfigCmd.Flags().StringVar(&setVPNServerNetIfc, "netifc", scriptExecString("${VPNSEVERNETIFC}"), "VPN Server network interface (detected: "+getInterfaceNames()+")")
	gHiddenFlags = append(gHiddenFlags, "netifc")

	// Proxy flags
	genConfigCmd.Flags().StringVar(&addSkysocksClientSrv, "proxyclientpk", scriptExecString("${PROXYCLIENTPK}"), "set server public key for proxy client")
	gHiddenFlags = append(gHiddenFlags, "proxyclientpk")
	genConfigCmd.Flags().BoolVar(&enableProxyClientAutostart, "startproxyclient", scriptExecBool("${STARTPROXYCLIENT:-false}"), "autostart proxy client")
	gHiddenFlags = append(gHiddenFlags, "startproxyclient")
	genConfigCmd.Flags().BoolVar(&isProxyServerEnable, "serveproxy", scriptExecBool("${PROXYSERVER:-true}"), "autostart proxy server (default: true)")
	genConfigCmd.Flags().StringVar(&proxyServerWhitelist, "proxywl", scriptExecArray("${PROXYSERVERWL[@]}"), "comma-separated list of public keys allowed to connect to proxy server (empty = allow all)")

	// Skychat flags
	genConfigCmd.Flags().BoolVar(&isSkychatEnable, "servechat", scriptExecBool("${SKYCHAT:-true}"), "autostart skychat (default: true)")
	genConfigCmd.Flags().StringVar(&skychatAddr, "chataddr", scriptExecString("${SKYCHATADDR:-"+skyenv.SkychatAddr+"}"), "skychat local address")
	gHiddenFlags = append(gHiddenFlags, "chataddr")

	// Reward address
	genConfigCmd.Flags().StringVar(&rewardSkyAddr, "rewardaddr", scriptExecString("${REWARDSKYADDR}"), "skycoin reward address or xpub key")

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

	// Advanced tuning flags (visible with --all)
	genConfigCmd.Flags().StringVar(&hvHTTPAddr, "hvaddr", scriptExecString("${HVHTTPADDR}"), "hypervisor HTTP address")
	gHiddenFlags = append(gHiddenFlags, "hvaddr")
	genConfigCmd.Flags().StringVar(&stunServers, "stun", scriptExecArray("${STUNSERVERS[@]}"), "comma-separated list of STUN servers")
	gHiddenFlags = append(gHiddenFlags, "stun")
	genConfigCmd.Flags().StringVar(&shutdownTimeout, "timeout", scriptExecString("${SHUTDOWNTIMEOUT}"), "graceful shutdown timeout (e.g. 10s)")
	gHiddenFlags = append(gHiddenFlags, "timeout")
	genConfigCmd.Flags().StringVar(&publicVisorRegTimeout, "regtimeout", scriptExecString("${REGTIMEOUT}"), "public visor registration timeout (e.g. 10m)")
	gHiddenFlags = append(gHiddenFlags, "regtimeout")
	genConfigCmd.Flags().IntVar(&publicVisorMaxTransports, "maxtransports", 0, "public visor max transports")
	gHiddenFlags = append(gHiddenFlags, "maxtransports")
	genConfigCmd.Flags().IntVar(&muxRoutes, "muxroutes", 0, "number of parallel mux routes per connection")
	gHiddenFlags = append(gHiddenFlags, "muxroutes")
	genConfigCmd.Flags().StringVar(&cliAddr, "cliaddr", scriptExecString("${CLIADDR}"), "CLI RPC address (e.g. 0.0.0.0:3435 for Docker)")
	gHiddenFlags = append(gHiddenFlags, "cliaddr")

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
		return `Generate a config file

	Custom deployment config (services-config.json) may be specified with:
	SKYDEPLOY=/path/to/services-config.json skywire cli config gen
	This overrides the embedded deployment defaults for all service URLs,
	DMSG servers, and DMSG endpoints. Use with --nofetch to skip HTTP fetch.`

	}(),
	PreRun: func(cmd *cobra.Command, _ []string) {
		log := logger
		if isEnvs || envfileOut != "" {
			if skyenv.OS == "windows" {
				envfile = envfileWindows
			} else {
				envfile = envfileLinux
			}
			// Uncomment settings that match flags passed on the command line
			envfile = applyFlagsToConf(envfile, cmd)

			if envfileOut != "" {
				if err := os.WriteFile(envfileOut, []byte(envfile+"\n"), 0644); err != nil { //nolint:gosec
					log.Fatalf("Failed to write conf file: %v", err)
				}
				fmt.Printf("Conf template written to %s\n", envfileOut)
			} else {
				fmt.Println(envfile)
			}
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

// fetchServiceConfig fetches service endpoints with the following priority:
//  1. DMSG — short-lived dmsghttp client using embedded servers (private, no DNS)
//  2. HTTP — plain HTTP to config service URL (current behavior)
//  3. Embedded — deployment.ServicesJSON (hardcoded defaults)
func fetchServiceConfig(log *logging.Logger) {
	var err error
	if !noFetch && !isDmsgHTTP {
		// Try DMSG-first fetch if we have a config service DMSG address
		if fetchServiceConfigDmsg(log) {
			return
		}
		// Fall back to HTTP
		client := http.Client{Timeout: servicesFetchTimeout}
		if serviceConfURL == "" {
			serviceConfURL = "http://"
		}
		if !isStdout {
			log.Infof("Fetching service endpoints from %s", serviceConfURL)
		}
		res, err := client.Get(serviceConfURL) //nolint:gosec
		if err != nil {
			logIfNotStdout(log, err, "Failed to fetch servers via HTTP")
			if !isStdout {
				log.Warn("Falling back on embedded config")
			}
			loadServicesFromFile(log)
			return
		}
		defer res.Body.Close() //nolint:errcheck,gosec
		body, err := io.ReadAll(res.Body)
		if err != nil {
			log.WithError(err).Error("Failed to read HTTP response")
			loadServicesFromFile(log)
			return
		}
		if err := json.Unmarshal(body, &services); err != nil {
			logIfNotStdout(log, err, "Failed to unmarshal JSON response to services struct")
			if !isStdout {
				log.Warn("Falling back on embedded config")
			}
			loadServicesFromFile(log)
			return
		}
		if !isStdout {
			log.Infof("Fetched service endpoints from '%s'", serviceConfURL)
		}
		// Supplement missing DMSG fields from embedded config if the
		// config bootstrapper hasn't been updated to serve them yet.
		if !services.HasDmsgEndpoints() {
			embedded := deployment.Prod
			if isTestEnv {
				embedded = deployment.Test
			}
			services.DmsgServers = embedded.DmsgServers
			services.ConfDmsg = embedded.ConfDmsg
			services.DmsgDiscoveryDmsg = embedded.DmsgDiscoveryDmsg
			services.TransportDiscoveryDmsg = embedded.TransportDiscoveryDmsg
			services.AddressResolverDmsg = embedded.AddressResolverDmsg
			services.RouteFinderDmsg = embedded.RouteFinderDmsg
			services.UptimeTrackerDmsg = embedded.UptimeTrackerDmsg
			services.ServiceDiscoveryDmsg = embedded.ServiceDiscoveryDmsg
		}
	} else {
		body := deployment.ServicesJSON
		if configServicePath != "" {
			body, err = os.ReadFile(configServicePath) //nolint:gosec
			if err != nil {
				logIfNotStdout(log, err, "Failed to read config service from file")
				if !isStdout {
					log.Warn("Falling back on embedded config")
				}
				return
			}
		}
		if err := json.Unmarshal(body, &servicesConfig); err != nil {
			logIfNotStdout(log, err, "Failed to unmarshal services-config.json file")
			if !isStdout {
				log.Warn("Falling back on embedded config")
			}
			return
		}
		services = servicesConfig.Prod
		if isTestEnv {
			services = servicesConfig.Test
		}
	}
}

// fetchServiceConfigDmsg tries to fetch service config from the config-bootstrapper
// over DMSG using a short-lived direct client. Returns true if successful.
func fetchServiceConfigDmsg(log *logging.Logger) bool {
	embeddedConf := deployment.Prod
	if isTestEnv {
		embeddedConf = deployment.Test
	}

	// Need both embedded DMSG servers and a config service DMSG address
	if len(embeddedConf.DmsgServers) == 0 || embeddedConf.ConfDmsg == "" {
		return false
	}

	if !isStdout {
		log.Infof("Fetching service endpoints via DMSG from %s", embeddedConf.ConfDmsg)
	}

	// Bootstrap a short-lived DMSG client using embedded servers.
	// Use a discard logger in stdout mode to prevent DMSG client logs
	// from polluting the JSON output.
	ctx, cancel := context.WithTimeout(context.Background(), servicesFetchTimeout)
	defer cancel()

	// Generate ephemeral keypair for the fetch
	pk, sk := cipher.GenerateKeyPair()

	dmsgLog := log
	if isStdout {
		silentLogger := logrus.New()
		silentLogger.SetOutput(io.Discard)
		dmsgLog = &logging.Logger{FieldLogger: silentLogger}
	}

	dmsgBoot, err := cmdutil.BootstrapDmsg(ctx, dmsgLog, pk, sk,
		dmsg.Prod.DmsgServers, embeddedConf.DmsgDiscovery, "")
	if err != nil {
		logIfNotStdout(log, err, "DMSG bootstrap failed for config fetch")
		return false
	}
	defer dmsgBoot.Close()

	// Make HTTP request through DMSG transport
	dmsgHTTPClient := &http.Client{
		Transport: dmsghttp.MakeHTTPTransport(ctx, dmsgBoot.Client),
		Timeout:   servicesFetchTimeout,
	}

	res, err := dmsgHTTPClient.Get(embeddedConf.ConfDmsg)
	if err != nil {
		logIfNotStdout(log, err, "Failed to fetch config via DMSG")
		return false
	}
	defer res.Body.Close() //nolint:errcheck
	body, err := io.ReadAll(res.Body)
	if err != nil {
		logIfNotStdout(log, err, "Failed to read DMSG response")
		return false
	}
	if err := json.Unmarshal(body, &services); err != nil {
		logIfNotStdout(log, err, "Failed to unmarshal DMSG config response")
		return false
	}
	if !isStdout {
		log.Info("Fetched service endpoints via DMSG")
	}
	return true
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
// With the unified services-config.json, DMSG fields are already in the services struct.
// The separate dmsghttp-config.json path is retained for backward compatibility
// and custom deployment overrides.
func configureDMSGHTTP(log *logging.Logger, outerErr error) {
	_ = outerErr
	if isDmsgHTTP || !isHTTPOnly {
		if dmsgHTTPPath != "" {
			// Override: load from user-supplied dmsghttp-config.json
			dmsghttpConfigData, err := os.ReadFile(dmsgHTTPPath)
			if err != nil {
				log.Fatalf("Failed to read config file: %v", err)
			}
			err = json.Unmarshal(dmsghttpConfigData, &dmsgHTTPServersList)
			if err != nil {
				log.WithError(err).Fatal("Failed to unmarshal " + skyenv.DMSGHTTPName)
			}
		} else if !services.HasDmsgEndpoints() {
			// Fallback: if fetched config didn't have DMSG fields,
			// try parsing the embedded config for legacy DmsgHTTPServers format
			err := json.Unmarshal(deployment.ServicesJSON, &dmsgHTTPServersList)
			if err != nil {
				log.WithError(err).Warn("Failed to parse legacy dmsghttp config from embedded services")
			}
		}
		// else: services struct already has DMSG fields from unified config
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

	if services.DmsgDiscovery == "" && services.DmsgDiscoveryDmsg == "" {
		log.Fatalf("Dmsg Discovery not set (neither HTTP nor DMSG)")
	}
	if services.TransportDiscovery == "" && services.TransportDiscoveryDmsg == "" {
		log.Fatalf("Transport Discovery not set (neither HTTP nor DMSG)")
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
// serviceDmsgServerEntries converts DmsgServerEntry to []*disc.Entry
// for use in the visor's DMSG config.
func serviceDmsgServerEntries() []*disc.Entry {
	var entries []*disc.Entry
	for _, s := range services.DmsgServers { //nolint:gocritic
		var pk cipher.PubKey
		if err := pk.UnmarshalText([]byte(s.Static)); err != nil {
			continue
		}
		entries = append(entries, &disc.Entry{
			Static: pk,
			Server: &disc.Server{Address: s.Server.Address},
		})
	}
	return entries
}

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
	tpLogPath := skyenv.LocalPath + "/" + skyenv.TpLogStore
	if isTestEnv {
		tpLogPath = skyenv.LocalPath + "-testenv/" + skyenv.TpLogStore
	}
	conf.Transport = &visorconfig.Transport{
		Discovery:         services.TransportDiscovery, //utilenv.TpDiscAddr,
		AddressResolver:   services.AddressResolver,    //utilenv.AddressResolverAddr,
		PublicAutoconnect: skyenv.PublicAutoconnect,
		TransportSetupPKs: services.TransportSetupPKs,
		LogStore: &visorconfig.LogStore{
			Type:             visorconfig.FileLogStore,
			Location:         tpLogPath,
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

	if muxRoutes > 0 {
		conf.Routing.MuxRoutes = muxRoutes
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
	localPath := skyenv.LocalPath
	if isTestEnv {
		localPath = skyenv.LocalPath + "-testenv"
	}
	conf.Launcher = &visorconfig.Launcher{
		ServiceDisc:   services.ServiceDiscovery, //utilenv.ServiceDiscAddr,
		Apps:          nil,
		ServerAddr:    offsetAddr(skyenv.AppSrvAddr),
		BinPath:       skyenv.AppBinPath,
		DisplayNodeIP: isDisplayNodeIP,
	}
	conf.UptimeTracker = &visorconfig.UptimeTracker{
		Addr: services.UptimeTracker, //utilenv.UptimeTrackerAddr,
	}
	if cliAddr != "" {
		conf.CLIAddr = offsetAddr(cliAddr)
	} else {
		conf.CLIAddr = offsetAddr(skyenv.RPCAddr)
	}
	conf.LogLevel = logLevel
	conf.LocalPath = localPath
	if stunServers != "" {
		conf.StunServers = strings.Split(stunServers, ",")
		for i := range conf.StunServers {
			conf.StunServers[i] = strings.TrimSpace(conf.StunServers[i])
		}
	} else {
		conf.StunServers = services.StunServers //utilenv.GetStunServers()
	}
	if shutdownTimeout != "" {
		d, err := time.ParseDuration(shutdownTimeout)
		if err != nil {
			log.WithError(err).Fatal("Failed to parse shutdown timeout")
		}
		conf.ShutdownTimeout = visorconfig.Duration(d)
	} else {
		conf.ShutdownTimeout = visorconfig.DefaultTimeout
	}
	conf.GeoIP = skyenv.GeoIP
	conf.MemoryLimit = "auto"
	if rewardSkyAddr != "" {
		canonical, _, err := rewardconfig.ValidateRewardAddress(rewardSkyAddr)
		if err != nil {
			log.WithError(err).Fatal("Invalid reward address")
		}
		conf.RewardAddress = canonical
	}

	dmsgptyAddr := dmsgpty.DefaultCLIAddr()
	if isTestEnv {
		dmsgptyAddr = filepath.Join(os.TempDir(), "dmsgpty-test.sock")
	}
	conf.Dmsgpty = &visorconfig.Dmsgpty{
		DmsgPort: skyenv.DmsgPtyPort,
		CLINet:   skyenv.DmsgPtyCLINet,
		CLIAddr:  dmsgptyAddr,
	}

	conf.STCP = &network.STCPConfig{
		ListeningAddress: offsetAddr(skyenv.STCPAddr),
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
	if !isHTTPOnly {
		// Prefer unified services config; fall back to legacy dmsgHTTPServersList
		if services.HasDmsgEndpoints() {
			// Unified config: DMSG fields from services-config.json
			conf.Dmsg.Servers = serviceDmsgServerEntries()

			if isDmsgHTTP {
				conf.Dmsg.Discovery = services.DmsgDiscoveryDmsg
				conf.Transport.AddressResolver = services.AddressResolverDmsg
				conf.Transport.Discovery = services.TransportDiscoveryDmsg
				conf.UptimeTracker.Addr = services.UptimeTrackerDmsg
				conf.Routing.RouteFinder = services.RouteFinderDmsg
				conf.Launcher.ServiceDisc = services.ServiceDiscoveryDmsg
			} else {
				conf.Dmsg.DiscoveryDmsg = services.DmsgDiscoveryDmsg
				conf.Transport.AddressResolverDmsg = services.AddressResolverDmsg
				conf.Transport.DiscoveryDmsg = services.TransportDiscoveryDmsg
				conf.Launcher.ServiceDiscDmsg = services.ServiceDiscoveryDmsg
			}
		} else if dmsgHTTPServersList != nil {
			// Legacy fallback: separate dmsghttp-config.json
			var dmsgConf *visorconfig.DmsgHTTPServersData
			if isTestEnv {
				dmsgConf = &dmsgHTTPServersList.Test
			} else {
				dmsgConf = &dmsgHTTPServersList.Prod
			}
			if dmsgConf != nil {
				conf.Dmsg.Servers = dmsgConf.DMSGServers
				if isDmsgHTTP {
					conf.Dmsg.Discovery = dmsgConf.DMSGDiscovery
					conf.Transport.AddressResolver = dmsgConf.AddressResolver
					conf.Transport.Discovery = dmsgConf.TransportDiscovery
					conf.UptimeTracker.Addr = dmsgConf.UptimeTracker
					conf.Routing.RouteFinder = dmsgConf.RouteFinder
					conf.Launcher.ServiceDisc = dmsgConf.ServiceDiscovery
				} else {
					conf.Dmsg.DiscoveryDmsg = dmsgConf.DMSGDiscovery
					conf.Transport.AddressResolverDmsg = dmsgConf.AddressResolver
					conf.Transport.DiscoveryDmsg = dmsgConf.TransportDiscovery
					conf.Launcher.ServiceDiscDmsg = dmsgConf.ServiceDiscovery
				}
			}
		}
	}

	// Configure public visor
	conf.IsPublic = isPublic
	if isPublic {
		regTimeout := visorconfig.Duration(skyenv.PublicVisorRegistrationTimeout)
		if publicVisorRegTimeout != "" {
			d, err := time.ParseDuration(publicVisorRegTimeout)
			if err != nil {
				log.WithError(err).Fatal("Failed to parse registration timeout")
			}
			regTimeout = visorconfig.Duration(d)
		}
		maxTp := visorconfig.PublicVisorMaxTransports
		if publicVisorMaxTransports > 0 {
			maxTp = publicVisorMaxTransports
		}
		conf.PublicVisorConfig = &visorconfig.PublicVisorConfig{
			RegistrationTimeout: regTimeout,
			MaxTransports:       maxTp,
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
		config := visorconfig.GenerateWorkDirConfig(isTestEnv)
		if hvHTTPAddr != "" {
			config.HTTPAddr = hvHTTPAddr
		} else {
			config.HTTPAddr = offsetAddr(config.HTTPAddr)
		}
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
	// Apply port offsets for testenv to avoid conflicts with production visor
	chatAddr := offsetAddr(skychatAddr)
	socksClientAddr := offsetAddr(skyenv.SkysocksClientAddr)

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
				AutoStart: isSkychatEnable,
				Port:      routing.Port(skyenv.SkychatPort),
				Args:      append([]string{"app", "skychat"}, "--addr", chatAddr),
			},
			{
				Name:      skyenv.SkysocksName,
				Binary:    "skywire",
				AutoStart: isProxyServerEnable,
				Port:      routing.Port(skyenv.SkysocksPort),
				Args:      []string{"app", "skysocks"},
			},
			{
				Name:      skyenv.SkysocksClientName,
				Binary:    "skywire",
				AutoStart: false,
				Port:      routing.Port(skyenv.SkysocksClientPort),
				Args:      append([]string{"app", "skysocks-client"}, "--addr", socksClientAddr),
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
				AutoStart: isSkychatEnable,
				Port:      routing.Port(skyenv.SkychatPort),
				Args:      []string{"--addr", chatAddr},
			},
			{
				Name:      skyenv.SkysocksName,
				AutoStart: isProxyServerEnable,
				Port:      routing.Port(skyenv.SkysocksPort),
				Args:      []string{},
			},
			{
				Name:      skyenv.SkysocksClientName,
				AutoStart: false,
				Port:      routing.Port(skyenv.SkysocksClientPort),
				Args:      []string{"--addr", socksClientAddr},
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
			// Re-indent for readable file output
			var data any
			if err = json.Unmarshal(jsonData, &data); err == nil {
				if indented, indentErr := json.MarshalIndent(data, "", "    "); indentErr == nil {
					jsonData = indented
				}
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
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}
		if netutil.IsVirtualInterface(iface.Name) {
			continue
		}
		interfaceNames = append(interfaceNames, iface.Name)
		if iface.Index == 0 && defaultInterface == "" {
			defaultInterface = iface.Name
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

// applyFlagsToConf uncomments settings in the conf template that correspond
// to flags passed on the command line. This makes -q/-Q output reflect the
// user's intent (e.g., `config gen -iq` uncomments ISHYPERVISOR=true).
func applyFlagsToConf(conf string, cmd *cobra.Command) string {
	// Map of flag names to the SKYENV variable they control.
	// When a flag is explicitly set, uncomment the corresponding line.
	flagToEnv := map[string]string{
		"pkg":       "PKGENV=true",
		"bestproto": "BESTPROTO=true",
		"ishv":      "ISHYPERVISOR=true",
		"testenv":   "TESTENV=true",
		"dmsghttp":  "DMSGHTTP=true",
		"public":    "VISORISPUBLIC=true",
		"servevpn":  "VPNSERVER=true",
		"autoconn":  "DISABLEPUBLICAUTOCONN=true",
		"publicip":  "DISPLAYNODEIP=true",
	}

	// Also handle string/value flags
	valueFlagToEnv := map[string]string{
		"cliaddr": "CLIADDR",
		"hvaddr":  "HVHTTPADDR",
		"timeout": "SHUTDOWNTIMEOUT",
		"reward":  "REWARDSKYADDR",
	}

	lines := strings.Split(conf, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Check boolean flags
		for flagName, envSetting := range flagToEnv {
			if cmd.Flags().Changed(flagName) {
				commented := "#" + envSetting
				if strings.HasPrefix(trimmed, commented) || trimmed == commented {
					lines[i] = strings.Replace(line, "#"+envSetting, envSetting, 1)
				}
			}
		}
		// Check value flags
		for flagName, envKey := range valueFlagToEnv {
			if cmd.Flags().Changed(flagName) {
				val, _ := cmd.Flags().GetString(flagName) //nolint:errcheck
				if val != "" && strings.Contains(trimmed, envKey) && strings.HasPrefix(trimmed, "#") {
					lines[i] = envKey + "='" + val + "'"
				}
			}
		}
	}
	return strings.Join(lines, "\n")
}
