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
	autopeer               bool
	stdout                 bool
	regen                  bool
	retainHypervisors      bool
	testEnv                bool
	ptext                  string
	pkgEnv                 bool
	usrEnv                 bool
	hypervisor             bool
	hypervisorPKs          string
	dmsgHTTP               bool
	publicRPC              bool
	vpnServerEnable        bool
	disableauth            bool
	enableauth             bool
	selectedOS             string
	disableApps            string
	bestProtocol           bool
	serviceConfURL         string
	services               *visorconfig.Services
	force                  bool
	hide                   bool
	all                    bool
	outunset               bool
	ver                    string
	root                   bool
	svcconf                = strings.ReplaceAll(utilenv.ServiceConfAddr, "http://", "")     //skyenv.DefaultServiceConfAddr
	testconf               = strings.ReplaceAll(utilenv.TestServiceConfAddr, "http://", "") //skyenv.DefaultServiceConfAddr
	ghiddenflags           []string
	uhiddenflags           []string
	binPath                string
	logLevel               string
	pkg                    bool
	input                  string
	updateEndpoints        bool
	addHypervisorPKs       string
	resetHypervisor        bool
	setVPNClientKillswitch string
	addVPNClientSrv        string
	addVPNClientPasscode   string
	resetVPNclient         bool
	addVPNServerPasscode   string
	setVPNServerSecure     string
	setVPNServerAutostart  string
	setVPNServerNetIfc     string
	resetVPNServer         bool
	addSkysocksClientSrv   string
	resetSkysocksClient    bool
	skysocksPasscode       string
	resetSkysocks          bool
	setPublicAutoconnect   string
	minHops                int
	conf                   *visorconfig.V1
	usr                    bool
)

// RootCmd contains commands that interact with the config of local skywire-visor
var RootCmd = &cobra.Command{
	Use:   "config",
	Short: "Generate or update a skywire config",
}
