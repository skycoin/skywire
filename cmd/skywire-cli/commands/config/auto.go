// Package cliconfig cmd/skywire-cli/commands/config/auto.go
package cliconfig

import (
	"fmt"
	"os"
	"strings"

	"github.com/bitfield/script"
	"github.com/spf13/cobra"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skyenv"
//	"github.com/skycoin/skywire/pkg/visor/visorconfig"
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
	isStartedWithSystemd bool
	cmds string
	isRunScript bool
)

func init() {
	RootCmd.AddCommand(autoConfigCmd)
	autoConfigCmd.Flags().BoolVarP(&isRunScript, "script", "s", false, "run the skywire-autoconfig script")

}

var autoConfigCmd = &cobra.Command{
	Use:   "auto [ 0 | 1 | <public-key> ]",
	Short: "Automatically generate or update a config",
	Long: "\n  Automatically generate or update a config\n\n  A substituite for the package-level config management scripts\n  golang adaptation of skywire-autoconfig.sh\n\n 0 as argument drops any remote hypervisors which were set in the configuration\n 1 as argument drops remote hypervisors and does not create the local hv config\n <public-key> as argument sets the remote hypervisor",
	PreRun: func(cmd *cobra.Command, _ []string) {
//		if !isRunScript {
		// source the skyenv file
		if _, err := os.Stat(skyenv.SkyEnvs()); err == nil {
			if skyenv.OS == "win" {
				cmds = `call `+ skyenv.SkyEnvs()
				_, _ = script.Exec(cmds).Stdout() // noliint:errcheck
			} else {
				cmds = `bash -c "source `+ skyenv.SkyEnvs() + `"`
				_, _ = script.Exec(cmds).Stdout() // noliint:errcheck
			}
		}
			noautoconfig, exists := os.LookupEnv("NOAUTOCONFIG")
			if exists {
				if noautoconfig == "true" {
					internal.PrintOutput(cmd.Flags(), "", "autoconfiguration disabled.")
					os.Exit(0)
				}
			}
			if skyenv.OS != "win" {
				//root permissions required on linux
				euid, _ := script.Exec(`bash -c "echo -e ${EUID}"`).String()
				if euid != "0\n" {
					internal.PrintFatalError(cmd.Flags(), fmt.Errorf("root permissions required"))
				}
			    if s, err := script.Exec(`bash -c "[[ $(ps -eo pid,comm,cgroup | grep skywire) == *'system.slice'* ]] && printf 'true'"`).String(); err == nil {
					if s == "true" {
						isStartedWithSystemd = true
					}
			}
		}
	//}
	},
	Run: func(cmd *cobra.Command, args []string) {
		if skyenv.OS == "linux" {
//not working currently
//			if isRunScript {
//				_, _ = script.Exec(skywireautoconfig).Stdout()
//				os.Exit(0)
//			}
			// if the command is run in the DMSGPTY terminal, the process will be interrupted before the command can complete
			// similarly, if this command is run as a child process of the systemd service, it will be killed by systemd before it completes
			// DMSGPTYTERM=1 is exported by the dmsgpty terminal and can be detected by this process
			if os.Getenv("DMSGPTYTERM") != "1" && isStartedWithSystemd {
				//halt any running instance of defunct systemd services no longer provided by the package / installation
				_, _ = script.Exec(`bash -c "systemctl is-active --quiet skywire-visor && systemctl disable --now skywire-visor 2> /dev/null"`).Stdout()
				_, _ = script.Exec(`bash -c "systemctl is-active --quiet skywire-hypervisor && systemctl disable --now skywire-hypervisor 2> /dev/null"`).Stdout()
			}
			_, _ = script.Exec(`bash -c "systemctl is-active --quiet skywire-autoconfig && systemctl disable skywire-autoconfig 2> /dev/null"`).Stdout()
		}
		 //create by default the local hypervisor config if no config exists ; retain any hypervisor config which exists
		_, err := os.Stat(skyenv.SkywireConfig())
		if err != nil  {
			_, err := os.Stat(` /etc/` + skyenv.ConfigName)
			if err == nil  {
				fmt.Printf("%s", warnmsg1("Importing configuration from /etc/skywire-config.json"))
				_,_ = script.Exec(`cp `+ ` /etc/` + skyenv.ConfigName + " " + skyenv.SkywireConfig()).Stdout()
			}
		}

//		_, err = os.Stat(skyenv.SkywireConfig())
//		if err == nil  {

//			if vconf, err := visorconfig.ReadFile(skyenv.SkywireConfig()); err != nil {
//				fmt.Printf("test")
//				if vconf.Hypervisor != nil {
					isHypervisor = true
//				}
//			}
//		}

		// retain remote hypervisors by default
		isRetainHypervisors = true
		 // #check for argument - remote pk, 0, or 1
		 // # 0 as argument drops any remote hypervisors which were set in the configuration
		 // # & triggers the creation of the local hyperisor configuration
		 if len(args) > 0 {
			 if args[0] == "0" {
				 isRetainHypervisors = false
				 isHypervisor = true
			 }
			 // # 1 as argument drops remote hypervisors and does not create the local hv config
			 if args[0] == "1" {
				 isRetainHypervisors = false
				 isHypervisor = false
			 }
		// # validate public key provided as argument
		var pk cipher.PubKey
		if args[0] != "0" && args[0] != "1" && args[0] != "" && args[0] != " " && args[0] != "\n" {
			internal.Catch(cmd.Flags(), pk.Set(args[0]))
			hypervisorPKs = pk.String()
			isRetainHypervisors = false
			isHypervisor = false
		}
	}
		//# show config gen command used
		// defaults
		// -b --bestproto dmsg or direct based on location
		// -e auth enable authorization for hypervisor
		// -p pkg package defaults
		var cmdslc []string
		confgen := fmt.Sprintf(`%sskywire-cli %sconfig gen`, cyan, yellow)
		cmdslc = append(cmdslc, fmt.Sprintf(` -b`))
		cmdslc = append(cmdslc, fmt.Sprintf(` -e`))
		cmdslc = append(cmdslc, fmt.Sprintf(` -p`))
		cmdslc = append(cmdslc, fmt.Sprintf(` -r`))
		if isHypervisor {
			cmdslc = append(cmdslc, fmt.Sprintf(` -i`))
		}
		// retain hypervisors when regenerating config
		if isRetainHypervisors {
			cmdslc = append(cmdslc, fmt.Sprintf(` -x`))
		}
		// remote hypervisor public key
		if hypervisorPKs != "" {
			cmdslc = append(cmdslc, fmt.Sprintf(` -j %s`, hypervisorPKs))
		}
		// visor appears in the service discovery for service type visor
		if os.Getenv("VISORISPUBLIC") == "1" {
		cmdslc = append(cmdslc, fmt.Sprintf(` --public`))
		}
		// disable autoconnect to public visors
		if os.Getenv("NOAUTOCONNECT") == "1" {
		cmdslc = append(cmdslc, fmt.Sprintf(` --autoconn`))
		}
		// default to auto start vpn server
		if os.Getenv("VPNSERVER") == "1" {
		cmdslc = append(cmdslc, fmt.Sprintf(` --servevpn`))
		}
		// #use test deployment instead of production with env TESTENV=1
		if os.Getenv("TESTENV") == "1" {
		cmdslc = append(cmdslc, fmt.Sprintf(` --testenv`))
		}
		fmt.Printf("%s", mesg2("Configuring skywire"))
		fmt.Printf("%s", mesg3(fmt.Sprintf("Generating skywire config with command:\n	%s%s%s", confgen, strings.Join(cmdslc, ""), nc)))
		// hide the output from config gen
		isHide = true
		//genconfigcmd := genConfigCmd
		//genconfigcmd.SetArgs(cmdslc)
		//##generate visor configuration##
		//genconfigcmd.Execute()
		genconfigcmd := "skywire-cli config gen " + strings.Join(cmdslc, "") + " -w"
		genconfigcmd1 := "echo \"" + genconfigcmd + "\"" + genconfigcmd + " >> /dev/null 2>&1 ; [[ ${?} != 0 ]] && " + genconfigcmd
		genconfigcmd1 = `bash -c '` + genconfigcmd1 + `'`

		_, _ = script.Exec(genconfigcmd1).Stdout()

		fmt.Printf("%s", mesg3(fmt.Sprintf("%sSkywire%s configuration updated\nconfig path: %s/opt/skywire/skywire.json%s", blue, nc, purple, nc)))
		if skyenv.OS == "linux" {
			_, err := os.Stat("/etc/skywire-config.json")
			if err != nil  {
				fmt.Printf("%s", mesg2("backing up configuration to /etc/skywire-config.json"))
				cmds = `bash -c ' set -x ; cp `+ skyenv.SkywireConfig() + ` /etc/` + skyenv.ConfigName + "'"
				_, _ = script.Exec(cmds).Stdout()
			}
		}
		// #check if >>this script<< is a child process of the systemd service i.e.:  run in dmsgpty terminal
		var now string
		if isStartedWithSystemd {
			now="--now"
		}

		var svc string
		svc = "skywire"
		cmds = fmt.Sprintf(`bash -c 'systemctl enable %s %s'`, now, svc)
		//start the service on ${SKYBIAN} == "true"
		if os.Getenv("SKYBIAN") == "true" {
			fmt.Printf("%s", mesg3(fmt.Sprintf("Enabling %s service%s..\n %s", svc, strings.Replace(now, "--", " and starting", -1), cmds)))
			cmds += `  2> /dev/null'`
			_, _ = script.Exec(cmds).Stdout()
		}
		if os.Getenv("DMSGPTYTERM") == "1" {
			if !isStartedWithSystemd {
				cmds = fmt.Sprintf("systemctl restart %s 2> /dev/null", svc)
				fmt.Printf("%s", mesg3(fmt.Sprintf("Please restart %s service manually:\n		%s%s%s", svc, red, cmd, nc)))
				os.Exit(0)
			} else {
				cmds = fmt.Sprintf("systemctl start %s 2> /dev/null", svc)
				fmt.Printf("%s", mesg3(fmt.Sprintf("Please shut down your visor and start it again with:\n		%s%s%s", svc, red, cmd, nc)))
				os.Exit(0)
			}
		}
		//#restart the service
		cmds = fmt.Sprintf(`bash -c 'systemctl is-active --quiet %s && %s && systemctl restart %s 2> /dev/null'`, svc, fmt.Sprintf("printf \"%s\"", mesg3(fmt.Sprintf("Restarting %s.service...", svc))), svc)
		script.Exec(cmds).Stdout()
		cmds = fmt.Sprintf(`bash -c 'if ! systemctl is-active --quiet %s >/dev/null; then printf "%s" ; exit 1 ; fi'`, svc, mesg2(fmt.Sprintf("Start the %s service with:\n	%ssystemctl start %s%s", svc, red, svc, nc)))
		//fmt.Printf(cmds)
		_, err = script.Exec(cmds).Stdout()
		if err != nil  {
			os.Exit(0)
		}

		cmds = fmt.Sprintf(`bash -c "skywire-cli visor pk -p | tail -n1"`)
		publickey, err := script.Exec(cmds).String()
		if err != nil  {
			os.Exit(1)
		}
		fmt.Printf("%s", mesg2(fmt.Sprintf("Visor Public Key:\n%s%s%s", green, publickey, nc)))
		if isHypervisor {
			cmds = fmt.Sprintf(`bash -c "[[ $(ps -o comm= -p $PPID) != 'sshd' ]] && printf 'true'"`)
			if sshd, err := script.Exec(cmds).String(); err == nil {
				if sshd == "true" { //when this command is run in ssh session its pointless to print the interface on localhost
					fmt.Printf("%s", mesg2(fmt.Sprintf("Starting now on:\n%shttp://127.0.0.1:8000%s", red, nc)))
				}
			}
			cmds = fmt.Sprintf(`bash -c "skywire-cli vpn url -p"`)
			if vpnurl, err := script.Exec(cmds).String(); err == nil {
				fmt.Printf("%s", mesg2(fmt.Sprintf("Use the vpn:\n%s%s%s", red, strings.TrimSuffix(vpnurl, "\n"), nc)))
			}
			hpvurl := "Access hypervisor UI from local network here:"
			cmds = fmt.Sprintf(`bash -c "ip addr show | grep -w inet | grep -v 127.0.0.1 | awk '{ print $2}' | cut -d \"/\" -f 1"`)
			lanips, _ := script.Exec(cmds).String()//; err == nil {
				lanip := strings.Split(lanips, "\n")
				for _, i := range lanip {
					if i != "" {
						hpvurl += fmt.Sprintf("\n%shttp://%s:8000%s", yellow, i, nc)
					}
				}
				fmt.Printf("%s", mesg2(fmt.Sprintf("%s",hpvurl)))
				fmt.Printf("%s", mesg2(fmt.Sprintf("support:\n%shttps://t.me/skywire%s", blue, nc)))
			fmt.Printf("%s", mesg2("run the following command on OTHER NODES to set this one as the hypervisor:"))
		    fmt.Printf("%s", fmt.Sprintf("%sskywire-cli config auto %s%s%s\n", cyan, yellow, publickey, nc))
		    fmt.Printf("%s", mesg2(fmt.Sprintf("to see this text again run: %sskywire-cli config auto%s\n", cyan, nc)))
		} else {
		  	fmt.Printf("%s", mesg2(fmt.Sprintf("%sSkywire%s starting in visor mode", blue, nc)))
		    fmt.Printf("%s", mesg2(fmt.Sprintf("Visor Public Key: %s%s%s", green, publickey, nc)))
		}
	},
}
//		#recreate pacman logging
		func mesg2(s string) string {
			return fmt.Sprintf("%s ->%s%s %s%s\n", cyan, nc, bold, s, nc)
		}
		func mesg3(s string) string {
			return fmt.Sprintf("%s -->%s %s%s\n", blue, nc, s, nc)
		}
		func errmsg1(s string) string {
			return fmt.Sprintf("%s>>> Error:%s%s %s%s\n", red, nc, bold, s, nc)
		}
		func warnmsg1( s string) string {
			return fmt.Sprintf("%s>>> Warning:%s%s %s%s\n", red, nc, bold, s, nc)
		}
		func errmsg2(s string) string {
			return fmt.Sprintf("%s>>> FATAL:%s%s %s%s\n", red, nc, bold, s, nc)
		}


