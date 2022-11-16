// Package cliconfig cmd/skywire-cli/commands/config/auto.go
package cliconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/bitfield/script"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/netutil"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

const (
	nc = "\033[0m"
	red = "\033[0;31m"
	green = "\033[0;32m"
	yellow = "\033[0;33m"
	blue = "\033[1;34m"
	purple = "\033[0;35m"
	cyan = "\033[0;36m"
	bold = "\033[1m"
)

var (
	startedWithSystemd bool
)
/*
func init() {
	RootCmd.AddCommand(autoConfigCmd)
}

var autoConfigCmd = &cobra.Command{
	Use:   "auto",
	Short: "Automatically generate or update a config",
	Long: "\n  " + Short + "\n\n  A substituite for the package-level config management scripts\n  golang adaptation of skywire-autoconfig.sh & skywire.bat",
	PreRun: func(cmd *cobra.Command, _ []string) {
		// source the skyenv file
		if _, err := os.Stat(skyenv.SkyEnvs()); err == nil {
			if skyenv.OS == "win" {
				script.Exec(`call `+ skyenv.SkyEnvs()).Stdout
			} else {
				script.Exec(`source `+ skyenv.SkyEnvs()).Stdout
			}
			noautoconfig, exists := os.LookupEnv("NOAUTOCONFIG")
			if exists {
				if noautoconfig == "true" {
					internal.PrintOutput("autoconfiguration disabled.")
					os.Exit(0)
				}
			}
			if skyenv.OS != "win" {
				//root permissions required on linux
				euid := os.GetEnv("EUID")
				if euid != "0" {
					internal.PrintFatalError("root permissions required")
				}
				startedWithSystemd = script.Exec(`bash -c "[[ $(ps -eo pid,comm,cgroup | grep skywire) == *'system.slice'* ]] && echo 'true'"`).String
				if startedWithSystemd == true
			}
	},
	Run: func(cmd *cobra.Command, args []string) {
		if skyenv.OS == "linux" {
			if os.GetEnv("DMSGPTYTERM") != "1" && startedWithSystemd {
				//halt any running instance
				script.Exec(`bash -c "systemctl is-active --quiet skywire-visor && systemctl disable --now skywire-visor 2> /dev/null"`).Stdout
				script.Exec(`bash -c "systemctl is-active --quiet skywire-hypervisor && systemctl disable --now skywire-hypervisor 2> /dev/null"`).Stdout
			}
			script.Exec(`bash -c "systemctl is-active --quiet skywire-autoconfig && systemctl disable skywire-autoconfig 2> /dev/null"`).Stdout
		}
//		#recreate pacman logging
		func msg2(s string) {
			fmt.Printf("%s ->%s%s %s%s\n", cyan, nc, bold, s, nc)
		}
		func msg3(s string) {
			fmt.Printf("%s -->%s%s %s%s\n", blue, nc, bold, s, nc)
		}
		func errmsg1(s string) {
			fmt.Printf("%s>>> Error:%s%s %s%s\n", red, nc, bold, s, nc)
		}
		func warnmsg1(s string) {
			fmt.Printf("%s>>> Warning:%s%s %s%s\n", red, nc, bold, s, nc)
		}
		func errmsg2() {
			fmt.Printf("%s>>> FATAL:%s%s %s%s\n", red, nc, bold, s, nc)
		}

//		#generate config as root
		func configGen() {
		  //create by default the local hypervisor config if no config exists ; retain any hypervisor config which exists
			vconf =
			_, err := os.Stat(SkywireConfig())
			if err != nil ||  {


		  [[ (! -f /opt/skywire/skywire.json) || ($(cat /opt/skywire/skywire.json | grep -Po '"hypervisor":') != "") ]] &&	_is_hypervisor="-i"
		  #check for argument - remote pk or 0
		  # 0 as argument drops any remote hypervisors which were set in the configuration
		  # & triggers the creation of the local hyperisor configuration
			if [[ ${_1} == "0" ]]; then
		    _retain_hv=""
				unset _1
				_is_hypervisor="-i"
			fi
			# 1 as argument drops remote hypervisors and does not create the local hv config
		  	if [[ ${_1} == "1" ]]; then
		      _retain_hv=""
		  		unset _1
		  		_is_hypervisor=""
		  	fi
			# create the flag to set the remote hypervisor(s)
			if [[ ! -z ${_1} ]]; then
		    _retain_hv=""
				_hvpks="--hvpks ${_1}"	#shorthand flag: -j
				_is_hypervisor=""
			 fi
			##generate (hyper)visor configuration##
			# show config gen command used
			_msg3 "Generating skywire config with command:
		${_cyan}skywire-cli ${_yellow}config gen -bepr ${_retain_hv} ${_is_hypervisor} ${_public_rpc} ${_vpn_server} ${_test_env} ${_hvpks} ${_no_autoconnect} ${_is_public_visor}${_nc}"
		    skywire-cli config gen -bepr ${_retain_hv} ${_is_hypervisor} ${_public_rpc} ${_vpn_server} ${_test_env} ${_hvpks} ${_no_autoconnect} ${_is_public_visor} >> /dev/null 2>&1
		    if [[ ${?} != 0 ]]; then
		      #print the error!
		      skywire-cli config gen -bepr ${_retain_hv} ${_is_hypervisor} ${_public_rpc} ${_vpn_server} ${_test_env} ${_hvpks} ${_no_autoconnect} ${_is_public_visor}
		      _err=$?
		      _errmsg2 "error generating skywire config"
		      exit ${_err}
		    fi
			#logging check
			if [[ -f /opt/skywire/skywire.json ]]; then
				_msg3 "${_blue}Skywire${_nc} configuration updated
		config path: ${_purple}/opt/skywire/skywire.json${_nc}"
		  if [[ ! -f /etc/skywire-config.json ]]; then
		    _msg2 "backing up configuration to /etc/skywire-config.json"
		    cp -b /opt/skywire/skywire.json /etc/skywire-config.json
		  fi
			else
				_errmsg2 "expected config file not found at /opt/skywire/skywire.json"
				exit 100
			fi
		}

		#only use public rpc flag with env PUBLICRPC=1
		if [[ ( ${PUBLICRPC} -eq "1") ]]; then
		  _public_rpc="--publicrpc "
		fi
		#use public flag with env VISORISPUBLIC=1
		if [[ ( ${VISORISPUBLIC} -eq "1") ]]; then
		  _is_public_visor="--public "
		fi
		#use public flag with env NOAUTOCONNECT=1
		if [[ ( ${NOAUTOCONNECT} -eq "1") ]]; then
		  _no_autoconnect="--autoconn "
		fi
		#enable VPN server automatically on config re-gen with env VPNSERVER=1
		if [[ ${VPNSERVER} -eq "1" ]]; then
		  _vpn_server="--servevpn "
		fi
		#default to retaining hypervisors already set
		_retain_hv="-x"
		#use test deployment instead of production with env TESTENV=1
		if [[ ${TESTENV} -eq "1" ]]; then
			_test_env="--testenv"
		fi
		#check if >>this script<< is a child process of the systemd service i.e.:  run in dmsgpty terminal
		if [[ "${SYSTEMDCHILD}" -ne "1" ]]; then
			_now="--now"
		fi

		#root portion of the config
		_msg2 "Configuring skywire"
		#attempt to import config if none exists - i.e. import skybian config or restore config
		if [[ ! -f /opt/skywire/skywire.json ]]; then
			if [[ -f /etc/skywire-config.json ]]; then
		  		_warnmsg1 "Importing configuration from /etc/skywire-config.json"
		  		cp -b  /etc/skywire-config.json /opt/skywire/skywire.json
			fi
		fi
		#config generation
		_config_gen
		_svc=skywire
		if [[ $SKYBIAN == "true" ]]; then
		  _msg3 "Enabling ${_svc} service${_now/--/ and starting }..
		    systemctl enable ${_now} ${_svc}.service"
		systemctl enable ${_now} ${_svc}.service 2> /dev/null
		fi
		if [[ $DMSGPTYTERM == "1" ]]; then
			if [[ ${_now} != "--now" ]]; then
				_msg3 "Please restart ${_svc} service manually:
				${_red}systemctl restart ${_svc} 2> /dev/null${_nc}"
				exit 0
			else
				_msg3 "Please shut down your visor and start it again with:
				${_red}systemctl start ${_svc} 2> /dev/null${_nc}"
				exit 0
			fi
		fi
		#restart the service
			systemctl is-active --quiet ${_svc} && _msg3 "Restarting skywire.service..." && systemctl restart ${_svc} 2> /dev/null
		if ! systemctl is-active --quiet ${_svc} >/dev/null; then
			 _msg2 "Start the skywire service with:
			${_red}systemctl start ${_svc}${_nc}"
			exit 0
		fi
		_pubkey=$(skywire-cli visor pk -p | tail -n1)
		_msg2 "Visor Public Key:
		${_green}${_pubkey}${_nc}"
		if [[ $_is_hypervisor == "-i" ]]; then
			if [[ $(ps -o comm= -p $PPID) != "sshd" ]]; then
				_msg2 "Starting now on:\n${_red}http://127.0.0.1:8000${_nc}"
				_vpnurl=$(skywire-cli vpn url -p)
				_msg2 "Use the vpn:\n${_red}${_vpnurl}${_nc}"
			fi
			_hpvurl="Access hypervisor UI from local network here:"
			_lanips="$(ip addr show | grep -w inet | grep -v 127.0.0.1 | awk '{ print $2}' | cut -d "/" -f 1)"
			for _lanip in $_lanips
			do
				_hpvurl+="\n${_yellow}http://${_lanip}:8000${_nc}"
			done

			_msg2 "$_hpvurl"
			_welcome
			_msg2 "run the following command on OTHER NODES to set this one as the hypervisor:"
		    echo -e "${_cyan}skywire-autoconfig ${_yellow}${_pubkey}${_nc}"
		    _msg2 "to see this text again run: ${_cyan}skywire-autoconfig${_nc}"
		else
		  	_msg2 "${_blue}Skywire${_nc} starting in visor mode"
		    _msg2 "Visor Public Key: ${_green}${_pubkey}${_nc}"
		    _welcome
		fi

	},
}
*/
