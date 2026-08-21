// Package cliconfig cmd/skywire-cli/commands/config/gen.go c4-vis-cli
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
	coinCipher "github.com/skycoin/skycoin/src/cipher"
	"github.com/spf13/cobra"
	"github.com/wlynxg/anet"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cliout"
	"github.com/skycoin/skywire/pkg/cliout/cliconfig"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgclient"
	"github.com/skycoin/skywire/pkg/dmsgc"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/netutil"
	"github.com/skycoin/skywire/pkg/pty"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
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
	Run: func(cmd *cobra.Command, _ []string) {
		pk, sk := cipher.GenerateKeyPair()
		internal.Catch(cmd.Flags(), cliout.Print(cmd, cliconfig.Keypair{
			PublicKey: pk.String(), SecretKey: sk.String(),
		}))
	},
}

var pkFromSKCmd = &cobra.Command{
	Use:   "pk <secret-key-hex>",
	Short: "derive public key from a secret key",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
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
		internal.Catch(cmd.Flags(), cliout.Print(cmd, cliconfig.Keypair{PublicKey: pk.Hex()}))
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
	genConfigCmd.Flags().BoolVarP(&isHide, "hide", "w", false, "suppress config output to terminal")
	genConfigCmd.Flags().StringVar(&walletCustody, "wallet-custody", "", "where the skycoin-web wallet keeps keys: browser|disk|remote")
	genConfigCmd.Flags().StringVar(&walletDir, "wallet-dir", "", "wallet dir for --wallet-custody=disk (a seed-wallet store, not per-coin)")
	genConfigCmd.Flags().StringVar(&walletRemotePK, "wallet-remote", "", "visor PK holding the wallets, for --wallet-custody=remote")
	genConfigCmd.Flags().BoolVar(&noWallet, "no-wallet", false, "do not serve the skycoin-web wallet frontend")
	gHiddenFlags = append(gHiddenFlags, "hide")
	genConfigCmd.Flags().BoolVarP(&isEnvs, "envs", "q", false, "show the conf template (reflects flags passed)")
	genConfigCmd.Flags().StringVarP(&envfileOut, "envout", "Q", "", "write conf template to file (reflects flags passed)")

	// Config generation flags
	genConfigCmd.Flags().BoolVarP(&isForce, "force", "f", false, "remove pre-existing config")
	gHiddenFlags = append(gHiddenFlags, "force")
	genConfigCmd.Flags().BoolVarP(&isRegen, "regen", "r", false, "regenerate existing config and retain keys")
	genConfigCmd.Flags().BoolVarP(&isRetainHypervisors, "retainhv", "x", false, "retain existing hypervisors with regen")
	gHiddenFlags = append(gHiddenFlags, "retainhv")

	// Network and deployment flags
	genConfigCmd.Flags().StringVarP(&serviceConfURL, "url", "a", scriptExecArray(fmt.Sprintf("${SVCCONFADDR[@]-%s}", serviceConfURL)), "services conf url\n\r")
	gHiddenFlags = append(gHiddenFlags, "url")
	genConfigCmd.Flags().BoolVarP(&isTestEnv, "testenv", "t", scriptExecBool("${TESTENV:-false}"), "use test deployment (ports offset +10000 from prod)")
	gHiddenFlags = append(gHiddenFlags, "testenv")
	// Deployment services are dmsg-only. --dmsghttp is the (default) behavior and
	// kept as a hidden no-op for back-compat; the former --http / --dual modes are
	// gone — plain HTTP to deployment services is no longer supported.
	genConfigCmd.Flags().BoolVarP(&isDmsgHTTP, "dmsghttp", "d", scriptExecBool("${DMSGHTTP:-false}"), "use only dmsg for skywire services, no http (this is the default)")
	gHiddenFlags = append(gHiddenFlags, "dmsghttp")
	genConfigCmd.Flags().StringVarP(&dmsgHTTPPath, "dmsgconf", "D", scriptExecString("${DMSGCONF}"), "dmsghttp config path")
	gHiddenFlags = append(gHiddenFlags, "dmsgconf")
	// Note: the historical `BESTPROTO=true` knob was a China-only
	// dmsghttp-fallback path that fired only when ipinfo.io reported
	// country=CN. The default dmsghttp+http fallback in service
	// resolution covers that case everywhere now, so the knob is
	// vestigial and removed in this revision. Operators who actually
	// need the China-only behavior should set DMSGHTTP=true +
	// DISABLEPUBLICAUTOCONN=true directly.
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
	genConfigCmd.Flags().IntVar(&stcprPort, "stcpr", scriptExecInt("${STCPRPORT:-0}"), "tcp transport listening port (0 = random / shared master port)")
	gHiddenFlags = append(gHiddenFlags, "stcpr")
	genConfigCmd.Flags().IntVar(&sudphPort, "sudph", scriptExecInt("${SUDPHPORT:-0}"), "udp transport listening port (0 = random / shared master port)")
	gHiddenFlags = append(gHiddenFlags, "sudph")
	// Unified master transport port: one number carrying every transport type
	// (stcpr+WS on <port>/tcp, quic+sudph+wt+webrtc on <port>/udp, demuxed by
	// protocol). Per-type ports above override it. Visible because this is the
	// recommended way to expose a public visor behind a single firewall rule.
	genConfigCmd.Flags().IntVar(&transportPort, "transport-port", scriptExecInt("${TRANSPORTPORT:-0}"), "shared master transport port for all transport types (0 = per-type ports)")
	genConfigCmd.Flags().IntVar(&genMinHops, "min-hops", scriptExecInt("${MINHOPS:-1}"), "minimum route hops (1 = allow direct 1-hop routes, >=2 = force multihop through intermediaries for sender privacy)")
	genConfigCmd.Flags().IntVar(&arTransportLimit, "ar-transport-limit", scriptExecInt("${ARTRANSPORTLIMIT:-0}"), "address-resolver registration: 0 = stay registered, N>0 = deregister after N transports, N<0 = never register (inbound-invisible)")
	genConfigCmd.Flags().BoolVar(&noDirectTransports, "no-direct-transports", scriptExecBool("${NODIRECTTRANSPORTS:-false}"), "never create direct p2p transports; dmsg (relay) is still allowed")
	genConfigCmd.Flags().BoolVar(&ptyRPCExec, "pty-rpc-exec", scriptExecBool("${PTYRPCEXEC:-false}"), "allow visor-RPC-initiated dmsgpty exec (control/jump node opt-in; off = closes a local privilege-escalation vector)")

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
	genConfigCmd.Flags().BoolVar(&cascadeRouteSetup, "cascade", false, "opt into source-driven cascade route setup (default: legacy setup-node path)")
	genConfigCmd.Flags().BoolVar(&snConfig, "sn", false, "generate config for route setup node")
	gHiddenFlags = append(gHiddenFlags, "sn")
	genConfigCmd.Flags().BoolVar(&dmsgDiscConfig, "dmsgdisc", false, "generate config for dmsg-discovery service")
	gHiddenFlags = append(gHiddenFlags, "dmsgdisc")
	genConfigCmd.Flags().BoolVar(&dmsgSrvConfig, "dmsgsrv", false, "generate config for dmsg-server service")
	gHiddenFlags = append(gHiddenFlags, "dmsgsrv")
	genConfigCmd.Flags().BoolVar(&tpdConfig, "tpd", false, "generate config for transport-discovery service")
	gHiddenFlags = append(gHiddenFlags, "tpd")
	genConfigCmd.Flags().BoolVar(&sdConfig, "sd", false, "generate config for service-discovery service")
	gHiddenFlags = append(gHiddenFlags, "sd")
	genConfigCmd.Flags().BoolVar(&arConfig, "ar", false, "generate config for address-resolver service")
	gHiddenFlags = append(gHiddenFlags, "ar")
	genConfigCmd.Flags().BoolVar(&rfConfig, "rf", false, "generate config for route-finder service")
	gHiddenFlags = append(gHiddenFlags, "rf")
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
	genConfigCmd.Flags().BoolVar(&isEnablePKEndpoint, "pk-endpoint", scriptExecBool("${ENABLEPKENDPOINT:-false}"), "expose unauthenticated GET /api/pk on the hypervisor (skybian / Arch-ARM image builds set this)")
	gHiddenFlags = append(gHiddenFlags, "pk-endpoint")

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
	genConfigCmd.Flags().BoolVarP(&isVpnServerEnable, "servevpn", "v", scriptExecBool("${VPNSERVER:-true}"), "autostart vpn server")
	genConfigCmd.Flags().BoolVar(&isVpnRouterEnable, "servevpnrouter", scriptExecBool("${VPNROUTER:-false}"), "autostart the vpn router (LAN/WiFi gateway that NATs into the vpn-client tunnel; needs --vpnrouter-lan-ifc)")
	genConfigCmd.Flags().StringVar(&vpnRouterLanIfc, "vpnrouter-lan-ifc", scriptExecString("${VPNROUTERLANIFC}"), "downstream LAN/WiFi interface the vpn router serves (e.g. eth1 or wlan0)")
	genConfigCmd.Flags().StringVar(&vpnRouterSubnet, "vpnrouter-subnet", scriptExecString("${VPNROUTERSUBNET}"), "vpn router gateway+subnet as <gateway-ip>/<prefix> (default 192.168.42.1/24)")
	genConfigCmd.Flags().BoolVar(&vpnRouterWifi, "vpnrouter-wifi", scriptExecBool("${VPNROUTERWIFI:-false}"), "vpn router WiFi-out: run hostapd (AP) on the downstream interface")
	genConfigCmd.Flags().StringVar(&vpnRouterSSID, "vpnrouter-ssid", scriptExecString("${VPNROUTERSSID}"), "vpn router WiFi SSID (with --vpnrouter-wifi)")
	genConfigCmd.Flags().StringVar(&vpnRouterPassphrase, "vpnrouter-passphrase", scriptExecString("${VPNROUTERPASSPHRASE}"), "vpn router WiFi WPA2 passphrase, 8–63 chars (with --vpnrouter-wifi)")
	genConfigCmd.Flags().StringVar(&vpnRouterBand, "vpnrouter-band", scriptExecString("${VPNROUTERBAND}"), "vpn router WiFi band: 2.4 or 5 (default 2.4)")
	genConfigCmd.Flags().IntVar(&vpnRouterChannel, "vpnrouter-channel", scriptExecInt("${VPNROUTERCHANNEL:-0}"), "vpn router WiFi channel (0 = default for the band)")
	genConfigCmd.Flags().StringVar(&vpnRouterCountry, "vpnrouter-country", scriptExecString("${VPNROUTERCOUNTRY}"), "vpn router WiFi regulatory country code (default US)")
	genConfigCmd.Flags().BoolVar(&vpnRouterOpenWiFi, "vpnrouter-open", scriptExecBool("${VPNROUTEROPEN:-false}"), "vpn router WiFi: allow an open (passphrase-less) network")
	genConfigCmd.Flags().BoolVar(&vpnRouterMeshGW, "vpnrouter-mesh-gateway", scriptExecBool("${VPNROUTERMESHGW:-false}"), "vpn router: also act as a mesh gateway (resolve *.dmsg / *.skynet for clients)")
	genConfigCmd.Flags().StringVar(&vpnRouterMeshGWCIDR, "vpnrouter-mesh-gateway-cidr", scriptExecString("${VPNROUTERMESHGWCIDR}"), "vpn router mesh-gateway synthetic-IP pool (default 100.64.0.0/16)")
	genConfigCmd.Flags().BoolVar(&vpnRouterMeshTLS, "vpnrouter-mesh-gateway-tls", scriptExecBool("${VPNROUTERMESHTLS:-false}"), "vpn router mesh gateway: TLS-MITM HTTPS to *.dmsg/*.skynet (clients must trust the generated CA)")
	gHiddenFlags = append(gHiddenFlags, "servevpn")

	// VPN flags
	// VPN client killswitch is handled as string for cobra flag compatibility.
	genConfigCmd.Flags().StringVar(&setVPNClientKillswitch, "killsw", scriptExecString("${VPNKS}"), "vpn client killswitch")
	gHiddenFlags = append(gHiddenFlags, "killsw")
	genConfigCmd.Flags().StringVar(&addVPNClientSrv, "addvpn", scriptExecString("${ADDVPNPK}"), "set vpn server public key for vpn client")
	gHiddenFlags = append(gHiddenFlags, "addvpn")
	genConfigCmd.Flags().StringVar(&addVPNServerWhitelist, "vpnwl", scriptExecArray("${VPNSERVERWL[@]}"), "vpn server whitelist (comma separated; empty allows all)")
	genConfigCmd.Flags().StringVar(&setVPNServerSecure, "secure", scriptExecString("${VPNSEVERSECURE}"), "change secure mode status of vpn server")
	gHiddenFlags = append(gHiddenFlags, "secure")
	genConfigCmd.Flags().StringVar(&setVPNServerNetIfc, "netifc", scriptExecString("${VPNSEVERNETIFC}"), "VPN Server network interface (detected: "+getInterfaceNames()+")")
	gHiddenFlags = append(gHiddenFlags, "netifc")

	// Proxy flags
	genConfigCmd.Flags().StringVar(&addSkysocksClientSrv, "proxyclientpk", scriptExecString("${PROXYCLIENTPK}"), "set server public key for proxy client")
	gHiddenFlags = append(gHiddenFlags, "proxyclientpk")
	genConfigCmd.Flags().BoolVar(&enableProxyClientAutostart, "startproxyclient", scriptExecBool("${STARTPROXYCLIENT:-false}"), "autostart proxy client")
	gHiddenFlags = append(gHiddenFlags, "startproxyclient")
	genConfigCmd.Flags().BoolVar(&isProxyServerEnable, "serveproxy", scriptExecBool("${PROXYSERVER:-true}"), "autostart proxy server")
	genConfigCmd.Flags().StringVar(&proxyServerWhitelist, "proxywl", scriptExecArray("${PROXYSERVERWL[@]}"), "proxy server whitelist (comma separated; empty allows all)")

	// Embedded resolving-proxy flags. Browsers point a single SOCKS5
	// at the dmsgweb listener and `.dmsg` / `.skynet` URLs resolve.
	// When both are enabled, the runtime auto-chains dmsgweb's
	// upstream → skynetweb so one entry covers both TLDs.
	genConfigCmd.Flags().BoolVar(&enableDmsgWeb, "dmsgweb", scriptExecBool("${DMSGWEB:-false}"), "enable embedded .dmsg resolving SOCKS5 proxy on 127.0.0.1:4445")
	genConfigCmd.Flags().BoolVar(&enableSkynetWeb, "skynetweb", scriptExecBool("${SKYNETWEB:-false}"), "enable embedded .skynet resolving SOCKS5 proxy on 127.0.0.1:4446")
	genConfigCmd.Flags().BoolVar(&enableSkymailBridge, "skymail-bridge", scriptExecBool("${SKYMAILBRIDGE:-false}"), "enable SMTP to skywire bridge on 127.0.0.1:1025")
	genConfigCmd.Flags().StringVar(&dmsgWebUpstreamSOCKS, "dmsgweb-upstream", scriptExecString("${DMSGWEBUPSTREAM}"), "upstream SOCKS5 for non .dmsg traffic (empty chains to skynetweb)")
	gHiddenFlags = append(gHiddenFlags, "dmsgweb-upstream")
	genConfigCmd.Flags().StringVar(&skynetWebUpstreamSOCKS, "skynetweb-upstream", scriptExecString("${SKYNETWEBUPSTREAM}"), "upstream SOCKS5 for non .skynet traffic")
	gHiddenFlags = append(gHiddenFlags, "skynetweb-upstream")
	genConfigCmd.Flags().StringVar(&dmsgWebProxyAddr, "dmsgweb-addr", scriptExecString("${DMSGWEBADDR}"), "host the .dmsg SOCKS5 proxy binds to (empty=127.0.0.1; 0.0.0.0 or a LAN IP to serve the LAN)")
	gHiddenFlags = append(gHiddenFlags, "dmsgweb-addr")
	genConfigCmd.Flags().StringVar(&skynetWebProxyAddr, "skynetweb-addr", scriptExecString("${SKYNETWEBADDR}"), "host the .skynet SOCKS5 proxy binds to (empty=127.0.0.1; 0.0.0.0 or a LAN IP to serve the LAN)")
	gHiddenFlags = append(gHiddenFlags, "skynetweb-addr")

	// Skychat flags
	genConfigCmd.Flags().BoolVar(&isSkychatEnable, "servechat", scriptExecBool("${SKYCHAT:-true}"), "autostart skychat")
	genConfigCmd.Flags().StringVar(&skychatAddr, "chataddr", scriptExecString("${SKYCHATADDR:-"+skyenv.SkychatAddr+"}"), "skychat local address")
	gHiddenFlags = append(gHiddenFlags, "chataddr")
	genConfigCmd.Flags().BoolVar(&isSkychatPairEnable, "servechatpair", scriptExecBool("${SKYCHATPAIR:-true}"), "skychat pair RPC channel (required for group chat)")
	gHiddenFlags = append(gHiddenFlags, "servechatpair")
	// Visible, unlike the other two: it is the one skychat knob that takes
	// something away (the local UI on --chataddr), so an operator has to be
	// able to find it without reading the source.
	genConfigCmd.Flags().BoolVar(&isSkychatPortless, "chatportless", scriptExecBool("${SKYCHATPORTLESS:-false}"), "run skychat with no TCP port; reach its UI only through the hypervisor")

	// Skycoin embedded apps. Default-off for both — operator opts in
	// per-app via these flags or via SKYENV. The wallet's user-drop
	// (SKYCOINWEBUSER) is the security-critical knob: the wallet
	// touches the operator's ~/.skycoin/wallets dir, so when the
	// visor itself runs as _skywire the wallet should be configured
	// to drop to the operator's UID.
	genConfigCmd.Flags().BoolVar(&isSkycoinDaemonEnable, "skycoind", scriptExecBool("${SKYCOIND:-false}"), "autostart skycoin daemon (legacy single instance)")
	genConfigCmd.Flags().StringVar(&skycoinDaemonFiber, "skycoindfiber", scriptExecString("${SKYCOIND_FIBER_TOML}"), "legacy FIBER_TOML path (single instance)")
	gHiddenFlags = append(gHiddenFlags, "skycoindfiber")
	genConfigCmd.Flags().StringVar(&skycoinDaemonAPISets, "skycoindapi", scriptExecString("${SKYCOIND_API_SETS}"), "legacy api sets (single instance)")
	gHiddenFlags = append(gHiddenFlags, "skycoindapi")
	genConfigCmd.Flags().StringVar(&skycoinDaemonUser, "skycoindUSER", scriptExecString("${SKYCOIND_USER}"), "skycoin daemon UID (applies to every instance)")
	gHiddenFlags = append(gHiddenFlags, "skycoindUSER")
	// Multi-instance: SKYCOIND_INSTANCES is a bash array. Entries
	// are either the literal "skycoin" (built-in defaults, no
	// FIBER_TOML override) or a path to a fiber.toml. scriptExecArray
	// expands ${VAR[@]} into a CSV string here; gen splits on commas
	// at render time and emits one AppConfig per entry with auto-
	// allocated --port (base 6420 +2N) and --data-dir.
	genConfigCmd.Flags().StringVar(&skycoinDaemonInstances, "skycoindinstances", scriptExecArray("${SKYCOIND_INSTANCES[@]}"), "skycoin daemon instances (comma separated; skycoin or fiber.toml path)")
	gHiddenFlags = append(gHiddenFlags, "skycoindinstances")
	genConfigCmd.Flags().StringVar(&skycoinDaemonFlags, "skycoindflags", scriptExecString("${SKYCOIND_FLAGS}"), "extra flags appended to every skycoin daemon (port and data dir auto allocated)")
	gHiddenFlags = append(gHiddenFlags, "skycoindflags")
	genConfigCmd.Flags().StringVar(&coinNodes, "coin-nodes", scriptExecArray("${COIN_NODES[@]}"), "fibercoin nodes to forward over dmsg + advertise (type=coin); CSV of local_addr[@dmsg_port]")
	gHiddenFlags = append(gHiddenFlags, "coin-nodes")
	genConfigCmd.Flags().BoolVar(&isSkycoinWebEnable, "skycoinweb", scriptExecBool("${SKYCOINWEB:-false}"), "autostart skycoin web wallet (thin client)")
	// 8002 to avoid colliding with skychat's default at 127.0.0.1:8001.
	// Both apps' upstream defaults happen to be 8001; skychat got there
	// first in skywire so skycoin-web shifts up by one. Operator can
	// override via SKYCOINWEBADDR or the universal settings panel.
	genConfigCmd.Flags().StringVar(&skycoinWebAddr, "skycoinwebaddr", scriptExecString("${SKYCOINWEBADDR:-127.0.0.1:8002}"), "skycoin web bind address (host:port)")
	gHiddenFlags = append(gHiddenFlags, "skycoinwebaddr")
	// SKYCOINWEBNODES is a bash array (multiple node URLs supported
	// — one per fibercoin the wallet is meant to multi-coin-browse).
	// scriptExecArray expands ${VAR[@]} into a CSV string here; we
	// re-split on commas at AppConfig render time and emit repeated
	// --node-url flags as skycoin-web's StringArrayVar expects.
	genConfigCmd.Flags().StringVar(&skycoinWebNodeURLs, "skycoinwebnodes", scriptExecArray("${SKYCOINWEBNODES[@]}"), "node URLs the skycoin web wallet talks to (comma separated)")
	gHiddenFlags = append(gHiddenFlags, "skycoinwebnodes")
	genConfigCmd.Flags().StringVar(&skycoinWebWalletDir, "skycoinwebwallet", scriptExecString("${SKYCOINWEBWALLET}"), "skycoin web wallet dir override")
	gHiddenFlags = append(gHiddenFlags, "skycoinwebwallet")
	genConfigCmd.Flags().StringVar(&skycoinWebUser, "skycoinwebuser", scriptExecString("${SKYCOINWEBUSER}"), "skycoin web UID (empty inherits visor UID)")
	gHiddenFlags = append(gHiddenFlags, "skycoinwebuser")

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
	genConfigCmd.Flags().IntVar(&publicVisorMaxTransports, "maxtransports", scriptExecInt("${MAXTRANSPORTS:-0}"), "public visor max transports")
	gHiddenFlags = append(gHiddenFlags, "maxtransports")
	genConfigCmd.Flags().IntVar(&muxRoutes, "muxroutes", 0, "number of parallel mux routes per connection")
	gHiddenFlags = append(gHiddenFlags, "muxroutes")
	genConfigCmd.Flags().StringVar(&cliAddr, "cliaddr", scriptExecString("${CLIADDR}"), "CLI RPC address (e.g. 0.0.0.0:3435 for Docker)")
	gHiddenFlags = append(gHiddenFlags, "cliaddr")

	// Embedded LAN/WAN DMSG server. Always on whenever ISHYPERVISOR=true
	// — "I'm a hypervisor" implies "I provide DMSG to my managed
	// visors". The previous LANDMSG=true toggle was redundant in the
	// 99% case (operators set it precisely because they had also set
	// ISHYPERVISOR=true) and confusing in the 1% case (HA pair where
	// the use case for one-but-not-the-other turned out to be
	// imaginary — running two LAN-DMSG servers across two hypervisors
	// is fine, managed visors just see two relay options).
	//
	// Operators who actually want hypervisor-without-LAN-DMSG can
	// hand-edit /opt/skywire/skywire.json to drop the
	// hypervisor.lan_dmsg_server.enable boolean.
	genConfigCmd.Flags().IntVar(&lanDmsgPort, "lan-dmsg-port", scriptExecInt("${LANDMSGPORT:-0}"), "embedded DMSG server TCP port (0 = OS-assigned at runtime; pin via LANDMSGPORT for stable WAN reachability)")
	gHiddenFlags = append(gHiddenFlags, "lan-dmsg-port")
	genConfigCmd.Flags().StringVar(&lanDmsgPublicAddress, "lan-dmsg-public", scriptExecString("${LANDMSGPUBLIC}"), "embedded DMSG server WAN-reachable address (host:port; requires port-forward)")
	gHiddenFlags = append(gHiddenFlags, "lan-dmsg-public")

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
					owner = strings.TrimSpace(owner)
					rootOwner = strings.TrimSpace(rootOwner)
					// The package case (-p, /opt/skywire owned by the
					// 'skywire' sysusers user) intentionally has root
					// writing into a non-root-owned directory — the
					// daemon either runs as root or `skywire autoconfig`
					// chowns the install to SKYWIRE_USER afterwards.
					// The old unconditional warn fired on every
					// `sudo skywire autoconfig`, masquerading
					// expected behavior as a problem. Suppress it for
					// the explicit -p case and add the actual path to
					// the message for the cases where it really is a
					// surprise (e.g. `sudo config gen` defaulting to
					// /home/$user/skywire-config.json).
					if (owner != rootOwner) && isRoot && !isPkgEnv {
						log.Warnf("writing config as root to %s (owned by %q, not root); pass -p for the package path or -o to choose explicitly", confPath, owner)
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

		configureResolvingProxies()

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
		dmsg.Prod.DmsgServers, embeddedConf.DmsgDiscovery, "", "")
	if err != nil {
		logIfNotStdout(log, err, "DMSG bootstrap failed for config fetch")
		return false
	}
	defer dmsgBoot.Close()

	// Pre-seed the conf service's PK in the dmsg client's entry cache.
	// The conf service runs as a direct client (it doesn't register in
	// the HTTP discovery), so dmsgC.DialStream's default disc lookup
	// fails with "entry is not found in discovery" — which is harmless
	// (the FallbackRoundTripper below recovers via plain HTTP) but
	// loud during a fresh apt install where every postinst log line
	// is operator-visible. Seeding the entry tells DialStream the conf
	// service is reachable via every embedded DMSG server as a
	// delegated relay, mirroring the visor-side
	// `seedDmsgServiceEntries` pattern in pkg/visor/init_dmsg.go.
	if confPK := dmsg.ExtractPKFromDmsgAddr(embeddedConf.ConfDmsg); confPK != "" {
		var pkObj cipher.PubKey
		if err := pkObj.UnmarshalText([]byte(confPK)); err == nil {
			serverPKs := make([]cipher.PubKey, 0, len(dmsg.Prod.DmsgServers))
			for i := range dmsg.Prod.DmsgServers {
				serverPKs = append(serverPKs, dmsg.Prod.DmsgServers[i].Static)
			}
			dmsgBoot.Client.SeedEntryCache(pkObj, &disc.Entry{
				Static: pkObj,
				Client: &disc.Client{DelegatedServers: serverPKs},
			})
		}
	}

	// Make HTTP request through DMSG transport using FallbackRoundTripper.
	// This tries each connected DMSG server directly, bypassing discovery
	// lookup (conf service uses a direct client, not registered in discovery).
	dmsgHTTPClient := &http.Client{
		Transport: dmsgclient.NewFallbackRoundTripper(ctx, []*dmsg.Client{dmsgBoot.Client}),
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
					for _, j := range oldConf.Pty.Whitelist {
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

// mergeExistingApps makes a regenerate non-destructive to the launcher apps.
// Without it, `config gen -r` rebuilds the apps list from the current defaults,
// which both adds newly-introduced apps AND silently drops any app the operator
// added by hand (plus resetting their per-app toggles). This reconciles the
// freshly-built default list against the previous config so that:
//
//   - operator-toggled fields on apps present in both (AutoStart, Args,
//     LauncherMode, RestartPolicy) are restored from the old entry — so a regen
//     doesn't reset e.g. a skysocks-client's --srv or an app you enabled;
//   - custom apps present only in the old config are carried over verbatim; and
//   - brand-new default apps (added since the old config was written) still
//     appear.
//
// Structural/versioned fields (Binary, Port) always come from the freshly-built
// defaults. No-op when not regenerating or when the old config had no apps.
func mergeExistingApps(log *logging.Logger) {
	if !isRegen || oldConfCache == nil || oldConfCache.Launcher == nil {
		return
	}
	old := oldConfCache.Launcher.Apps
	if len(old) == 0 {
		return
	}
	oldByName := make(map[string]appserver.AppConfig, len(old))
	for _, a := range old {
		oldByName[a.Name] = a
	}
	newByName := make(map[string]struct{}, len(conf.Launcher.Apps))
	var added []string
	for i := range conf.Launcher.Apps {
		name := conf.Launcher.Apps[i].Name
		newByName[name] = struct{}{}
		prev, ok := oldByName[name]
		if !ok {
			added = append(added, name)
			continue
		}
		conf.Launcher.Apps[i].AutoStart = prev.AutoStart
		if len(prev.Args) > 0 {
			conf.Launcher.Apps[i].Args = prev.Args
		}
		if prev.LauncherMode != "" {
			conf.Launcher.Apps[i].LauncherMode = prev.LauncherMode
		}
		if prev.RestartPolicy != "" {
			conf.Launcher.Apps[i].RestartPolicy = prev.RestartPolicy
		}
	}
	var carried []string
	for _, a := range old {
		if _, ok := newByName[a.Name]; !ok {
			conf.Launcher.Apps = append(conf.Launcher.Apps, a)
			carried = append(carried, a.Name)
		}
	}
	if len(added) > 0 {
		log.Infof("regen: added %d new default app(s): %s", len(added), strings.Join(added, ", "))
	}
	if len(carried) > 0 {
		log.Infof("regen: preserved %d custom app(s): %s", len(carried), strings.Join(carried, ", "))
	}
}

// configureDMSGHTTP loads dmsghttp server list when dmsg URLs are needed.
// Deployment services are dmsg-only; the DMSG fields are already in the services
// struct from services-config.json. The separate dmsghttp-config.json path is
// retained for backward compatibility and custom deployment overrides.
func configureDMSGHTTP(log *logging.Logger, outerErr error) {
	_ = outerErr
	{
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
	// The deployment's keys are dropped on builds that do not want them (the
	// phone — see visorconfig.UseDeploymentSurveyWhitelist). Keys named
	// explicitly with --surveywhitelist are always kept: the policy is a
	// default, not a prohibition.
	if visorconfig.UseDeploymentSurveyWhitelist() {
		services.SurveyWhitelist = append(services.SurveyWhitelist, surveyWlPKs...)
	} else {
		services.SurveyWhitelist = surveyWlPKs
	}

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

// serviceProjectionJQ returns the JQ filter to project the generated
// visor config into a service-specific config shape. Returns empty
// string when no projection flag is set, so writeConfigOutput emits
// the regular visor config. The projections deliberately mirror the
// existing fields the target service already accepts — they don't
// invent new schema, only rearrange the visor config's keys.
//
// Mutually-exclusive flags: only the FIRST set flag wins, in
// precedence order sn → dmsgsrv → disc. The CLI rejects multiple
// flags upstream of this helper, but the precedence here keeps
// behavior deterministic if that check is ever bypassed.
func serviceProjectionJQ() string {
	switch {
	case snConfig:
		return "{public_key: .pk, secret_key: .sk, dmsg: {discovery: .dmsg.discovery, discovery_dmsg: .dmsg.discovery_dmsg, sessions_count: .dmsg.sessions_count, servers: .dmsg.servers}, transport_discovery: (.transport.discovery_dmsg // .transport.discovery), log_level: .log_level}"
	case dmsgSrvConfig:
		// Project to a dmsg-server config. The output `dmsg` block
		// mirrors the visor's `dmsg` block shape — same fields,
		// `discovery` / `discovery_dmsg` / `servers` / `sessions_count`
		// carried straight over. Single-deployment shape (object form)
		// by default; users with multi-deployment requirements hand-
		// edit the `dmsg` field into an array of the same per-element
		// shape. `public_address` lives at the top level (one external
		// address for the single TCP listener); the per-deployment
		// override `advertised_address` inside `dmsg[i]` is only set
		// when the multi-network NAT case requires it.
		return "{public_key: .pk, secret_key: .sk, dmsg: {discovery: .dmsg.discovery, discovery_dmsg: .dmsg.discovery_dmsg, sessions_count: .dmsg.sessions_count, servers: .dmsg.servers}, public_address: \"\", local_address: \":8081\", health_endpoint_address: \":8082\", log_level: .log_level, max_sessions: 2048}"
	case dmsgDiscConfig:
		// Project to a dmsg-discovery config. Keys map onto the
		// Config struct in cmd/dmsg/dmsg-discovery/commands/config.go.
		// dmsg_servers is the static transit set (replaces the
		// runtime read of deployment.Prod.DmsgServers).
		return "{public_key: .pk, secret_key: .sk, addr: \":9090\", redis: \"redis://localhost:6379\", dmsg_port: 80, mode: \"dual\", dmsg_servers: .dmsg.servers, log_level: .log_level}"
	case tpdConfig:
		// Project to a transport-discovery config. The `dmsg` block
		// matches the shape used by every other service in this
		// family so operators see one schema everywhere.
		return "{public_key: .pk, secret_key: .sk, addr: \":9091\", redis: \"redis://localhost:6379\", redis_pool_size: 10, entry_timeout: 300000000000, log_level: .log_level, tag: \"transport_discovery\", mode: \"dual\", store_data_path: \"/var/lib/skywire/tpd/bandwidth\", uptime_db: \"/var/lib/skywire/tpd/uptime.db\", dmsg: {discovery: .dmsg.discovery, discovery_dmsg: .dmsg.discovery_dmsg, sessions_count: .dmsg.sessions_count, servers: .dmsg.servers}}"
	case sdConfig:
		// Project to a service-discovery config. Same `dmsg` shape.
		return "{public_key: .pk, secret_key: .sk, addr: \":9098\", redis: \"redis://localhost:6379\", entry_timeout: 300000000000, log_level: .log_level, mode: \"dual\", dmsg: {discovery: .dmsg.discovery, discovery_dmsg: .dmsg.discovery_dmsg, sessions_count: .dmsg.sessions_count, servers: .dmsg.servers}}"
	case arConfig:
		// Project to an address-resolver config.
		return "{public_key: .pk, secret_key: .sk, addr: \":9093\", udp_addr: \":30178\", redis: \"redis://localhost:6379\", redis_pool_size: 10, entry_timeout: 300000000000, log_level: .log_level, tag: \"address_resolver\", mode: \"dual\", dmsg: {discovery: .dmsg.discovery, discovery_dmsg: .dmsg.discovery_dmsg, sessions_count: .dmsg.sessions_count, servers: .dmsg.servers}}"
	case rfConfig:
		// Project to a route-finder config.
		return "{public_key: .pk, secret_key: .sk, addr: \":9092\", redis: \"redis://localhost:6379\", redis_pool_size: 10, log_level: .log_level, tag: \"route_finder\", mode: \"dual\", dmsg: {discovery: .dmsg.discovery, discovery_dmsg: .dmsg.discovery_dmsg, sessions_count: .dmsg.sessions_count, servers: .dmsg.servers}}"
	}
	return ""
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
			// "file" (the retired on-disk CSV store) is now a no-op that only warns
			// at boot; default fresh configs to "memory" so they don't carry it.
			Type:             visorconfig.MemoryLogStore,
			Location:         tpLogPath,
			RotationInterval: visorconfig.DefaultLogRotationInterval,
		},
		SudphPort:          sudphPort,
		StcprPort:          stcprPort,
		TransportPort:      transportPort,
		ARTransportLimit:   arTransportLimit,
		NoDirectTransports: noDirectTransports,
	}
}

// minHopsValue returns the --min-hops flag value clamped to a valid uint16.
// Negative input (nonsensical for a hop count) is treated as 0 (no minimum).
func minHopsValue() uint16 {
	if genMinHops < 0 {
		return 0
	}
	return uint16(genMinHops) //nolint:gosec
}

// configureRouting sets up routing configuration on conf, preserving MinHops
// from a previous config if regenerating.
func configureRouting() {
	conf.Routing = &visorconfig.Routing{
		RouteFinder:        services.RouteFinder,     //utilenv.RouteFinderAddr,
		RouteSetupNodes:    services.RouteSetupNodes, //[]cipher.PubKey{utilenv.MustPK(utilenv.SetupPK)},
		RouteFinderTimeout: visorconfig.DefaultTimeout,
		MinHops:            minHopsValue(),
		CalculateRoutes:    enableCalculateRoutes,
		// Default routing policy: the "adaptive" composite preset. It is a no-op
		// for latency-sensitive/other apps (decideAdaptive returns the empty spec
		// for anything but the client apps) and shapes vpn-client/skysocks-client/
		// skynet-client dials — seeding the most transport-diverse route, then
		// letting on_tick size/evict/probe adaptively. Overridable: an explicit
		// routing.policy_per_dial or a per-app launcher routing_policy wins, and
		// it is preserved across regen (see below).
		PolicyPerDial: "preset:adaptive",
		// Cascade route setup is opt-in (--cascade); the legacy setup-node
		// path stays the default until the cascade multihop data-plane bug
		// is fixed and enough of the network has updated to support it.
		EnableCascadeRouteSetup: cascadeRouteSetup,
	}

	if muxRoutes > 0 {
		conf.Routing.MuxRoutes = muxRoutes
	}

	if oldConfCache != nil && oldConfCache.Routing != nil {
		// Preserve the previous min_hops across regen UNLESS --min-hops (or
		// MINHOPS in skywire.conf) was set to a non-default value this run, in
		// which case the new value already in the literal above wins.
		if oldConfCache.Routing.MinHops != 0 && minHopsValue() == 1 {
			conf.Routing.MinHops = oldConfCache.Routing.MinHops
		}
		// Preserve an opted-in cascade across regen (matches MinHops). To turn
		// it back off, edit enable_cascade_route_setup in the JSON directly.
		if oldConfCache.Routing.EnableCascadeRouteSetup {
			conf.Routing.EnableCascadeRouteSetup = true
		}
		// Respect an explicit routing policy chosen in the previous config
		// (any non-empty value the operator set, including "none"). Only a
		// config that never specified one falls through to the adaptive default
		// above, so regenerating an untouched config opts it into the new
		// default while never clobbering a deliberate choice.
		if oldConfCache.Routing.PolicyPerDial != "" {
			conf.Routing.PolicyPerDial = oldConfCache.Routing.PolicyPerDial
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
	// The standalone uptime-tracker is deprecated (uptime is now tracked by the
	// discovery services). Generated configs no longer carry an `uptime_tracker`
	// block — conf.UptimeTracker stays nil and initUptimeTracker skips it.
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
	conf.GeoIP = services.GeoIP
	if conf.GeoIP == "" {
		conf.GeoIP = deployment.Prod.GeoIP
	}
	conf.MemoryLimit = "auto"
	// Config bootstrap service
	conf.ConfService = serviceConfURL
	conf.ConfServiceDmsg = services.ConfDmsg
	if conf.ConfServiceDmsg == "" {
		conf.ConfServiceDmsg = deployment.Prod.ConfDmsg
	}
	// Reward system endpoints
	conf.RewardSystem = services.RewardSystem
	conf.RewardSystemDmsg = services.RewardSystemDmsg
	if conf.RewardSystem == "" {
		conf.RewardSystem = deployment.Prod.RewardSystem
	}
	if conf.RewardSystemDmsg == "" {
		conf.RewardSystemDmsg = deployment.Prod.RewardSystemDmsg
	}
	if rewardSkyAddr != "" {
		canonical, _, err := rewardconfig.ValidateRewardAddress(rewardSkyAddr)
		if err != nil {
			log.WithError(err).Fatal("Invalid reward address")
		}
		conf.RewardAddress = canonical
	}

	dmsgptyAddr := pty.DefaultCLIAddr()
	if isTestEnv {
		dmsgptyAddr = filepath.Join(os.TempDir(), "dmsgpty-test.sock")
	}
	conf.Pty = &visorconfig.Pty{
		DmsgPort:     skyenv.DmsgPtyPort,
		CLINet:       skyenv.DmsgPtyCLINet,
		CLIAddr:      dmsgptyAddr,
		AllowRPCExec: ptyRPCExec,
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
	// Service-URL mode: dmsg-only by DEFAULT (dmsg:// URLs in the primary
	// fields, no http). --dual keeps the http URLs with dmsg in the _dmsg
	// fallback fields. --http (isHTTPOnly) skips the dmsg fields entirely.
	// --dmsghttp forces dmsg-only (== the default) for backward compatibility.
	// Deployment services are dmsg-only: the dmsg:// service URLs go straight into
	// the primary config fields. (Plain-HTTP to deployment services is no longer
	// supported — the former --http / --dual modes are gone.) The dmsg URLs come
	// from services-config.json's *_dmsg fields, the single source of truth.
	if services.HasDmsgEndpoints() {
		conf.Dmsg.Servers = deployment.DmsgServerEntriesToDisc(services.DmsgServers)
		conf.Dmsg.Discovery = services.DmsgDiscoveryDmsg
		conf.Transport.AddressResolver = services.AddressResolverDmsg
		conf.Transport.Discovery = services.TransportDiscoveryDmsg
		conf.Routing.RouteFinder = services.RouteFinderDmsg
		conf.Launcher.ServiceDisc = services.ServiceDiscoveryDmsg
	} else if dmsgHTTPServersList != nil {
		// Legacy fallback: separate dmsghttp-config.json (custom deployments).
		dmsgConf := &dmsgHTTPServersList.Prod
		if isTestEnv {
			dmsgConf = &dmsgHTTPServersList.Test
		}
		conf.Dmsg.Servers = dmsgConf.DMSGServers
		conf.Dmsg.Discovery = dmsgConf.DMSGDiscovery
		conf.Transport.AddressResolver = dmsgConf.AddressResolver
		conf.Transport.Discovery = dmsgConf.TransportDiscovery
		conf.Routing.RouteFinder = dmsgConf.RouteFinder
		conf.Launcher.ServiceDisc = dmsgConf.ServiceDiscovery
	}

	// Configure the skycoin-web wallet. Only emit a block when the operator
	// asked for something — an unconditional block would add noise to every
	// generated config AND freeze today's defaults into the file, so a later
	// change to those defaults would not reach existing configs.
	if noWallet || walletCustody != "" {
		switch walletCustody {
		case "", visorconfig.WalletCustodyBrowser, visorconfig.WalletCustodyDisk, visorconfig.WalletCustodyRemote:
		default:
			log.Fatalf("invalid --wallet-custody %q: want browser, disk or remote", walletCustody)
		}
		if walletCustody == visorconfig.WalletCustodyRemote && walletRemotePK == "" {
			log.Fatal("--wallet-custody=remote requires --wallet-remote <visor-pk>")
		}
		conf.Wallet = &visorconfig.WalletConfig{
			Serve:    !noWallet,
			Custody:  walletCustody,
			Dir:      walletDir,
			RemotePK: walletRemotePK,
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
	// Always include hypervisor config (for runtime enable/disable).
	// The Enable field controls whether it auto-starts.
	{
		config := visorconfig.GenerateWorkDirConfig(isTestEnv)
		config.Enable = isHypervisor
		config.EnablePKEndpoint = isEnablePKEndpoint
		if hvHTTPAddr != "" {
			config.HTTPAddr = hvHTTPAddr
		} else {
			config.HTTPAddr = offsetAddr(config.HTTPAddr)
		}
		// Embedded LAN/WAN DMSG server. Implicit-on whenever this
		// visor is also a hypervisor — the hypervisor's
		// responsibility to its managed visors implies it provides
		// the DMSG relay. Operators who want a hypervisor WITHOUT
		// the embedded server can drop the
		// `hypervisor.lan_dmsg_server.enable` boolean in
		// /opt/skywire/skywire.json post-gen; that path is
		// expected to be vanishingly rare so it's not exposed via
		// a config-gen flag.
		if isHypervisor {
			// Generate the LAN DMSG server keypair eagerly. The struct's
			// `pk`/`sk` json tags use `omitempty` but encoding/json does
			// NOT treat a zero-valued fixed-size byte array as empty, so
			// without explicit keys here the marshaled config emits
			// `"sk": "0000...0000"`. Re-reading that config back fails
			// because cipher.SecKey.UnmarshalText calls SecKeyFromHex →
			// NewSecKey → secp256k1.VerifySeckey which rejects all-zero
			// keys with ErrInvalidSecKey. The lan-dmsg server needs a
			// stable keypair anyway (the type's docstring even says it's
			// persisted across restarts), so generating it at gen time
			// is the cleanest path.
			lanPK, lanSK := cipher.GenerateKeyPair()
			config.LANDmsgServer = &visorconfig.LANDmsgServerConf{
				Enable:        true,
				Port:          lanDmsgPort,
				PublicAddress: lanDmsgPublicAddress,
				PK:            lanPK,
				SK:            lanSK,
			}
		}
		conf.Hypervisor = &config
	}

	// Manipulate dmsgpty whitelist PKs
	conf.Pty.Whitelist = make([]cipher.PubKey, 0)
	if dmsgptyWlPKs != "" {
		keys := strings.Split(dmsgptyWlPKs, ",")
		for _, key := range keys {
			if key != "" {
				keyParsed, err := coinCipher.PubKeyFromHex(strings.TrimSpace(key))
				if err != nil {
					log.WithError(err).Fatalf("Failed to parse Dmsgpty Whitelist public key: %s.", key)
				}
				conf.Pty.Whitelist = append(conf.Pty.Whitelist, cipher.PubKey(keyParsed))
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

// buildCoinNodes parses the --coin-nodes CSV (${COIN_NODES[@]}) into
// visorconfig.CoinNodeConfig entries. Each spec is "<local_addr>" or
// "<local_addr>@<dmsg_port>", where local_addr is the node's HTTP API on this
// host (host:port). When the dmsg port is omitted it defaults to local_addr's
// TCP port — so "127.0.0.1:6420" is advertised on dmsg port 6420. Whitelist is
// not expressible via the flag (public read-mostly coin APIs are the common
// case); edit the config directly to restrict peers. Returns nil for empty.
func buildCoinNodes() ([]visorconfig.CoinNodeConfig, error) {
	specs := splitCSV(coinNodes)
	if len(specs) == 0 {
		return nil, nil
	}
	out := make([]visorconfig.CoinNodeConfig, 0, len(specs))
	for _, s := range specs {
		addr := s
		var dport uint64
		if at := strings.LastIndex(s, "@"); at >= 0 {
			addr = s[:at]
			p, err := strconv.ParseUint(s[at+1:], 10, 16)
			if err != nil {
				return nil, fmt.Errorf("coin-nodes: invalid dmsg port in %q: %w", s, err)
			}
			dport = p
		}
		_, portStr, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("coin-nodes: invalid local_addr %q (want host:port): %w", addr, err)
		}
		if dport == 0 {
			p, err := strconv.ParseUint(portStr, 10, 16)
			if err != nil {
				return nil, fmt.Errorf("coin-nodes: invalid port in %q: %w", addr, err)
			}
			dport = p
		}
		out = append(out, visorconfig.CoinNodeConfig{LocalAddr: addr, DmsgPort: uint16(dport)})
	}
	return out, nil
}

// buildSkycoinDaemonApps emits the AppConfig slice for skycoin-daemon
// instances based on the SKYCOIND_* knobs.
//
// Two paths:
//
//   - Multi-instance: SKYCOIND_INSTANCES is non-empty. Each entry is
//     either the literal "skycoin" (built-in defaults, no FIBER_TOML)
//     or a path to a fiber.toml. Emits one AppConfig per entry with
//     auto-allocated --port (base 6420, step +2) and --data-dir.
//     SKYCOIND_FLAGS is appended to every instance's args (and
//     validated to not redefine --port or --data-dir, which the
//     auto-allocator owns). SKYCOIND_FIBER_TOML / SKYCOIND_API_SETS
//     are silently ignored on this path — they're the legacy single-
//     instance knobs.
//
//   - Legacy single-instance: SKYCOIND_INSTANCES empty. Emits the
//     historical single AppConfig with skyenv.SkycoinDaemonName, args
//     from --skycoindapi, env from --skycoindfiber. AutoStart driven
//     by --skycoind. Always emitted (even when --skycoind=false) so
//     operators can toggle autostart at runtime via the Apps tab.
//
// Returns an error on flag-validation failure; the caller should
// surface it to log.Fatal so config-gen aborts cleanly.
func buildSkycoinDaemonApps() ([]appserver.AppConfig, error) {
	insts := splitCSV(skycoinDaemonInstances)

	if len(insts) == 0 {
		// Legacy single-instance path — emit the daemon entry
		// regardless of SKYCOIND so the Apps tab can flip it on.
		// Default flags expose the full GUI API surface (so the
		// thin-client wallet has everything it needs to talk to
		// this daemon) and run at debug log level (this is dev
		// territory — terse default-skycoin logging hides
		// problems). Operator can override via the universal
		// settings panel or --skycoindapi for the GUI flag alone.
		// Port and data-dir are deliberately omitted: skycoin's
		// own defaults (6420 + ~/.skycoin) are sensible for the
		// single-instance case.
		args := []string{
			"skycoin", "daemon",
			"--enable-all-api-sets=true",
			"--log-level=debug",
		}
		if skycoinDaemonAPISets != "" {
			args = append(args, "--enable-gui-api-sets", skycoinDaemonAPISets)
		}
		var env []string
		if skycoinDaemonFiber != "" {
			env = append(env, "FIBER_TOML="+skycoinDaemonFiber)
		}
		return []appserver.AppConfig{{
			Name:      skyenv.SkycoinDaemonName,
			Binary:    "skywire",
			AutoStart: isSkycoinDaemonEnable,
			Port:      routing.Port(skyenv.SkycoinDaemonPort),
			Args:      args,
			User:      skycoinDaemonUser,
			Env:       env,
		}}, nil
	}

	// Multi-instance path — validate and emit.
	flagsToks, err := splitDaemonFlags(skycoinDaemonFlags)
	if err != nil {
		return nil, fmt.Errorf("--skycoindflags: %w", err)
	}
	for _, t := range flagsToks {
		if t == "--port" || strings.HasPrefix(t, "--port=") ||
			t == "--data-dir" || strings.HasPrefix(t, "--data-dir=") {
			return nil, fmt.Errorf("--skycoindflags must not contain --port or --data-dir; those are auto-allocated per instance")
		}
	}

	const basePort = 6420 // skycoin daemon's default HTTP port
	const portStep = 2    // +2 leaves the daemon's RPC port slot free per instance

	out := make([]appserver.AppConfig, 0, len(insts))
	usedNames := make(map[string]bool, len(insts))
	for i, raw := range insts {
		name, fiberPath, err := parseDaemonInstance(raw)
		if err != nil {
			return nil, fmt.Errorf("--skycoindinstances entry %d (%q): %w", i, raw, err)
		}
		appName := skyenv.SkycoinDaemonName + "-" + name
		if usedNames[appName] {
			return nil, fmt.Errorf("--skycoindinstances duplicate instance name %q", name)
		}
		usedNames[appName] = true

		port := basePort + i*portStep
		dataDir := filepath.Join(conf.LocalPath, appName)

		// Multi-instance: --port and --data-dir MUST differ per
		// instance so they're auto-allocated regardless. The same
		// "open the GUI API + log debug" defaults the single-
		// instance path uses apply here too. SKYCOIND_FLAGS
		// layers on top.
		args := []string{"skycoin", "daemon",
			"--port", fmt.Sprintf("%d", port),
			"--data-dir", dataDir,
			"--enable-all-api-sets=true",
			"--log-level=debug",
		}
		args = append(args, flagsToks...)

		var env []string
		if fiberPath != "" {
			env = append(env, "FIBER_TOML="+fiberPath)
		}

		// Skywire app port: keep stepping by 1 from the legacy
		// SkycoinDaemonPort so multiple instances each have a
		// unique slot inside the visor's app-port space.
		appPort := routing.Port(int(skyenv.SkycoinDaemonPort) + i)

		out = append(out, appserver.AppConfig{
			Name:      appName,
			Binary:    "skywire",
			AutoStart: isSkycoinDaemonEnable,
			Port:      appPort,
			Args:      args,
			User:      skycoinDaemonUser,
			Env:       env,
		})
	}
	return out, nil
}

// parseDaemonInstance maps a SKYCOIND_INSTANCES entry to an
// (instance-name, fiber.toml-path) pair. The literal "skycoin"
// produces ("skycoin", "") — built-in defaults. Any other value is
// treated as a fiber.toml path; the name is the basename without
// the .toml suffix.
func parseDaemonInstance(raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", fmt.Errorf("empty entry")
	}
	if raw == "skycoin" {
		return "skycoin", "", nil
	}
	base := filepath.Base(raw)
	base = strings.TrimSuffix(base, ".toml")
	if base == "" || base == "." || base == "/" {
		return "", "", fmt.Errorf("can't derive instance name from path")
	}
	return base, raw, nil
}

// splitCSV splits a comma-separated string into trimmed, non-empty
// tokens. scriptExecArray's bash-array expansion lands here.
func splitCSV(s string) []string {
	out := []string{}
	for _, t := range strings.Split(s, ",") {
		t = strings.TrimSpace(t)
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

// splitDaemonFlags applies a tiny shell-like tokenizer to the
// SKYCOIND_FLAGS string so values quoted with " ... " stay intact.
// Wraps the same parser used for the on-disk config file.
func splitDaemonFlags(s string) ([]string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	return visorconfig.SplitArgs(s)
}

// skychatExternalArgs builds the launcher Args slice for the skychat
// app under external-apps mode (apps run as separate `skywire app`
// invocations). When pair is true the chat-app gets --pair-enable,
// which opens its pair-RPC channel to the visor and is required for
// group chat (#2580 federated send, #2585 admin mirror feeds) to
// actually propagate messages.
func skychatExternalArgs(addr string, pair bool) []string {
	args := []string{"app", "skychat", "--addr", addr}
	if pair {
		args = append(args, "--pair-enable")
	}
	return args
}

// skychatInternalArgs builds the launcher Args for skychat under the (default)
// internal-apps mode: apps run inside the visor process. The external-mode
// "app skychat" prefix is dropped (the launcher routes by app Name); the
// --addr it binds is the same one the external form uses, so the local UI,
// `skywire cli skychat`, and `skywire cli visor doctor` all find skychat in
// the same place whichever mode the config was generated in.
//
// Running in-process does not require giving up the port: skychat publishes
// its mux to the visor's control surface either way, so the hypervisor keeps
// its no-loopback path to the same handler this listener serves.
//
// portless drops the listener entirely — no TCP port, hypervisor-only access.
// Opt-in, because "the app is running but nothing answers on its address" is
// a surprising default to hand someone.
func skychatInternalArgs(addr string, pair, portless bool) []string {
	args := []string{"--addr", addr}
	if portless {
		args = []string{"--portless"}
	}
	if pair {
		args = append(args, "--pair-enable")
	}
	return args
}

// skycoinWebFlagsEnv builds the bare skycoin-web flags (--host/--port/
// --node-url/--wallet-dir) and the resolving-proxy env shared by both the
// external-launch form (`skywire skycoin web <flags>`) and the internal
// launcher app (RunSkycoinWeb, handed the flags directly). Keeping one
// source for both is what lets skycoin-web launch identically on the
// host-native and (via the same internal path) the wasm visor.
func skycoinWebFlagsEnv() (flags, env []string) {
	if skycoinWebAddr != "" {
		if h, p, err := net.SplitHostPort(offsetAddr(skycoinWebAddr)); err == nil {
			flags = append(flags, "--host", h, "--port", p)
		}
	}
	nodeURLs := skycoinWebNodeURLs
	if strings.TrimSpace(nodeURLs) == "" && (enableDmsgWeb || enableSkynetWeb) {
		// No explicit SKYCOINWEBNODES and a resolving proxy is enabled →
		// default to the deployment skycoin node over the mesh (same PK:port
		// as services-config.json prod.skycoin_node_dmsg and the wasm
		// wallet's defaultCoinNode; here as an http URL the proxy resolves).
		nodeURLs = "http://039a6d1e3c237f5f05b78ec19e9f31a007f84835d7ef1e812876102281d1db74c1.dmsg:6420"
	}
	for _, u := range strings.Split(nodeURLs, ",") {
		if u = strings.TrimSpace(u); u != "" {
			flags = append(flags, "--node-url", u)
		}
	}
	if skycoinWebWalletDir != "" {
		flags = append(flags, "--wallet-dir", skycoinWebWalletDir)
	}
	// dmsgweb (:4445) auto-chains skynetweb, so it's the single entry point
	// when both are on; skynetweb-only uses :4446.
	if enableDmsgWeb || enableSkynetWeb {
		proxy := "socks5://127.0.0.1:4445"
		if !enableDmsgWeb && enableSkynetWeb {
			proxy = "socks5://127.0.0.1:4446"
		}
		env = []string{"HTTP_PROXY=" + proxy, "HTTPS_PROXY=" + proxy}
	}
	return flags, env
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
		apps := []appserver.AppConfig{
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
				// --pair-enable opens the chat-app ↔ visor pair-RPC
				// channel that group chat needs for the per-partner
				// CXO feeds (#2580 federated send + #2585 admin mirror)
				// and for `cli skychat group history` to work. Without
				// it the chat-app's /status reports
				// `groups_error: pair-rpc-disabled` and group SSE events
				// don't reach listeners. Default-on via SKYCHATPAIR
				// (overridable in /etc/skywire.conf).
				Args: skychatExternalArgs(chatAddr, isSkychatPairEnable),
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
			{
				// Local LAN/WiFi gateway that NATs downstream clients into the
				// vpn-client tunnel. Off by default; enabled via --servevpnrouter
				// (VPNROUTER) and needs a downstream interface (--vpnrouter-lan-ifc).
				Name:      skyenv.VPNRouterName,
				Binary:    "skywire",
				AutoStart: isVpnRouterEnable,
				Port:      routing.Port(skyenv.VPNRouterPort),
				Args:      vpnRouterArgs([]string{"app", "vpn-router"}),
			},
			{
				Name:      skyenv.SkydexMarketName,
				Binary:    "skywire",
				AutoStart: false,
				Port:      routing.Port(skyenv.SkydexMarketPort),
				Args:      []string{"app", "skydex-market", "--addr", skyenv.SkydexMarketAddr},
			},
			{
				Name:      skyenv.SkydexClientName,
				Binary:    "skywire",
				AutoStart: false,
				Port:      routing.Port(skyenv.SkydexClientPort),
				// --market-port is sourced from the same constant as the market
				// app's routing Port above, so the client always dials where the
				// market listens (single source of truth).
				Args: []string{"app", "skydex-client", "--addr", skyenv.SkydexClientAddr, "--market-port", strconv.Itoa(int(skyenv.SkydexMarketPort))},
			},
		}
		// Skycoin daemon — full node, syncs the chain locally. May
		// emit one entry (legacy single-instance) or N (one per
		// SKYCOIND_INSTANCES entry). Each multi-instance entry gets
		// auto-allocated --port + --data-dir and a FIBER_TOML env
		// when the entry is a fiber.toml path.
		daemons, derr := buildSkycoinDaemonApps()
		if derr != nil {
			log.WithError(derr).Fatal("invalid skycoin daemon configuration")
		}
		apps = append(apps, daemons...)

		// Skycoin thin-client web wallet. AutoStart driven by
		// SKYCOINWEB. SKYCOINWEBNODES is a CSV → repeated
		// --node-url flags so the wallet can multi-coin-browse
		// (default skycoin + fibercoins). The User= field lets
		// the wallet drop to the operator's UID so the wallet dir
		// is writable even when the visor itself runs as _skywire.
		// External-launch form: `skywire app skycoin web <flags>` (Binary=skywire),
		// under the same `skywire app <name>` namespace as the other apps.
		// Opt-in — the default is the internal launcher app in the else branch.
		webFlags, webEnv := skycoinWebFlagsEnv()
		webArgs := append([]string{"app", "skycoin", "web"}, webFlags...)
		apps = append(apps, appserver.AppConfig{
			Name:      skyenv.SkycoinWebName,
			Binary:    "skywire",
			AutoStart: isSkycoinWebEnable,
			Port:      routing.Port(skyenv.SkycoinWebPort),
			Args:      webArgs,
			Env:       webEnv,
			User:      skycoinWebUser,
		})

		conf.Launcher.Apps = apps
	} else {
		// Internal apps configuration (default - apps run within visor process)
		swFlags, swEnv := skycoinWebFlagsEnv()
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
				// --pair-enable opens chat-app ↔ visor pair-RPC for
				// group chat. See the external-apps branch above for
				// the rationale; mirrored here so internal-apps
				// generated configs behave the same — as is --addr,
				// so skychat answers on the same place in both modes.
				Args: skychatInternalArgs(chatAddr, isSkychatPairEnable, isSkychatPortless),
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
			{
				// Local LAN/WiFi gateway that NATs downstream clients into the
				// vpn-client tunnel. Off by default; enabled via --servevpnrouter
				// (VPNROUTER) with a downstream interface (--vpnrouter-lan-ifc).
				Name:      skyenv.VPNRouterName,
				AutoStart: isVpnRouterEnable,
				Args:      vpnRouterArgs(nil),
				Port:      routing.Port(skyenv.VPNRouterPort),
			},
			{
				Name:      skyenv.SkydexMarketName,
				AutoStart: false,
				Args:      []string{"--addr", skyenv.SkydexMarketAddr},
				Port:      routing.Port(skyenv.SkydexMarketPort),
			},
			{
				Name:      skyenv.SkydexClientName,
				AutoStart: false,
				// --market-port from the same constant as the market's routing Port,
				// so the client always dials where the market listens.
				Args: []string{"--addr", skyenv.SkydexClientAddr, "--market-port", strconv.Itoa(int(skyenv.SkydexMarketPort))},
				Port: routing.Port(skyenv.SkydexClientPort),
			},
			{
				// skycoin-web thin-client wallet as an INTERNAL app — Binary
				// empty → RunSkycoinWeb (cmd/apps/skycoin-web/commands). This
				// is the default; the same internal path serves it on the wasm
				// visor. External (`skywire skycoin web`) is the opt-in above.
				//
				// IMPORTANT — User= is a NO-OP for an internal app. Internal
				// apps run IN the visor process (RunModeInternal AppFunc), so
				// there is no child process to setuid: on a root visor (common,
				// for VPN + root pty) an internal skycoin-web IS root, and any
				// wallet dir it writes lands under root's HOME
				// (/root/.skycoin/wallets). To actually drop to SKYCOINWEBUSER
				// and write wallets under that user's HOME, run skycoin-web
				// EXTERNAL (the --external-apps branch above), which the
				// launcher spawns as a separate process and can setuid. The
				// default (no --wallet-dir) sidesteps this entirely: skycoin-web
				// runs web-only, wallets live in browser storage — same as the
				// wasm visor.
				Name:      skyenv.SkycoinWebName,
				AutoStart: isSkycoinWebEnable,
				Port:      routing.Port(skyenv.SkycoinWebPort),
				Args:      swFlags,
				Env:       swEnv,
				User:      skycoinWebUser,
			},
		}
	}

	// Fibercoin node discovery (servicedisc.ServiceTypeCoin). Independent
	// of the apps mode above — the node runs on its own; the visor only
	// forwards + advertises it. See pkg/visor/init_coinnode.go.
	coinNodeCfgs, cnErr := buildCoinNodes()
	if cnErr != nil {
		log.WithError(cnErr).Fatal("invalid coin-nodes configuration")
	}
	conf.CoinNodes = coinNodeCfgs

	// On regen, fold the previous config's apps back in so a regenerate
	// updates-in-place instead of wiping the operator's setup: custom apps
	// (added by hand) survive, and the operator's per-app toggles are kept —
	// while brand-new default apps (added since the old config was written)
	// still appear. Runs before --disable-apps so an explicit disable still
	// wins over a preserved entry.
	mergeExistingApps(log)

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

// configureResolvingProxies populates the dmsg_web / skynet_web
// blocks when --dmsgweb / --skynetweb is set. The visor auto-starts
// each block at boot when Enable=true and (when both are enabled)
// auto-chains dmsgweb → skynetweb so a browser pointed at port 4445
// covers both .dmsg and .skynet traffic.
func configureResolvingProxies() {
	if enableDmsgWeb {
		conf.DmsgWeb = &visorconfig.DmsgWebConfig{
			Enable:        true,
			UpstreamSOCKS: dmsgWebUpstreamSOCKS,
			ProxyAddr:     dmsgWebProxyAddr,
		}
	}
	if enableSkynetWeb {
		conf.SkynetWeb = &visorconfig.SkynetWebConfig{
			Enable:        true,
			UpstreamSOCKS: skynetWebUpstreamSOCKS,
			ProxyAddr:     skynetWebProxyAddr,
		}
	}
	if enableSkymailBridge {
		conf.SkymailBridge = &visorconfig.SkymailBridgeConfig{
			Enable: true,
			Addr:   skyenv.SkymailBridgeAddr,
			Mode:   "b",
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
		if filter := serviceProjectionJQ(); filter != "" {
			jsonData, err = script.Echo(string(jsonData)).JQ(filter).Bytes()
			if err != nil {
				log.Fatalf("Failed to convert config to service config format: %v", err)
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
	if filter := serviceProjectionJQ(); filter != "" {
		j, err = script.Echo(string(j)).JQ(filter).Bytes()
		if err != nil {
			log.Fatalf("Failed to convert config to service config format: %v", err)
		}
		var data any
		if err = json.Unmarshal(j, &data); err != nil {
			log.Fatalf("Failed to convert config to service config format: %v", err)
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
	// anet, not net — the same reason as everywhere else in this repo
	// (pkg/netutil/net_native.go): Android 11+ denies unprivileged processes
	// the netlink RIB dump the standard library uses, so net.Interfaces()
	// there fails with "route ip+net: netlinkrib: permission denied". This
	// runs at flag-registration time, i.e. on EVERY invocation of this
	// binary, so on Android the standard library put that error on stdout
	// ahead of the output of whatever command was actually asked for —
	// including the `config pk` output the Android app parses. anet is a
	// straight pass-through to the standard library off Android.
	interfaces, err := anet.Interfaces()
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
		"pkg":                  "PKGENV=true",
		"ishv":                 "ISHYPERVISOR=true",
		"testenv":              "TESTENV=true",
		"dmsghttp":             "DMSGHTTP=true",
		"public":               "VISORISPUBLIC=true",
		"servevpn":             "VPNSERVER=true",
		"autoconn":             "DISABLEPUBLICAUTOCONN=true",
		"publicip":             "DISPLAYNODEIP=true",
		"no-direct-transports": "NODIRECTTRANSPORTS=true",
		"pty-rpc-exec":         "PTYRPCEXEC=true",
	}

	// Also handle string/value flags
	valueFlagToEnv := map[string]string{
		"cliaddr": "CLIADDR",
		"hvaddr":  "HVHTTPADDR",
		"timeout": "SHUTDOWNTIMEOUT",
		"reward":  "REWARDSKYADDR",
	}

	// Integer/port flags map to bare KEY=N env lines (e.g. #TRANSPORTPORT=0).
	// When the flag is explicitly set, uncomment + replace with the value so
	// `config gen --transport-port 7777 -q` reflects it in the template.
	intFlagToEnv := map[string]string{
		"transport-port":     "TRANSPORTPORT",
		"stcpr":              "STCPRPORT",
		"sudph":              "SUDPHPORT",
		"min-hops":           "MINHOPS",
		"ar-transport-limit": "ARTRANSPORTLIMIT",
	}

	// Array-shaped flags map to bash-array env vars of the form
	// KEY=('a' 'b' 'c'). Values come in comma-separated form from the
	// CLI (`--hvpks PK1,PK2`); we split on comma, trim each entry,
	// drop empties, and emit the canonical single-quoted-space-joined
	// bash-array form. Mirrors how SkyenvArray decodes the same lines
	// back out — round-trip safe.
	arrayFlagToEnv := map[string]string{
		"hvpks":           "HYPERVISORPKS",
		"dmsgpty":         "DMSGPTYPKS",
		"survey":          "SURVEYPKS",
		"url":             "SVCCONFADDR",
		"tpsetup":         "TPSETUPPKS",
		"routesetup":      "ROUTESETUPPKS",
		"vpnwl":           "VPNSERVERWL",
		"proxywl":         "PROXYSERVERWL",
		"skycoinwebnodes": "SKYCOINWEBNODES",
		"stun":            "STUNSERVERS",
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
		// Check integer/port flags (bare KEY=N lines, e.g. #TRANSPORTPORT=0)
		for flagName, envKey := range intFlagToEnv {
			if !cmd.Flags().Changed(flagName) {
				continue
			}
			val, _ := cmd.Flags().GetInt(flagName) //nolint:errcheck
			// Match the bash (#KEY=) or PowerShell (#$KEY=) template form.
			if strings.HasPrefix(trimmed, "#"+envKey+"=") {
				lines[i] = envKey + "=" + strconv.Itoa(val)
			} else if strings.HasPrefix(trimmed, "#$"+envKey+"=") {
				lines[i] = "$" + envKey + "=" + strconv.Itoa(val)
			}
		}
		// Check array flags (bash-array form: KEY=('a' 'b' 'c'))
		for flagName, envKey := range arrayFlagToEnv {
			if !cmd.Flags().Changed(flagName) {
				continue
			}
			val, _ := cmd.Flags().GetString(flagName) //nolint:errcheck
			if val == "" {
				continue
			}
			// Match either the empty template form (#KEY=('')) or a
			// previously-set non-empty form (#KEY=('foo')) so re-runs
			// of -q with new flag values overwrite the prior line.
			commentedKey := "#" + envKey + "="
			if !strings.HasPrefix(trimmed, commentedKey) {
				continue
			}
			parts := strings.Split(val, ",")
			out := make([]string, 0, len(parts))
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p == "" {
					continue
				}
				out = append(out, "'"+p+"'")
			}
			if len(out) == 0 {
				continue
			}
			lines[i] = envKey + "=(" + strings.Join(out, " ") + ")"
		}
	}
	return strings.Join(lines, "\n")
}