const skywireautoconfig string = `#!/bin/bash
#/opt/skywire/scripts/skywire-autoconfig
#skywire autoconfiguration script for debian & archlinux packages
#source the skyenv file if it exists - provided by the skybian package or the user
[[ -f /etc/profile.d/skyenv.sh ]] && source /etc/profile.d/skyenv.sh
#set NOAUTOCONFIG=true to avoid running the script in the postinstall
if [[ ${NOAUTOCONFIG} == true ]]; then
  #unset the env
  NOAUTOCONFIG=''
  echo "autoconfiguration disabled. to configure and start skywire run: skywire-autoconfig"
  exit 0
fi
#check for root
if [[ $EUID -ne 0 ]]; then
	echo "root permissions required"
	exit 1
fi
#grant network permissions to the vpn app binaries
setcap cap_net_admin+ep /opt/skywire/apps/vpn-client
setcap cap_net_admin+ep /opt/skywire/apps/vpn-server
# determine if skywire is running via systemd
if [[ $(ps -eo pid,comm,cgroup | grep skywire) == *"system.slice"* ]]; then
WSYSTEMD=1
fi
#root portion of the configuration
if [[ $DMSGPTYTERM -ne "1" ]] && [[ $WSYSTEMD -eq "1" ]]; then
	#halt any running instance
	systemctl is-active --quiet skywire-visor && systemctl disable --now skywire-visor 2> /dev/null
	systemctl is-active --quiet skywire-hypervisor && systemctl disable --now skywire-hypervisor 2> /dev/null
fi
systemctl is-active --quiet skywire-autoconfig && systemctl disable skywire-autoconfig 2> /dev/null

#make the logging of this script colorful
_nc='\033[0m'
_red='\033[0;31m'
_green='\033[0;32m'
_yellow='\033[0;33m'
_blue='\033[1;34m'
_purple='\033[0;35m'
_cyan='\033[0;36m'
_bold='\033[1m'
##set the argument to pass into functions##
_1=${1}
#recreate pacman logging
_msg2() {
	(( QUIET )) && return
	local mesg=$1; shift
	printf "${_cyan} ->${_nc}${_bold} ${mesg}${_nc}\n" "$@"
}
_msg3() {
(( QUIET )) && return
local mesg=$1; shift
printf "${_blue} -->${_nc}${BOLD} ${mesg}${_nc}\n" "$@"
}
_errmsg1() {
	(( QUIET )) && return
	local mesg=$1; shift
	printf "${_red}>>> Error:${_nc}${_bold} ${mesg}${_nc}\n" "$@"
}
_warnmsg1() {
	(( QUIET )) && return
	local mesg=$1; shift
	printf "${_red}>>> Warning:${_nc}${_bold} ${mesg}${_nc}\n" "$@"
}
_errmsg2() {
	(( QUIET )) && return
	local mesg=$1; shift
	printf "${_red}>>> FATAL:${_bold} ${mesg}${_nc}\n" "$@"
}
#helpful text
_welcome(){
	if [[ $(uname -m) == *"arm"* ]]; then
		_msg2 "register your public key:"
		_msg2 "${_blue}https://whitelist.skycoin.com/${_nc}"
		_msg2 "track uptime:"
		_msg2 "${_blue}http://ut.skywire.skycoin.com/uptimes${_nc}"
	fi
	_msg2 "support:
${_blue}https://t.me/skywire${_nc}"
}
#generate config as root
_config_gen() {
  # remove any existing symlink
  [[ -f /opt/skywire/skywire-visor.json ]] && rm /opt/skywire/skywire-visor.json
  #create by default the local hypervisor config if no config exists ; and retain any hypervisor config which exists
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
`
