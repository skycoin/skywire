// Package cliconfig cmd/skywire-cli/commands/config/envfiles.go
package cliconfig

const envfileLinux = `#
# /etc/skywire.conf
#
#########################################################################
#	SKYWIRE CONFIG TEMPLATE
#		Defaults for booleans are false
#		Uncomment to change default value
#########################################################################

### Installation path ###################################################

#--	Default config paths for the installer or package (system paths)
#PKGENV=true

#--	Default config paths for the current userspace
#USRENV=true

#--	service conf path override
#SVCCONF="services-config.json"

#--	dmsghttp config path override
#DMSGCONF="dmsghttp-config.json"

#--	Output path of the config file
#OUTPUT='./skywire-config.json'

#--	Set app bin_path
#BINPATH='./apps'

### Deployment ##########################################################

#--	Set custom service conf URLs
#SVCCONFADDR=('')

#--	Use test deployment
#TESTENV=true

#--	Use dmsghttp to connect to the production deployment ; overrides BESTPROTO=true
#DMSGHTTP=true

#--	Number of dmsg serverts to connect to (0 unlimits)
#MINDMSGSESS=8

#--	Automatically determine the best protocol (dmsg or http)
#	based on location to connect to the deployment servers
#BESTPROTO=true

### Transports ##########################################################

#--	Other Visors will automatically establish transports to this visor
#	requires port forwarding or public ip
#VISORISPUBLIC=true

#--	Disable auto-transports to public visors from this visor
#DISABLEPUBLICAUTOCONN=true

#-- Add transport setup public keys
#TPSETUPPKS('')

#--	Enable transport discovery data sync (bandwidth/latency)
#SYNCTPDDATA=true

### Ports ###############################################################

#- set port for UDP connections / SUDPH transports
#SUDPHPORT=0

#- set port for TCP connections / STCPR or STCP transports
#STCPRPORT=0

### Routing #############################################################

#-- Add route setup-node public keys
#ROUTESETUPPKS('')

#--	Enable local route calculation (instead of using route finder)
#CALCULATEROUTES=true

### Remote Access #######################################################

#--	Set remote hypervisor public keys
#HYPERVISORPKS=('')

#--	Grant access to pseudoterminal (pty) for public keys
#DMSGPTYPKS('')

### Survey Access #######################################################

#--	Grant access for survey collection to these public keys
#SURVEYPKS('')

### Hypervisor UI #######################################################

#--	Start the hypervisor interface for this visor
#ISHYPERVISOR=true

### Rewards #############################################################

#--	Skycoin reward address or xpub key
#REWARDSKYADDR=''

### Apps ################################################################

#--	Display the node ip in the service discovery
#	for any public services this visor is running
#DISPLAYNODEIP=true

#--	Autostart vpn server for this visor
#VPNSERVER=false

#--	Set server public key for proxy client to connect to
#PROXYCLIENTPK=''

#--	Enable autostart of the proxy client
#STARTPROXYCLIENT=true

#--	Autostart proxy server
#PROXYSERVER=false

#--	Autostart skychat
#SKYCHAT=false

#--	Skychat local address
#SKYCHATADDR=':8001'

#--	Whitelist public keys for the proxy server (empty = allow all)
#PROXYSERVERWL=('')

#--	Set VPN client killswitch
#VPNKS=true

#--	Set vpn server public key for the vpn client to use
#ADDVPNPK=''

#--	Whitelist public keys for the vpn server (empty = allow all)
#VPNSERVERWL=('')

#--	Change secure mode status of vpn server
#VPNSEVERSECURE=''

#--	Set VPN Server network interface - i.e. eth0
#VPNSEVERNETIFC=''

### Advanced Tuning #####################################################

#--	Hypervisor HTTP address (default :8000)
#HVHTTPADDR=':8000'

#--	STUN servers for NAT traversal
#STUNSERVERS=('')

#--	Graceful shutdown timeout (default 10s)
#SHUTDOWNTIMEOUT='10s'

#--	Public visor registration timeout (default 10m)
#REGTIMEOUT='10m'

#--	Public visor max transports (default 1000)
#MAXTRANSPORTS=1000

#--	Number of parallel mux routes per connection (default 0)
#MUXROUTES=0

### Miscellaneous #######################################################

#--	Set secret key
#SK=''

#--	Custom config version override
#VERSION=''

#--	Set visor runtime log level.
#	Default is info ; uncomment for debug logging
#LOGLVL=debug

`

