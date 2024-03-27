// Package cliconfig cmd/skywire-cli/commands/config/root.go
package cliconfig

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/bitfield/script"
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
	path                        string
	noFetch                     bool
	noDefaults                  bool
	stcprPort                   int
	sudphPort                   int
	sk                          cipher.SecKey
	output                      string
	confPath                    string
	configName                  string //nolint Note: configName used, but golangci-lint marked it unused in wrong
	isStdout                    bool
	isSquash                    bool
	isRegen                     bool
	isRetainHypervisors         bool
	isTestEnv                   bool
	pText                       string
	isPkgEnv                    bool
	isUsrEnv                    bool
	isHypervisor                bool
	hypervisorPKs               string
	dmsgptyWlPKs                string
	surveyWhitelistPKs          string
	routeSetupNodes             string
	transportSetupPKs           string
	isDmsgHTTP                  bool
	minDmsgSess                 int
	isVpnServerEnable           bool
	isDisableAuth               bool
	isEnableAuth                bool
	selectedOS                  string
	disableApps                 string
	isBestProtocol              bool
	serviceConfURL              string
	services                    visorconfig.Services
	servicesConfig              servicesConf
	isForce                     bool
	isHide                      bool
	isAll                       bool
	isOutUnset                  bool
	ver                         string
	isRoot                      = visorconfig.IsRoot()
	svcConf                     = strings.ReplaceAll(utilenv.ServiceConfAddr, "http://", "")     //visorconfig.DefaultServiceConfAddr
	testConf                    = strings.ReplaceAll(utilenv.TestServiceConfAddr, "http://", "") //visorconfig.DefaultServiceConfAddr
	gHiddenFlags                []string
	uHiddenFlags                []string
	binPath                     string
	logLevel                    string
	isPkg                       bool
	input                       string
	isUpdateEndpoints           bool
	addHypervisorPKs            string
	isResetHypervisor           bool
	setVPNClientKillswitch      string
	addVPNClientSrv             string
	addVPNClientPasscode        string
	isResetVPNclient            bool
	addVPNServerPasscode        string
	setVPNServerSecure          string
	setVPNServerAutostart       string
	setVPNServerNetIfc          string
	isResetVPNServer            bool
	addSkysocksClientSrv        string
	isResetSkysocksClient       bool
	skysocksPasscode            string
	isResetSkysocks             bool
	setPublicAutoconnect        string
	minHops                     int
	isUsr                       bool
	isPublic                    bool
	disablePublicAutoConn       bool
	isDisplayNodeIP             bool
	addExampleApps              bool
	enableProxyClientAutostart  bool
	disableProxyServerAutostart bool
	proxyServerPass             string
	proxyClientPass             string
	configServicePath           string
	dmsgHTTPPath                string
	snConfig                    bool
)

// RootCmd contains commands that interact with the config of local skywire-visor
var RootCmd = &cobra.Command{
	Use:   "config",
	Short: "Generate or update a skywire config",
	Long:  "Generate or update the config file used by skywire-visor.",
}

type servicesConf struct { //nolint
	Test visorconfig.Services `json:"test"`
	Prod visorconfig.Services `json:"prod"`
}

func scriptExecString(s string) string {
	if visorconfig.OS == "windows" {
		var variable, defaultvalue string
		if strings.Contains(s, ":-") {
			parts := strings.SplitN(s, ":-", 2)
			variable = parts[0] + "}"
			defaultvalue = strings.TrimRight(parts[1], "}")
		} else {
			variable = s
			defaultvalue = ""
		}
		out, err := script.Exec(fmt.Sprintf(`powershell -c '$SKYENV = "%s"; if ($SKYENV -ne "" -and (Test-Path $SKYENV)) { . $SKYENV }; echo %s"`, skyenvfile, variable)).String()
		if err == nil {
			if (out == "") || (out == variable) {
				return defaultvalue
			}
			return strings.TrimRight(out, "\n")
		}
		return defaultvalue
	}
	z, err := script.Exec(fmt.Sprintf(`bash -c 'SKYENV=%s ; if [[ $SKYENV != "" ]] && [[ -f $SKYENV ]] ; then source $SKYENV ; fi ; printf "%s"'`, skyenvfile, s)).String()
	if err == nil {
		return strings.TrimSpace(z)
	}
	return ""
}

