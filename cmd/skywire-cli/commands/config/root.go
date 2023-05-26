// Package cliconfig cmd/skywire-cli/commands/config/root.go
package cliconfig

import (
	"strings"

	"github.com/skycoin/dmsg/pkg/disc"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	utilenv "github.com/skycoin/skywire-utilities/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var logger = logging.MustGetLogger("skywire-cli")

var (
	conf                = new(visorconfig.V1)
	dmsgHTTPServersList = &visorconfig.DmsgHTTPServers{
		Test: visorconfig.DmsgHTTPServersData{DMSGServers: []*disc.Entry{}},
		Prod: visorconfig.DmsgHTTPServersData{DMSGServers: []*disc.Entry{}},
	}
	noFetch                bool
	noDefaults             bool
	stcprPort              int
	sudphPort              int
	sk                     cipher.SecKey
	output                 string
	confPath               string
	configName             string //nolint Note: configName used, but golangci-lint marked it unused in wrong
	isStdout               bool
	isRegen                bool
	isRetainHypervisors    bool
	isTestEnv              bool
	pText                  string
	isPkgEnv               bool
	isUsrEnv               bool
	isHypervisor           bool
	hypervisorPKs          string
	dmsgptywlPKs           string
	surveywhitelistPks     string
	routesetupnodePks      string
	transportsetupPks  string
	isDmsgHTTP             bool
	isVpnServerEnable      bool
	isDisableAuth          bool
	isEnableAuth           bool
	selectedOS             string
	disableApps            string
	isBestProtocol         bool
	serviceConfURL         string
	services               *visorconfig.Services
	isForce                bool
	isHide                 bool
	isAll                  bool
	isOutUnset             bool
	ver                    string
	isRoot                 = visorconfig.IsRoot()
	svcConf                = strings.ReplaceAll(utilenv.ServiceConfAddr, "http://", "")     //visorconfig.DefaultServiceConfAddr
	testConf               = strings.ReplaceAll(utilenv.TestServiceConfAddr, "http://", "") //visorconfig.DefaultServiceConfAddr
	gHiddenFlags           []string
	uHiddenFlags           []string
	binPath                string
	logLevel               string
	isPkg                  bool
	input                  string
	isUpdateEndpoints      bool
	addHypervisorPKs       string
	isResetHypervisor      bool
	setVPNClientKillswitch string
	addVPNClientSrv        string
	addVPNClientPasscode   string
	isResetVPNclient       bool
	addVPNServerPasscode   string
	setVPNServerSecure     string
	setVPNServerAutostart  string
	setVPNServerNetIfc     string
	isResetVPNServer       bool
	addSkysocksClientSrv   string
	isResetSkysocksClient  bool
	skysocksPasscode       string
	isResetSkysocks        bool
	setPublicAutoconnect   string
	minHops                int
	isUsr                  bool
	isPublic               bool
	disablePublicAutoConn  bool
	isDisplayNodeIP        bool
	addExampleApps         bool
)

// RootCmd contains commands that interact with the config of local skywire-visor
var RootCmd = &cobra.Command{
	Use:   "config",
	Short: "Generate or update a skywire config",
	Long:  "Generate or update the config file used by skywire-visor.",
}
