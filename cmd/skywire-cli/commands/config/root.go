package cliconfig

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	utilenv "github.com/skycoin/skywire-utilities/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var logger = logging.MustGetLogger("skywire-cli")

var (
	sk                     cipher.SecKey
	output                 string
	confPath               string
	configName             string
	isStdout               bool
	isRegen                bool
	isRetainHypervisors    bool
	isTestEnv              bool
	ptext                  string
	isPkgEnv               bool
	isUsrEnv               bool
	isHypervisor           bool
	hypervisorPKs          string
	isDmsgHTTP             bool
	isPublicRPC            bool
	isVpnServerEnable      bool
	isDisableauth          bool
	isEnableauth           bool
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
	isRoot                 bool
	svcconf                = strings.ReplaceAll(utilenv.ServiceConfAddr, "http://", "")     //skyenv.DefaultServiceConfAddr
	testconf               = strings.ReplaceAll(utilenv.TestServiceConfAddr, "http://", "") //skyenv.DefaultServiceConfAddr
	ghiddenflags           []string
	uhiddenflags           []string
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
	conf                   *visorconfig.V1
	isUsr                  bool
	isPublic               bool
	isPublicAutoConn       bool
)

// RootCmd contains commands that interact with the config of local skywire-visor
var RootCmd = &cobra.Command{
	Use:   "config",
	Short: "Generate or update a skywire config",
}
