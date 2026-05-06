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

### Ports ###############################################################
#	Note: when generating a test deployment config (-t / TESTENV=true),
#	all ports are automatically offset by +10000 to allow prod and test
#	visors to run simultaneously on the same machine.

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

### Skycoin embedded apps ###############################################
#	The skywire binary ships skycoin daemon + thin-client web wallet
#	as 'skywire skycoin daemon' / 'skywire skycoin web'. Both default
#	off in config-gen. The wallet's user-drop field is the security
#	knob to remember: it touches the operator's wallet directory
#	(~/.skycoin/wallets) and should run as the operator's UID — even
#	when the visor itself runs as _skywire.
#
#	Note: only one daemon and one wallet are configured by default.
#	Running multiple daemons (one per fibercoin chain) is supported
#	by the binary itself but requires editing the AppConfig array
#	by hand — the hypervisor UI's multi-instance app management
#	(see Apps tab → add) is the manageable path until tab-based
#	skycoin daemon controls land.

#--	Autostart skycoin daemon (full node, syncs the chain)
#SKYCOIND=true

#--	FIBER_TOML path. Empty = vanilla skycoin. Set to a fiber.toml
#	to make the daemon serve a fibercoin chain instead.
#SKYCOIND_FIBER_TOML='/path/to/fiber.toml'

#--	GUI API sets to enable on the daemon (comma-separated list).
#	Empty = the daemon's compiled-in defaults. See:
#		'skywire skycoin daemon --help' for the full list.
#	Common values: STATUS,WALLET,READ,TXN,BACKGROUND_SCANNER
#SKYCOIND_API_SETS='STATUS,WALLET,READ'

#--	Drop skycoin daemon to this user (POSIX setuid before exec).
#	Empty = run as the visor's own UID. The chain ends up under
#	~<user>/.skycoin/data.db, so picking the right user matters
#	for chain ownership across upgrades.
#SKYCOIND_USER='youruser'

#--	Autostart skycoin-web thin-client wallet
#SKYCOINWEB=true

#--	skycoin-web bind address (default 127.0.0.1:8002 — bumped
#	one up from skycoin's upstream default of 8001 because
#	skychat is already pinned at 127.0.0.1:8001 in skywire).
#SKYCOINWEBADDR='127.0.0.1:8002'

#--	Node URLs the wallet talks to. Bash array — set one per
#	fibercoin you want the wallet to multi-coin-browse. Empty
#	= upstream default (https://node.skycoin.com). For
#	local-daemon use, point at the daemon's bind:
#		SKYCOINWEBNODES=('http://127.0.0.1:6420')
#	For multi-chain:
#		SKYCOINWEBNODES=('http://127.0.0.1:6420' 'http://127.0.0.1:6421')
#SKYCOINWEBNODES=('')

#--	Wallet directory override. Empty = upstream default at
#	$HOME/.skycoin/wallets of whichever user the app runs as
#	(see SKYCOINWEBUSER below).
#SKYCOINWEBWALLET=''

#--	Drop skycoin-web to this user (POSIX setuid before exec).
#	Empty = run as the visor's own UID. Required when the visor
#	runs as _skywire and the wallet should access the operator's
#	~/.skycoin/wallets directory. The launcher only honours this
#	in external mode (Binary set on the AppConfig entry, which
#	the default skycoin-web entry already has).
#SKYCOINWEBUSER='youruser'

### Advanced Tuning #####################################################

#--	CLI RPC address (default localhost:3435)
#	Use 0.0.0.0:3435 for Docker/remote access
#CLIADDR='localhost:3435'

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

### Auto-Update (skywire-autoupdate package) ############################
#	These settings are only used by the skywire-update script
#	installed by the skywire-autoupdate package.

#--	Update channel:
#	"stable"  = latest commit where all CI tests passed (default)
#	"develop" = latest develop branch commit (may be untested)
#	"latest"  = latest tagged release version
#	"<hash>"  = pin to a specific commit hash
#UPDATE_CHANNEL=stable

#--	Docker deployment directory (for skywire-docker-update)
#	Set this to enable auto-updating docker-based deployment services.
#	The directory must contain a compose.yaml or docker-compose.yml.
#DEPLOY_DIR=''

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

### Auto-Update (skywire-autoupdate package) ############################
#	These settings are only used by the skywire-update script
#	installed by the skywire-autoupdate package.

#--	Update channel:
#	"stable"  = latest commit where all CI tests passed (default)
#	"develop" = latest develop branch commit (may be untested)
#	"<hash>"  = pin to a specific commit hash
#$UPDATE_CHANNEL='stable'

#--	Docker deployment directory (for skywire-docker-update)
#$DEPLOY_DIR=''

### Miscellaneous #######################################################

#--	Set secret key
#$SK=''

#--	Custom config version override
#$VERSION=''

#--	Set visor runtime log level.
#	Default is info ; uncomment for debug logging
#$LOGLVL='debug'
`