func scriptExecBool(s string) bool {
	if visorconfig.OS == "windows" {
		var variable string
		if strings.Contains(s, ":-") {
			parts := strings.SplitN(s, ":-", 2)
			variable = parts[0] + "}"
		} else {
			variable = s
		}
		out, err := script.Exec(fmt.Sprintf(`powershell -c '$SKYENV = "%s"; if ($SKYENV -ne "" -and (Test-Path $SKYENV)) { . $SKYENV }; echo %s"`, skyenvfile, variable)).String()
		if err == nil {
			if (out == "") || (out == variable) {
				return false
			}
			b, err := strconv.ParseBool(strings.TrimSpace(strings.TrimRight(out, "\n")))
			if err == nil {
				return b
			}
		}
		return false
	}
	z, err := script.Exec(fmt.Sprintf(`bash -c 'SKYENV=%s ; if [[ $SKYENV != "" ]] && [[ -f $SKYENV ]] ; then source $SKYENV ; fi ; printf "%s"'`, skyenvfile, s)).String()
	if err == nil {
		b, err := strconv.ParseBool(z)
		if err == nil {
			return b
		}
	}

	return false
}

func scriptExecArray(s string) string {
	if visorconfig.OS == "windows" {
		variable := s
		if strings.Contains(variable, "[@]}") {
			variable = strings.TrimRight(variable, "[@]}")
			variable = strings.TrimRight(variable, "{")
		}
		out, err := script.Exec(fmt.Sprintf(`powershell -c '$SKYENV = "%s"; if ($SKYENV -ne "" -and (Test-Path $SKYENV)) { . $SKYENV }; foreach ($item in %s) { Write-Host $item }'`, skyenvfile, variable)).Slice()
		if err == nil {
			if len(out) != 0 {
				return ""
			}
			return strings.Join(out, ",")
		}
	}
	y, err := script.Exec(fmt.Sprintf(`bash -c 'SKYENV=%s ; if [[ $SKYENV != "" ]] && [[ -f $SKYENV ]] ; then source $SKYENV ; fi ; for _i in %s ; do echo "$_i" ; done'`, skyenvfile, s)).Slice()
	if err == nil {
		return strings.Join(y, ",")
	}
	return ""
}

func scriptExecInt(s string) int {
	if visorconfig.OS == "windows" {
		var variable string
		if strings.Contains(s, ":-") {
			parts := strings.SplitN(s, ":-", 2)
			variable = parts[0] + "}"
		} else {
			variable = s
		}
		out, err := script.Exec(fmt.Sprintf(`powershell -c '$SKYENV = "%s"; if ($SKYENV -ne "" -and (Test-Path $SKYENV)) { . $SKYENV }; echo %s"`, skyenvfile, variable)).String()
		if err == nil {
			if (out == "") || (out == variable) {
				return 0
			}
			i, err := strconv.Atoi(strings.TrimSpace(strings.TrimRight(out, "\n")))
			if err == nil {
				return i
			}
			return 0
		}
		return 0
	}
	z, err := script.Exec(fmt.Sprintf(`bash -c 'SKYENV=%s ; if [[ $SKYENV != "" ]] && [[ -f $SKYENV ]] ; then source $SKYENV ; fi ; printf "%s"'`, skyenvfile, s)).String()
	if err == nil {
		if z == "" {
			return 0
		}
		i, err := strconv.Atoi(z)
		if err == nil {
			return i
		}
	}
	return 0
}
