// Package cliconfig cmd/skywire-cli/commands/config/root.go
package cliconfig

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/bitfield/script"
	"github.com/skycoin/dmsg/pkg/disc"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
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
	serviceConfURL             = "http://conf.skywire.skycoin.com"
	testServiceConfURL         = "http://conf.skywire.dev"
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
	enableSyncTPDData          bool
	isSkychatEnable            bool
	skychatAddr                string
	hvHTTPAddr                 string
	stunServers                string
	shutdownTimeout            string
	publicVisorRegTimeout      string
	publicVisorMaxTransports   int
	muxRoutes                  int
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

// scriptExecValue sources the SKYENV file and evaluates a bash variable
// expression, returning the raw string result. This is the common core
// for all scriptExec* functions.
func scriptExecValue(expr string) (string, error) {
	if skyenv.OS == "windows" {
		// Convert bash ${VAR:-default} to just $VAR for powershell;
		// default handling is done on the Go side.
		variable := expr
		if strings.Contains(variable, ":-") {
			parts := strings.SplitN(variable, ":-", 2)
			variable = parts[0] + "}"
		}
		out, err := script.Exec(fmt.Sprintf(
			`powershell -c "$SKYENV = '%s'; if ($SKYENV -ne '' -and (Test-Path $SKYENV)) { . $SKYENV }; echo %s"`,
			skyenvfile, variable,
		)).String()
		if err != nil {
			return "", err
		}
		out = strings.TrimSpace(out)
		// If powershell echoed the literal variable name, it wasn't set.
		if out == "" || out == variable {
			return "", nil
		}
		return out, nil
	}
	out, err := script.Exec(fmt.Sprintf(
		`bash -c 'SKYENV=%s ; if [[ $SKYENV != "" ]] && [[ -f $SKYENV ]] ; then source $SKYENV ; fi ; printf "%s"'`,
		skyenvfile, expr,
	)).String()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// scriptExecSlice sources the SKYENV file and expands a bash array
// expression into a string slice, one element per line.
func scriptExecSlice(expr string) ([]string, error) {
	if skyenv.OS == "windows" {
		// Convert ${ARRAY[@]} to $ARRAY for powershell iteration.
		variable := expr
		if idx := strings.Index(variable, "[@]}"); idx != -1 {
			variable = strings.TrimSuffix(variable, "[@]}")
			variable = strings.TrimSuffix(variable, "{")
		}
		return script.Exec(fmt.Sprintf(
			`powershell -c "$SKYENV = '%s'; if ($SKYENV -ne '' -and (Test-Path $SKYENV)) { . $SKYENV }; foreach ($item in %s) { Write-Host $item }"`,
			skyenvfile, variable,
		)).Slice()
	}
	return script.Exec(fmt.Sprintf(
		`bash -c 'SKYENV=%s ; if [[ $SKYENV != "" ]] && [[ -f $SKYENV ]] ; then source $SKYENV ; fi ; for _i in %s ; do echo "$_i" ; done'`,
		skyenvfile, expr,
	)).Slice()
}

// parseDefault extracts the default value from a bash ${VAR:-default} expression.
func parseDefault(s string) string {
	if strings.Contains(s, ":-") {
		parts := strings.SplitN(s, ":-", 2)
		return strings.TrimRight(parts[1], "}")
	}
	return ""
}

func scriptExecString(s string) string {
	out, err := scriptExecValue(s)
	if err != nil || out == "" {
		return parseDefault(s)
	}
	return out
}

func scriptExecBool(s string) bool {
	out, err := scriptExecValue(s)
	if err != nil || out == "" {
		// Fall back to default from the expression (e.g. "false" from ${VAR:-false}).
		out = parseDefault(s)
	}
	b, err := strconv.ParseBool(out)
	if err != nil {
		return false
	}
	return b
}

func scriptExecArray(s string) string {
	items, err := scriptExecSlice(s)
	if err != nil || len(items) == 0 {
		return ""
	}
	return strings.Join(items, ",")
}

func scriptExecInt(s string) int {
	out, err := scriptExecValue(s)
	if err != nil || out == "" {
		out = parseDefault(s)
	}
	i, err := strconv.Atoi(out)
	if err != nil {
		return 0
	}
	return i
}
