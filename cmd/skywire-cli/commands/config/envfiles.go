// Package cliconfig cmd/skywire-cli/commands/config/envfiles.go c4-vis-cli
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

#--	Use dmsghttp to connect to the production deployment
#DMSGHTTP=true

#--	Number of dmsg serverts to connect to (0 unlimits)
#MINDMSGSESS=8

### Transports ##########################################################

#--	Other Visors will automatically establish transports to this visor
#	requires port forwarding or public ip
#VISORISPUBLIC=true

#--	Disable auto-transports to public visors from this visor
#DISABLEPUBLICAUTOCONN=true

#-- Add transport setup public keys
#TPSETUPPKS=('')

### Ports ###############################################################
#	Note: when generating a test deployment config (-t / TESTENV=true),
#	all ports are automatically offset by +10000 to allow prod and test
#	visors to run simultaneously on the same machine.

#- set port for UDP connections / SUDPH transports
#SUDPHPORT=0

#- set port for TCP connections / STCPR or STCP transports
#STCPRPORT=0

#- set ONE shared master port for all transport types (stcpr+WS on <port>/tcp,
#- quic+sudph+wt+webrtc on <port>/udp). Overrides SUDPHPORT/STCPRPORT unless those
#- are set to break a type out onto its own port. 0 = per-type ports.
#TRANSPORTPORT=0

### Routing #############################################################

#-- Add route setup-node public keys
#ROUTESETUPPKS=('')

#--	Enable local route calculation (instead of using route finder)
#CALCULATEROUTES=true

### Remote Access #######################################################

#--	Set remote hypervisor public keys
#HYPERVISORPKS=('')

#--	Grant access to pseudoterminal (pty) for public keys
#DMSGPTYPKS=('')

### Survey Access #######################################################

#--	Grant access for survey collection to these public keys
#SURVEYPKS=('')

### Hypervisor UI #######################################################

#--	Start the hypervisor interface for this visor
#ISHYPERVISOR=true

#--	Hypervisor web-UI listen address (host:port). Default ':8000' = all
#	interfaces (reachable on the LAN at http://<this-host-ip>:8000).
#	Use '127.0.0.1:8000' to restrict the UI to localhost, or pin a
#	specific LAN IP, e.g. '192.168.0.2:8000'.
#HVHTTPADDR=':8000'

#--	Expose an unauthenticated GET /api/pk on the hypervisor (returns this
#	visor's public key). Off by default; skybian / Arch-ARM image builds
#	set it so a freshly-imaged board can be discovered on the LAN.
#ENABLEPKENDPOINT=true

#--	Embedded LAN/WAN DMSG server is always on whenever ISHYPERVISOR=true.
#	Managed visors relay through this hypervisor instead of public DMSG
#	servers. The two knobs below control the WAN-reachability path; see
#	the comments for when to set them.

#--	Pin the DMSG server's TCP port for stable WAN reachability. Default 0
#	= OS-assigned at runtime (changes every restart — fine for LAN-only,
#	bad for remote visors that need a stable address). Set to a chosen
#	port (e.g. 8082) and port-forward it on your router for WAN access.
#LANDMSGPORT=8082

#--	Advertise a WAN-reachable address to remote visors (host:port). Empty
#	= LAN-only. Combine with LANDMSGPORT + a router port-forward for
#	hypervisors on a NAT.
#LANDMSGPUBLIC='203.0.113.42:8082'

### Rewards #############################################################

#--	Skycoin reward address or xpub key
#REWARDSKYADDR=''

### Apps ################################################################

#--	Display the node ip in the service discovery
#	for any public services this visor is running
#DISPLAYNODEIP=true

#--	Autostart vpn server for this visor
#VPNSERVER=false

#--	Autostart the vpn-router: a LAN/WiFi gateway that NATs downstream
#	clients into the vpn-client tunnel. Needs VPNROUTERLANIFC (the
#	downstream interface). Requires root + the vpn-client running.
#VPNROUTER=false

#--	Downstream interface the vpn-router serves. Ethernet-out: a second/USB
#	NIC (e.g. eth1). WiFi-out: the wireless interface (e.g. wlan0) — also set
#	VPNROUTERWIFI=true. The visor's own uplink (that reaches the vpn-server
#	over the mesh) must be a DIFFERENT interface.
#VPNROUTERLANIFC='eth1'

#--	vpn-router gateway + downstream subnet, as <gateway-ip>/<prefix>
#	(default 192.168.42.1/24)
#VPNROUTERSUBNET='192.168.42.1/24'

#--	WiFi-out: run hostapd (an access point) on VPNROUTERLANIFC so wireless
#	clients associate and are routed through the VPN. Leave false for the
#	ethernet-out variant. (rtl8723bs on the original skyminer boards can be
#	unstable in AP mode — a USB WiFi dongle is the robust option.)
#VPNROUTERWIFI=false

#--	WiFi SSID / WPA2 passphrase (8–63 chars) for the WiFi-out variant.
#	Set VPNROUTEROPEN=true for a passphrase-less open network instead.
#VPNROUTERSSID='skywire-vpn'
#VPNROUTERPASSPHRASE=''
#VPNROUTEROPEN=false

#--	WiFi band ('2.4' or '5'), channel (0 = default for the band), and
#	regulatory country code for the WiFi-out variant.
#VPNROUTERBAND='2.4'
#VPNROUTERCHANNEL=0
#VPNROUTERCOUNTRY='US'

