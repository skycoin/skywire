// Package cliconfig cmd/skywire-cli/commands/config/root.go c4-vis-cli
package cliconfig

import (
	"strconv"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var logger = logging.MustGetLogger("skywire-cli")

var (
	conf                = new(visorconfig.V1)
	dmsgHTTPServersList = &visorconfig.DmsgHTTPServers{
		Prod: visorconfig.DmsgHTTPServersData{DMSGServers: []*disc.Entry{}},
	}
	path                       string
	noFetch                    bool
	noDefaults                 bool
	stcprPort                  int
	sudphPort                  int
	transportPort              int
	genMinHops                 int
	arTransportLimit           int
	noDirectTransports         bool
	noDmsgServerTransports     bool
	ptyRPCExec                 bool
	sk                         cipher.SecKey
	output                     string
	confPath                   string
	configName                 string //nolint Note: configName used, but golangci-lint marked it unused in wrong
	stdout                     bool
	squash                     bool
	regenerate                 bool
	retainHypervisors          bool
	pText                      string
	pkgEnv                     bool
	usrEnv                     bool
	hypervisor                 bool
	hvDeskAddr                 string
	hypervisorPKs              string
	dmsgptyWlPKs               string
	surveyWhitelistPKs         string
	routeSetupNodes            string
	transportSetupPKs          string
	dmsgHTTP                   bool
	minDmsgSess                int
	vpnServerEnable            bool
	vpnRouterEnable            bool
	vpnRouterLanIfc            string
	vpnRouterSubnet            string
	vpnRouterWifi              bool
	vpnRouterSSID              string
	vpnRouterPassphrase        string
	vpnRouterBand              string
	vpnRouterChannel           int
	vpnRouterCountry           string
	vpnRouterOpenWiFi          bool
	vpnRouterMeshGW            bool
	vpnRouterMeshGWCIDR        string
	vpnRouterMeshTLS           bool
	disableAuth                bool
	enableAuth                 bool
	hvAuthEnv                  string
	enablePKEndpoint           bool
	selectedOS                 string
	disableApps                string
	serviceConfURL             = deployment.ProdConf.Conf
	dnsServer                  = "1.1.1.1"
	services                   visorconfig.Services
	servicesConfig             servicesConf
	force                      bool
	hide                       bool
	walletCustody              string
	walletDir                  string
	walletRemotePK             string
	noWallet                   bool
	all                        bool
	outUnset                   bool
	ver                        string
	root                       = visorconfig.IsRoot()
	gHiddenFlags               []string
	uHiddenFlags               []string
	binPath                    string
	logLevel                   string
	pkg                        bool
	input                      string
	updateEndpoints            bool
	addHypervisorPKs           string
	resetHypervisor            bool
	setVPNClientKillswitch     string
	addVPNClientSrv            string
	resetVPNclient             bool
	addVPNServerWhitelist      string
	setVPNServerSecure         string
	setVPNServerAutostart      string
	setVPNServerNetIfc         string
	resetVPNServer             bool
	addSkysocksClientSrv       string
	resetSkysocksClient        bool
	skysocksWhitelist          string
	resetSkysocks              bool
	setPublicAutoconnect       string
	minHops                    int
	cascadeRouteSetup          bool
	policyPerDial              string
	usr                        bool
	public                     bool
	disablePublicAutoConn      bool
	displayNodeIP              bool
	addExampleApps             bool
	enableProxyClientAutostart bool
	proxyServerEnable          bool
	proxyServerWhitelist       string
	enableDmsgWeb              bool
	enableSkynetWeb            bool
	wispUpstreamSOCKS          string
	enableSkymailBridge        bool
	dmsgWebUpstreamSOCKS       string
	skynetWebUpstreamSOCKS     string
	dmsgWebProxyAddr           string
	skynetWebProxyAddr         string
	extraResolvers             string
	noBrowseOrigin             bool
	browseOriginSuffix         string
	browseOriginTLSCert        string
	browseOriginTLSKey         string
	configServicePath          string
	dmsgHTTPPath               string
	snConfig                   bool
	dmsgDiscConfig             bool
	dmsgSrvConfig              bool
	tpdConfig                  bool
	sdConfig                   bool
	arConfig                   bool
	rfConfig                   bool
	externalApps               bool
	enableCalculateRoutes      bool
	logHookLevel               string
	logServerAddr              string
	ptySSHListen               string
	ptyPersist                 bool
	noDmsgscp                  bool
	dmsgscpRoot                string
	noStats                    bool
	statsRetention             int
	memoryLimit                string
	autoconnMaxTotal           int
	autoconnSiteLinks          int
	autoconnMaxPerType         string
	enableFlightRecorder       bool
	enableMuxFEC               bool
	hvUIDisable                bool
	skychatEnable              bool
	skychatAddr                string
	// skychatPairEnable adds --pair-enable to the skychat app args
	// in the generated config. Defaults to true; toggle via SKYCHATPAIR
	// in /etc/skywire.conf or --servechatpair=false on the CLI. The
	// pair-RPC channel is required for group chat (#2580 federated send
	// and #2585 admin mirror) to actually propagate messages, so this
	// should rarely be off in practice.
	skychatPairEnable bool
	// skychatPortless drops --chataddr from the generated internal-apps
	// config and emits --portless instead: skychat then binds no TCP port
	// and its UI is reachable only through the hypervisor's control
	// surface. Off by default — a chat app whose window you cannot open
	// without the hypervisor is a deployment choice, not a starting point.
	// Toggle via SKYCHATPORTLESS in /etc/skywire.conf or --chatportless.
	// Ignored under --extapps, where skychat is a separate process and has
	// nothing in-process to publish to.
	skychatPortless bool
	// Skycoin embedded apps — daemon (full node) + web (thin-client
	// wallet). Default-off for both. SkycoinWebUser drops the wallet
	// process to a different UID via the launcher's per-app
	// credential field; left empty means the wallet runs as the
	// visor's own UID.
	skycoinDaemonEnable  bool
	skycoinDaemonFiber   string // legacy single-instance FIBER_TOML — superseded by skycoinDaemonInstances when that's non-empty
	skycoinDaemonAPISets string // legacy single-instance API sets — superseded by skycoinDaemonFlags when that's non-empty
	skycoinDaemonUser    string
	// skycoinDaemonInstances is the new multi-instance knob. Each
	// entry is either the literal "skycoin" (built-in defaults, no
	// FIBER_TOML override) or a path to a fiber.toml. Each entry
	// produces one skycoin-daemon-<name> AppConfig with auto-
	// allocated --port and --data-dir; <name> = "skycoin" for the
	// literal, basename-without-.toml for paths.
	skycoinDaemonInstances string
	// skycoinDaemonFlags are extra flags appended to every
	// instance's args (e.g. "--enable-gui-api-sets READ,STATUS").
	// --port and --data-dir are auto-allocated per instance and
	// must not appear here — gen errors out if they do.
	skycoinDaemonFlags  string
	skycoinWebEnable    bool
	skycoinWebAddr      string
	skycoinWebNodeURLs  string // comma-separated; rendered as repeated --node-url flags
	skycoinWebWalletDir string
	skycoinWebElectrum  string // --btc-electrum-url; empty leaves skycoin-web on its built-in list
	skycoinWebUser      string
	// coinNodes advertises already-running fibercoin nodes over dmsg +
	// service-discovery (servicedisc.ServiceTypeCoin). CSV of
	// "<local_addr>[@<dmsg_port>]" specs; each becomes a
	// visorconfig.CoinNodeConfig the visor forwards + health-gates. The
	// node runs INDEPENDENTLY (it need not be a skywire-managed daemon
	// app), so this is orthogonal to SKYCOIND. Driven by ${COIN_NODES[@]}.
	coinNodes                string
	rewardSkyAddr            string
	alertEmail               string
	hvHTTPAddr               string
	stunServers              string
	shutdownTimeout          string
	publicVisorRegTimeout    string
	publicVisorMaxTransports int
	cliAddr                  string
	// Hypervisor-embedded DMSG server (LAN/WAN). Operator-set knobs:
	// port and an optional public address advertised to remote
	// visors. Port should be pinned for stable WAN reachability since
	// the OS-assigned port (when 0) changes on every restart. The
	// "enable" boolean is implicit — the LAN-DMSG server is always
	// on whenever ISHYPERVISOR=true.
	lanDmsgPort          int
	lanDmsgPublicAddress string
	dmsgServerConf       string
	dmsgServerOwnKey     bool
	dmsgServerPublicAddr string
	dmsgServerWSTLSAddr  string
	dmsgRelayAddr        string
	dmsgRelayKeys        string
	dmsgWebSecretKey     string
	resolverCA           string
	skyDNSEnable         bool
	noDmsgRelay          bool
)

// RootCmd contains commands that interact with the config of local skywire-visor
var RootCmd = &cobra.Command{
	Use:   "config",
	Short: "Generate or update a skywire config",
	Long:  "Generate or update the config file used by skywire-visor.",
}

type servicesConf struct {
	Prod visorconfig.Services `json:"prod"`
}

// Thin wrappers around the shared skyenv helpers in cmdutil.
// These pass the package-level skyenvfile to the shared functions.

func scriptExecString(s string) string { return cmdutil.SkyenvString(s, skyenvfile) }
func scriptExecBool(s string) bool     { return cmdutil.SkyenvBool(s, skyenvfile) }
func scriptExecArray(s string) string  { return cmdutil.SkyenvArray(s, skyenvfile) }
func scriptExecInt(s string) int       { return cmdutil.SkyenvInt(s, skyenvfile) }
func parseDefault(s string) string     { return cmdutil.SkyenvDefault(s) } //nolint:unused

// vpnRouterArgs builds the vpn-router launcher args from the config-gen knobs,
// so every supported topology — ethernet-out or WiFi-out (hostapd AP), with an
// optional custom subnet — is configurable via skywire.conf / `config gen`,
// not just the downstream interface. prefix is the invocation prefix
// (["app","vpn-router"] for the external app entry, nil for the internal one).
// Empty knobs are omitted so the app's own defaults apply.
func vpnRouterArgs(prefix []string) []string {
	args := append([]string{}, prefix...)
	if vpnRouterLanIfc != "" {
		args = append(args, "--lan-ifc", vpnRouterLanIfc)
	}
	if vpnRouterSubnet != "" {
		args = append(args, "--subnet", vpnRouterSubnet)
	}
	if vpnRouterWifi {
		args = append(args, "--wifi")
		if vpnRouterSSID != "" {
			args = append(args, "--ssid", vpnRouterSSID)
		}
		if vpnRouterPassphrase != "" {
			args = append(args, "--passphrase", vpnRouterPassphrase)
		}
		if vpnRouterBand != "" {
			args = append(args, "--band", vpnRouterBand)
		}
		if vpnRouterChannel != 0 {
			args = append(args, "--channel", strconv.Itoa(vpnRouterChannel))
		}
		if vpnRouterCountry != "" {
			args = append(args, "--country", vpnRouterCountry)
		}
		if vpnRouterOpenWiFi {
			args = append(args, "--open")
		}
	}
	if vpnRouterMeshGW {
		args = append(args, "--mesh-gateway")
		if vpnRouterMeshGWCIDR != "" {
			args = append(args, "--mesh-gateway-cidr", vpnRouterMeshGWCIDR)
		}
		if vpnRouterMeshTLS {
			args = append(args, "--mesh-gateway-tls")
		}
	}
	return args
}
