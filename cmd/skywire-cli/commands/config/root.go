// Package cliconfig cmd/skywire-cli/commands/config/root.go
package cliconfig

import (
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
		Test: visorconfig.DmsgHTTPServersData{DMSGServers: []*disc.Entry{}},
		Prod: visorconfig.DmsgHTTPServersData{DMSGServers: []*disc.Entry{}},
	}
	path                       string
	noFetch                    bool
	noDefaults                 bool
	stcprPort                  int
	sudphPort                  int
	sk                         cipher.SecKey
	output                     string
	confPath                   string
	configName                 string //nolint Note: configName used, but golangci-lint marked it unused in wrong
	isStdout                   bool
	isSquash                   bool
	isRegen                    bool
	isRetainHypervisors        bool
	isTestEnv                  bool
	pText                      string
	isPkgEnv                   bool
	isUsrEnv                   bool
	isHypervisor               bool
	hypervisorPKs              string
	dmsgptyWlPKs               string
	surveyWhitelistPKs         string
	routeSetupNodes            string
	transportSetupPKs          string
	isDmsgHTTP                 bool
	isHTTPOnly                 bool
	minDmsgSess                int
	isVpnServerEnable          bool
	isDisableAuth              bool
	isEnableAuth               bool
	selectedOS                 string
	disableApps                string
	isBestProtocol             bool
	serviceConfURL             = deployment.ProdConf.Conf
	testServiceConfURL         = deployment.TestConf.Conf
	dnsServer                  = "1.1.1.1"
	services                   visorconfig.Services
	servicesConfig             servicesConf
	isForce                    bool
	isHide                     bool
	isAll                      bool
	isOutUnset                 bool
	ver                        string
	isRoot                     = visorconfig.IsRoot()
	gHiddenFlags               []string
	uHiddenFlags               []string
	binPath                    string
	logLevel                   string
	isPkg                      bool
	input                      string
	isUpdateEndpoints          bool
	addHypervisorPKs           string
	isResetHypervisor          bool
	setVPNClientKillswitch     string
	addVPNClientSrv            string
	isResetVPNclient           bool
	addVPNServerWhitelist      string
	setVPNServerSecure         string
	setVPNServerAutostart      string
	setVPNServerNetIfc         string
	isResetVPNServer           bool
	addSkysocksClientSrv       string
	isResetSkysocksClient      bool
	skysocksWhitelist          string
	isResetSkysocks            bool
	setPublicAutoconnect       string
	minHops                    int
	isUsr                      bool
	isPublic                   bool
	disablePublicAutoConn      bool
	isDisplayNodeIP            bool
	addExampleApps             bool
	enableProxyClientAutostart bool
	isProxyServerEnable        bool
	proxyServerWhitelist       string
	configServicePath          string
	dmsgHTTPPath               string
	snConfig                   bool
	externalApps               bool
	enableCalculateRoutes      bool
	isSkychatEnable            bool
	skychatAddr                string
	rewardSkyAddr              string
	hvHTTPAddr                 string
	stunServers                string
	shutdownTimeout            string
	publicVisorRegTimeout      string
	publicVisorMaxTransports   int
	muxRoutes                  int
	cliAddr                    string
)

// RootCmd contains commands that interact with the config of local skywire-visor
var RootCmd = &cobra.Command{
	Use:   "config",
	Short: "Generate or update a skywire config",
	Long:  "Generate or update the config file used by skywire-visor.",
}

type servicesConf struct {
	Test visorconfig.Services `json:"test"`
	Prod visorconfig.Services `json:"prod"`
}

// Thin wrappers around the shared skyenv helpers in cmdutil.
// These pass the package-level skyenvfile to the shared functions.

func scriptExecString(s string) string { return cmdutil.SkyenvString(s, skyenvfile) }
func scriptExecBool(s string) bool     { return cmdutil.SkyenvBool(s, skyenvfile) }
func scriptExecArray(s string) string  { return cmdutil.SkyenvArray(s, skyenvfile) }
func scriptExecInt(s string) int       { return cmdutil.SkyenvInt(s, skyenvfile) }
func parseDefault(s string) string     { return cmdutil.SkyenvDefault(s) } //nolint:unused