#--	Mesh gateway: additionally let downstream clients reach mesh services by
#	name — resolve *.dmsg / *.skynet to a synthetic IP and transparently proxy
#	the connection over the mesh (no SOCKS, no per-device setup). The dest port
#	is the mesh routing port. VPNROUTERMESHGWCIDR is the synthetic-IP pool
#	(default 100.64.0.0/16; change only if it collides with your LAN).
#VPNROUTERMESHGW=false
#VPNROUTERMESHGWCIDR='100.64.0.0/16'

#--	Mesh gateway HTTPS: TLS-MITM connections to *.dmsg / *.skynet on :443 —
#	the gateway terminates TLS with a self-generated CA (persisted under
#	<local>/mesh-gateway-ca/) and bridges plaintext to the mesh service, so
#	browsers get a secure context. LAN clients must install that CA as trusted;
#	its path + fingerprint are logged on first start.
#VPNROUTERMESHTLS=false

#--	Set server public key for proxy client to connect to
#PROXYCLIENTPK=''

#--	Enable autostart of the proxy client
#STARTPROXYCLIENT=true

#--	Autostart proxy server
#PROXYSERVER=false

#--	Autostart skychat
#SKYCHAT=false

#--	Skychat local address
#SKYCHATADDR='127.0.0.1:8001'

#--	Run skychat with no TCP port at all: the UI is then reachable only
#--	through the hypervisor. Default: false (skychat binds SKYCHATADDR).
#SKYCHATPORTLESS=false

#--	Skychat pair-RPC channel to visor (required for group chat).
#--	Default: true. Set to false to disable group-chat plumbing.
#SKYCHATPAIR=false

#--	Autostart the dmsgweb SOCKS bridge (browse dmsg sites over a local
#	SOCKS5 proxy). Off by default.
#DMSGWEB=false

#--	dmsgweb upstream address the bridge listens on (host:port)
#DMSGWEBUPSTREAM=':8082'

#--	Autostart the skynetweb bridge (serve/reach skynet sites). Off by default.
#SKYNETWEB=false

#--	skynetweb upstream address the bridge listens on (host:port)
#SKYNETWEBUPSTREAM=':8083'
#DMSGWEBADDR='0.0.0.0'
#SKYNETWEBADDR='0.0.0.0'

#--	Autostart the skymail bridge (SMTP <-> skywire mail gateway). Off by default.
#SKYMAILBRIDGE=false

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

#--	Run multiple skycoin daemon instances (one per fibercoin chain),
#	comma-separated instance names. Empty = the single default daemon.
#	Each instance takes its own fiber.toml / API-sets / port set.
#SKYCOIND_INSTANCES=('skycoin' 'mdl')

#--	Extra flags passed verbatim to each skycoin daemon invocation.
#SKYCOIND_FLAGS='-launch-browser=false'

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
#	~/.skycoin/wallets directory. The launcher only honors this
#	in external mode (Binary set on the AppConfig entry, which
#	the default skycoin-web entry already has).
#SKYCOINWEBUSER='youruser'

#--	Advertise already-running fibercoin nodes over dmsg + service
#	discovery (type=coin), so a thin-client wallet — including the
#	browser wasm visor — can discover and reach a node for the right
#	coin over the mesh, with no local full node and no launch flags.
#	Bash array. Each entry is "<local_addr>[@<dmsg_port>]" where
#	local_addr is the node's HTTP API on this host; dmsg_port defaults
#	to local_addr's port. The node runs INDEPENDENTLY of the visor
#	(it need not be a skywire SKYCOIND app) and is health-gated: the
#	visor advertises it only while its /api/v1/health answers.
#		COIN_NODES=('127.0.0.1:6420')
#	Multi-coin (skycoin + a fibercoin on 6430):
#		COIN_NODES=('127.0.0.1:6420' '127.0.0.1:6430@6430')
#COIN_NODES=('')

### Advanced Tuning #####################################################

#--	CLI RPC address (default localhost:3435)
#	Use 0.0.0.0:3435 for Docker/remote access
#CLIADDR='localhost:3435'

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

#--	Extra services to restart after a successful binary update.
#	Bash array of systemd unit names, restarted at the END of
#	skywire-update — ONLY when a new binary was actually installed (never
#	on a no-op update tick or a plain 'skywire autoconfig' run). Use for
#	standalone units that depend on the skywire binary, e.g. a separate
#	dmsgweb SOCKS5 proxy. The skywire service is always restarted on
#	update; do not list it here.
#RESTART_SERVICES=('dmsgweb-surveys.service')

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

#--	Use dmsghttp to connect to the production deployment
#$DMSGHTTP=$true

#--	Number of dmsg servers to connect to (0 unlimits)
#$MINDMSGSESS=8

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

#- set ONE shared master port for all transport types (stcpr+WS on <port>/tcp,
#- quic+sudph+wt+webrtc on <port>/udp). Overrides SUDPHPORT/STCPRPORT unless those
#- are set to break a type out onto its own port. 0 = per-type ports.
#$TRANSPORTPORT=0

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

#--	Hypervisor web-UI listen address (host:port). Default ':8000' = all
#	interfaces (reachable on the LAN at http://<this-host-ip>:8000).
#	Use '127.0.0.1:8000' for localhost-only, or pin a LAN IP.
#$HVHTTPADDR=':8000'

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
#$SKYCHATADDR='127.0.0.1:8001'

#--	Run skychat with no TCP port at all: the UI is then reachable only
#--	through the hypervisor. Default: false (skychat binds SKYCHATADDR).
#$SKYCHATPORTLESS=$false

#--	Skychat pair-RPC channel to visor (required for group chat).
#--	Default: true. Set to false to disable group-chat plumbing.
#$SKYCHATPAIR=$false

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