const envfileWindows = `#
# C:\ProgramData\skywire.conf
#
#########################################################################
#	SKYWIRE CONFIG TEMPLATE
#		Defaults for booleans are false
#		Uncomment to change default value
#########################################################################

### Installation path ###################################################

#--	Default config paths for the installer or package (system paths)
#$PKGENV=$true

#--	Default config paths for the current userspace
#$USRENV=$true

#--	service conf path override
#$SVCCONF="services-config.json"

#--	dmsghttp config path override
#$DMSGCONF="dmsghttp-config.json"

#--	Output path of the config file
#$OUTPUT='C:\\ProgramData\\skywire-config.json'

#--	Set app bin_path
#$BINPATH='C:\\ProgramData\\apps'

### Deployment ##########################################################

#--	Set custom service conf URLs
#$SVCCONFADDR=@('')

#--	Use test deployment
#$TESTENV=$true

#--	Use dmsghttp to connect to the production deployment ; overrides BESTPROTO=$true
#$DMSGHTTP=$true

#--	Number of dmsg servers to connect to (0 unlimits)
#$MINDMSGSESS=8

#--	Automatically determine the best protocol (dmsg or http)
#	based on location to connect to the deployment servers
#$BESTPROTO=$true

### Transports ##########################################################

#--	Other Visors will automatically establish transports to this visor
#	requires port forwarding or public IP
#$VISORISPUBLIC=$true

#--	Disable auto-transports to public visors from this visor
#$DISABLEPUBLICAUTOCONN=$true

#--	Add transport setup public keys
#$TPSETUPPKS=@('')

#--	Enable transport discovery data sync (bandwidth/latency)
#$SYNCTPDDATA=$true

### Ports ###############################################################

#- set port for UDP connections / SUDPH transports
#$SUDPHPORT=0

#- set port for TCP connections / STCPR or STCP transports
#$STCPRPORT=0

### Routing #############################################################

#--	Add route setup-node public keys
#$ROUTESETUPPKS=@('')

#--	Enable local route calculation (instead of using route finder)
#$CALCULATEROUTES=$true

### Remote Access #######################################################

#--	Set remote hypervisor public keys
#$HYPERVISORPKS=@('')

#--	Grant access to pseudoterminal (pty) for public keys
#$DMSGPTYPKS=@('')

### Survey Access #######################################################

#--	Grant access for survey collection to these public keys
#$SURVEYPKS=@('')

### Hypervisor UI #######################################################

#--	Start the hypervisor interface for this visor
#$ISHYPERVISOR=$true

### Rewards #############################################################

#--	Skycoin reward address or xpub key
#$REWARDSKYADDR=''

### Apps ################################################################

#--	Display the node IP in the service discovery
#	for any public services this visor is running
#$DISPLAYNODEIP=$true

#--	Autostart vpn server for this visor
#$VPNSERVER=$false

#--	Set server public key for proxy client to connect to
#$PROXYCLIENTPK=''

#--	Enable autostart of the proxy client
#$STARTPROXYCLIENT=$true

#--	Autostart proxy server
#$PROXYSERVER=$false

#--	Autostart skychat
#$SKYCHAT=$false

#--	Skychat local address
#$SKYCHATADDR=':8001'

#--	Whitelist public keys for the proxy server (empty = allow all)
#$PROXYSERVERWL=@('')

#--	Set VPN client killswitch
#$VPNKS=$true

#--	Set VPN server public key for the VPN client to use
#$ADDVPNPK=''

#--	Whitelist public keys for the vpn server (empty = allow all)
#$VPNSERVERWL=@('')

#--	Change secure mode status of VPN server
#$VPNSEVERSECURE=''

#--	Set VPN Server network interface, e.g., 'Ethernet'
#$VPNSEVERNETIFC=''

### Advanced Tuning #####################################################

#--	Hypervisor HTTP address (default :8000)
#$HVHTTPADDR=':8000'

#--	STUN servers for NAT traversal
#$STUNSERVERS=@('')

#--	Graceful shutdown timeout (default 10s)
#$SHUTDOWNTIMEOUT='10s'

#--	Public visor registration timeout (default 10m)
#$REGTIMEOUT='10m'

#--	Public visor max transports (default 1000)
#$MAXTRANSPORTS=1000

#--	Number of parallel mux routes per connection (default 0)
#$MUXROUTES=0

### Miscellaneous #######################################################

#--	Set secret key
#$SK=''

#--	Custom config version override
#$VERSION=''

#--	Set visor runtime log level.
#	Default is info ; uncomment for debug logging
#$LOGLVL='debug'
`
