// Package autoconfigcmd builds the cobra.Command for `skywire
// autoconfig` as a freestanding factory. The CLI binary in
// cmd/skywire/commands wires the returned command into the root
// hierarchy and attaches its RunE; external consumers — TinyGo/Go
// WASM, doc generators, the apt-repo install-page form — call New()
// to introspect the flag set and render help text without dragging
// in the visor's transitive package graph.
//
// Why a factory: cobra.Command and its flag bindings hold mutable
// state (flag values, parse results, Changed bits). A fresh command
// per call means multiple consumers can coexist without trampling
// each other's state. The flag *values* are returned alongside the
// command so the caller can both register a custom Run (for the
// CLI binary) and read the parsed values (for the .conf edit
// computation).
//
// Why expose env-var metadata: each autoconfig flag corresponds to
// a SKYENV variable in /etc/skywire.conf. Encoding that mapping in
// the same place as the flag definition keeps the two from drifting
// apart, and lets WASM consumers render a form that shows operators
// exactly which line they're toggling.
//
// Parity with `skywire cli config gen`: the autoconfig surface is
// designed to be a strict superset over the SKYENV-reading subset
// of `cli config gen` flags. Every variable config gen looks up via
// scriptExec*("${VAR…}") in cmd/skywire-cli/commands/config/gen.go
// has an autoconfig flag whose effect on skywire.conf produces the
// same downstream behavior. The non-SKYENV flags (--stdout, --regen,
// the per-service generators --sn/--dmsgdisc/etc., the OS selector
// --os) are intentionally NOT exposed — they have no persistence
// surface so autoconfig has nothing to do with them.
package autoconfigcmd

import (
	"github.com/spf13/cobra"
)

// EnvFormat describes how a flag's runtime value is rendered into
// its /etc/skywire.conf line.
type EnvFormat string

const (
	// EnvFormatBool emits `KEY=true` or `KEY=false`.
	EnvFormatBool EnvFormat = "bool"
	// EnvFormatString emits `KEY='value'` with single-quote wrap.
	EnvFormatString EnvFormat = "string"
	// EnvFormatInt emits `KEY=N`. Zero values are skipped at the
	// edit-collection layer (matches the "0 = leave unchanged"
	// semantic in the autoconfig --help text for port flags).
	EnvFormatInt EnvFormat = "int"
	// EnvFormatBashArray emits `KEY=('a' 'b' …)` from a
	// comma-separated input — the shape HYPERVISORPKS,
	// DMSGPTYPKS, and similar array-typed env vars use.
	EnvFormatBashArray EnvFormat = "bashArray"
)

// EnvMapping ties a CLI flag to the SKYENV variable it writes when
// the operator passes the flag.
type EnvMapping struct {
	// Key is the SKYENV variable name (e.g. "ISHYPERVISOR").
	Key string
	// Format is the wire encoding for KEY=… in /etc/skywire.conf.
	Format EnvFormat
	// Negate inverts the emit for bool flags. A negation flag like
	// --no-ishv has Negate=true so passing it (which sets the
	// bool to true) writes ISHYPERVISOR=false to the .conf file.
	Negate bool
}

// Values holds the destination addresses for every flag binding
// installed by New. Callers pass a Values they own (typically a
// package-level var in cmd/skywire/commands/autoconfig.go) so that
// post-parse reads stay simple field access — no double-indirection.
//
// Field groupings mirror the help-output sections in the
// `skywire cli config gen --all` output so that operators familiar
// with config gen find each autoconfig flag in roughly the same
// neighborhood.
type Values struct {
	// --- Display-only ---
	Verbose bool

	// --- Hypervisor / identity ---
	Hvpks     string
	Ishv      bool
	NoIshv    bool
	HvAddr    string // HVHTTPADDR
	SecretKey string // SK — security-sensitive; see flag description
	Version   string // VERSION — testing override

	// --- Visor public/private + autoconnect ---
	RewardAddr     string
	Public         bool
	NoPublic       bool
	PublicIP       bool // DISPLAYNODEIP
	DisablePubAuto bool

	// --- Service discovery / deployment ---
	TestEnv     bool   // TESTENV
	DmsgHTTP    bool   // DMSGHTTP
	DmsgConf    string // DMSGCONF
	URL         string // SVCCONFADDR (services conf URL — accepts comma-list)
	SvcConf     string // SVCCONF (fallback service-configuration file)
	MinSess     int    // MINDMSGSESS
	StunServers string // STUNSERVERS (comma-separated)

	// --- Transport ports ---
	StcprPort     int
	SudphPort     int
	LanDmsgPort   int
	LanDmsgPublic string

	// --- Whitelists ---
	DmsgptyPks     string
	SurveyPks      string // SURVEYPKS
	RouteSetupPKs  string // ROUTESETUPPKS
	TransportSetup string // TPSETUPPKS

	// --- Route calculation ---
	CalculateRoutes bool // CALCULATEROUTES

	// --- VPN server feature ---
	VpnServer   bool
	NoVpnServer bool
	VpnKillSw   string // VPNKS
	AddVpn      string // ADDVPNPK
	VpnWl       string // VPNSERVERWL (comma-separated)
	VpnSecure   string // VPNSEVERSECURE (sic — typo retained for compat with config gen's env-var name)
	VpnNetIfc   string // VPNSEVERNETIFC (sic — same)

	// --- Proxy feature ---
	ProxyServer   bool
	NoProxyServer bool
	ProxyClientPK string // PROXYCLIENTPK
	StartProxyCli bool   // STARTPROXYCLIENT
	ProxyWl       string // PROXYSERVERWL (comma-separated)

	// --- SOCKS5 web bridges ---
	Dmsgweb           bool
	NoDmsgweb         bool
	Skynetweb         bool
	NoSkynetweb       bool
	DmsgwebUpstream   string // DMSGWEBUPSTREAM
	SkynetwebUpstream string // SKYNETWEBUPSTREAM

	// --- Skychat ---
	Skychat         bool
	NoSkychat       bool
	ChatAddr        string // SKYCHATADDR
	ServeChatPair   bool   // SKYCHATPAIR — default true in config gen
	NoServeChatPair bool

	// --- Skymail bridge (SMTP↔skywire) ---
	SkymailBridge   bool // SKYMAILBRIDGE
	NoSkymailBridge bool

	// --- Skycoin daemon (legacy + multi-instance) ---
	Skycoind          bool
	NoSkycoind        bool
	SkycoindFiber     string // SKYCOIND_FIBER_TOML
	SkycoindAPI       string // SKYCOIND_API_SETS
	SkycoindUser      string // SKYCOIND_USER
	SkycoindInstances string // SKYCOIND_INSTANCES (comma-separated)
	SkycoindFlags     string // SKYCOIND_FLAGS

	// --- Skycoin web wallet ---
	Skycoinweb       bool
	NoSkycoinweb     bool
	SkycoinwebAddr   string // SKYCOINWEBADDR
	SkycoinwebNodes  string // SKYCOINWEBNODES (comma-separated)
	SkycoinwebWallet string // SKYCOINWEBWALLET
	SkycoinwebUser   string // SKYCOINWEBUSER

	// --- Visor runtime ---
	BinPath         string // BINPATH
	LogLevel        string // LOGLVL
	ShutdownTimeout string // SHUTDOWNTIMEOUT
	RegTimeout      string // REGTIMEOUT
}

// New returns a freshly-constructed autoconfig cobra command with
// flag bindings pointed at v's fields. The command has no RunE /
// Run — callers (the CLI binary) attach their own; WASM consumers
// only need cmd.UsageString() and cmd.Flags() and don't run it at
// all.
//
// v must outlive the returned command. Passing nil triggers a
// panic at flag-registration time (cobra's BoolVar et al. require
// a non-nil destination).
func New(v *Values) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "autoconfig",
		Short: "Automatic visor configuration",
		Long:  longDesc,
	}

	// Flag descriptions mirror `skywire cli config gen`'s equivalent
	// flag where one exists, so the autoconfig surface and the
	// config-gen surface stay in sync. Operators (and the WASM-
	// rendered install-page form) see what the flag actually DOES,
	// not just which env var it writes.

	// --- Display ---
	cmd.Flags().BoolVarP(&v.Verbose, "verbose", "v", false, "show reward address, support links, and other details")

	// --- Hypervisor / identity ---
	cmd.Flags().StringVar(&v.Hvpks, "hvpks", "", "comma-separated remote hypervisor PKs to dial — writes HYPERVISORPKS in skywire.conf")
	cmd.Flags().BoolVar(&v.Ishv, "ishv", false, "enable local hypervisor — writes ISHYPERVISOR=true in skywire.conf")
	cmd.Flags().BoolVar(&v.NoIshv, "no-ishv", false, "disable local hypervisor — writes ISHYPERVISOR=false in skywire.conf")
	cmd.Flags().StringVar(&v.HvAddr, "hvaddr", "", "hypervisor HTTP address (host:port) — writes HVHTTPADDR in skywire.conf")
	cmd.Flags().StringVar(&v.SecretKey, "sk", "", "pin visor secret key (64-hex) — writes SK in skywire.conf. SECURITY: appears in shell history; prefer letting the visor generate its own SK on first boot.")
	cmd.Flags().StringVar(&v.Version, "version", "", "custom version override for testing — writes VERSION in skywire.conf")

	// --- Visor public/private + autoconnect ---
	cmd.Flags().StringVar(&v.RewardAddr, "rewardaddr", "", "skycoin reward address or xpub key — writes REWARDSKYADDR in skywire.conf")
	cmd.Flags().BoolVar(&v.Public, "public", false, "publicize visor in service discovery — writes VISORISPUBLIC=true in skywire.conf")
	cmd.Flags().BoolVar(&v.NoPublic, "no-public", false, "unpublish visor from service discovery — writes VISORISPUBLIC=false in skywire.conf")
	cmd.Flags().BoolVar(&v.PublicIP, "publicip", false, "display visor IP in service discovery — writes DISPLAYNODEIP=true in skywire.conf")
	cmd.Flags().BoolVar(&v.DisablePubAuto, "disable-public-autoconn", false, "disable autoconnect to public visors — writes DISABLEPUBLICAUTOCONN=true in skywire.conf")

	// --- Service discovery / deployment ---
	cmd.Flags().BoolVar(&v.TestEnv, "testenv", false, "use test deployment (ports offset +10000 from prod) — writes TESTENV=true in skywire.conf")
	cmd.Flags().BoolVar(&v.DmsgHTTP, "dmsghttp", false, "use only dmsg for skywire services (no http) — writes DMSGHTTP=true in skywire.conf")
	cmd.Flags().StringVar(&v.DmsgConf, "dmsgconf", "", "dmsghttp config path — writes DMSGCONF in skywire.conf")
	cmd.Flags().StringVar(&v.URL, "url", "", "services-config URL(s), comma-separated — writes SVCCONFADDR in skywire.conf")
	cmd.Flags().StringVar(&v.SvcConf, "svcconf", "", "fallback service-configuration file path — writes SVCCONF in skywire.conf")
	cmd.Flags().IntVar(&v.MinSess, "minsess", 0, "number of dmsg servers to connect to (0 = unlimited; leave unset to keep default 2) — writes MINDMSGSESS in skywire.conf")
	cmd.Flags().StringVar(&v.StunServers, "stun", "", "STUN servers, comma-separated — writes STUNSERVERS in skywire.conf")

	// --- Transport ports ---
	cmd.Flags().IntVar(&v.StcprPort, "stcpr", 0, "stcp transport listening port (0 = leave unchanged, random at runtime) — writes STCPRPORT in skywire.conf")
	cmd.Flags().IntVar(&v.SudphPort, "sudph", 0, "sudp transport listening port (0 = leave unchanged, random at runtime) — writes SUDPHPORT in skywire.conf")
	cmd.Flags().IntVar(&v.LanDmsgPort, "lan-dmsg-port", 0, "LAN dmsg-server listening port (0 = leave unchanged) — writes LANDMSGPORT in skywire.conf")
	cmd.Flags().StringVar(&v.LanDmsgPublic, "lan-dmsg-public", "", "public host:port for the LAN dmsg-server entry in dmsg discovery — writes LANDMSGPUBLIC in skywire.conf")

	// --- Whitelists ---
	cmd.Flags().StringVar(&v.DmsgptyPks, "dmsgpty-pks", "", "additional dmsgpty-whitelist PKs (hypervisor PKs are already implicit) — writes DMSGPTYPKS in skywire.conf")
	cmd.Flags().StringVar(&v.SurveyPks, "survey", "", "survey whitelist PKs, comma-separated — writes SURVEYPKS in skywire.conf")
	cmd.Flags().StringVar(&v.RouteSetupPKs, "routesetup", "", "route setup node PKs, comma-separated — writes ROUTESETUPPKS in skywire.conf")
	cmd.Flags().StringVar(&v.TransportSetup, "tpsetup", "", "transport setup node PKs, comma-separated — writes TPSETUPPKS in skywire.conf")

	// --- Route calculation ---
	cmd.Flags().BoolVar(&v.CalculateRoutes, "calculate-routes", false, "enable local route calculation — writes CALCULATEROUTES=true in skywire.conf")

	// --- VPN server ---
	cmd.Flags().BoolVar(&v.VpnServer, "vpnserver", false, "autostart vpn-server — writes VPNSERVER=true in skywire.conf")
	cmd.Flags().BoolVar(&v.NoVpnServer, "no-vpnserver", false, "do not autostart vpn-server — writes VPNSERVER=false in skywire.conf")
	cmd.Flags().StringVar(&v.VpnKillSw, "killsw", "", "vpn client killswitch — writes VPNKS in skywire.conf")
	cmd.Flags().StringVar(&v.AddVpn, "addvpn", "", "vpn server public key for vpn client — writes ADDVPNPK in skywire.conf")
	cmd.Flags().StringVar(&v.VpnWl, "vpnwl", "", "vpn server whitelist PKs, comma-separated (empty = allow all) — writes VPNSERVERWL in skywire.conf")
	cmd.Flags().StringVar(&v.VpnSecure, "secure", "", "vpn server secure-mode status — writes VPNSEVERSECURE in skywire.conf (env var name has a typo upstream)")
	cmd.Flags().StringVar(&v.VpnNetIfc, "netifc", "", "vpn server network interface — writes VPNSEVERNETIFC in skywire.conf (env var name has a typo upstream)")

	// --- Proxy ---
	cmd.Flags().BoolVar(&v.ProxyServer, "proxyserver", false, "autostart skysocks (proxy server) — writes PROXYSERVER=true in skywire.conf")
	cmd.Flags().BoolVar(&v.NoProxyServer, "no-proxyserver", false, "do not autostart skysocks — writes PROXYSERVER=false in skywire.conf")
	cmd.Flags().StringVar(&v.ProxyClientPK, "proxyclientpk", "", "proxy server public key for proxy client — writes PROXYCLIENTPK in skywire.conf")
	cmd.Flags().BoolVar(&v.StartProxyCli, "startproxyclient", false, "autostart proxy client — writes STARTPROXYCLIENT=true in skywire.conf")
	cmd.Flags().StringVar(&v.ProxyWl, "proxywl", "", "proxy server whitelist PKs, comma-separated (empty = allow all) — writes PROXYSERVERWL in skywire.conf")

	// --- SOCKS5 web bridges ---
	cmd.Flags().BoolVar(&v.Dmsgweb, "dmsgweb", false, "enable embedded .dmsg resolving SOCKS5 proxy on 127.0.0.1:4445 — writes DMSGWEB=true in skywire.conf")
	cmd.Flags().BoolVar(&v.NoDmsgweb, "no-dmsgweb", false, "disable embedded .dmsg resolving SOCKS5 proxy — writes DMSGWEB=false in skywire.conf")
	cmd.Flags().BoolVar(&v.Skynetweb, "skynetweb", false, "enable embedded .skynet resolving SOCKS5 proxy on 127.0.0.1:4446 — writes SKYNETWEB=true in skywire.conf")
	cmd.Flags().BoolVar(&v.NoSkynetweb, "no-skynetweb", false, "disable embedded .skynet resolving SOCKS5 proxy — writes SKYNETWEB=false in skywire.conf")
	cmd.Flags().StringVar(&v.DmsgwebUpstream, "dmsgweb-upstream", "", "upstream SOCKS5 for non-.dmsg traffic (empty chains to skynetweb) — writes DMSGWEBUPSTREAM in skywire.conf")
	cmd.Flags().StringVar(&v.SkynetwebUpstream, "skynetweb-upstream", "", "upstream SOCKS5 for non-.skynet traffic — writes SKYNETWEBUPSTREAM in skywire.conf")

	// --- Skychat ---
	cmd.Flags().BoolVar(&v.Skychat, "skychat", false, "autostart skychat — writes SKYCHAT=true in skywire.conf")
	cmd.Flags().BoolVar(&v.NoSkychat, "no-skychat", false, "do not autostart skychat — writes SKYCHAT=false in skywire.conf")
	cmd.Flags().StringVar(&v.ChatAddr, "chataddr", "", "skychat local address (host:port) — writes SKYCHATADDR in skywire.conf")
	cmd.Flags().BoolVar(&v.ServeChatPair, "servechatpair", false, "enable skychat pair RPC channel (required for group chat) — writes SKYCHATPAIR=true in skywire.conf")
	cmd.Flags().BoolVar(&v.NoServeChatPair, "no-servechatpair", false, "disable skychat pair RPC channel — writes SKYCHATPAIR=false in skywire.conf")

	// --- Skymail bridge ---
	cmd.Flags().BoolVar(&v.SkymailBridge, "skymail-bridge", false, "enable SMTP↔skywire bridge on 127.0.0.1:1025 — writes SKYMAILBRIDGE=true in skywire.conf")
	cmd.Flags().BoolVar(&v.NoSkymailBridge, "no-skymail-bridge", false, "disable SMTP↔skywire bridge — writes SKYMAILBRIDGE=false in skywire.conf")

	// --- Skycoin daemon ---
	cmd.Flags().BoolVar(&v.Skycoind, "skycoind", false, "autostart skycoin daemon (legacy single instance) — writes SKYCOIND=true in skywire.conf")
	cmd.Flags().BoolVar(&v.NoSkycoind, "no-skycoind", false, "do not autostart skycoin daemon — writes SKYCOIND=false in skywire.conf")
	cmd.Flags().StringVar(&v.SkycoindFiber, "skycoindfiber", "", "legacy FIBER_TOML path (single skycoin daemon instance) — writes SKYCOIND_FIBER_TOML in skywire.conf")
	cmd.Flags().StringVar(&v.SkycoindAPI, "skycoindapi", "", "legacy api sets (single skycoin daemon instance) — writes SKYCOIND_API_SETS in skywire.conf")
	cmd.Flags().StringVar(&v.SkycoindUser, "skycoindUSER", "", "skycoin daemon UID (applies to every instance) — writes SKYCOIND_USER in skywire.conf")
	cmd.Flags().StringVar(&v.SkycoindInstances, "skycoindinstances", "", "skycoin daemon instances, comma-separated (skycoin or fiber.toml path) — writes SKYCOIND_INSTANCES in skywire.conf")
	cmd.Flags().StringVar(&v.SkycoindFlags, "skycoindflags", "", "extra flags appended to every skycoin daemon (port + data dir auto-allocated) — writes SKYCOIND_FLAGS in skywire.conf")

	// --- Skycoin web wallet ---
	cmd.Flags().BoolVar(&v.Skycoinweb, "skycoinweb", false, "autostart skycoin web wallet (thin client) — writes SKYCOINWEB=true in skywire.conf")
	cmd.Flags().BoolVar(&v.NoSkycoinweb, "no-skycoinweb", false, "do not autostart skycoin web wallet — writes SKYCOINWEB=false in skywire.conf")
	cmd.Flags().StringVar(&v.SkycoinwebAddr, "skycoinwebaddr", "", "skycoin web bind address (host:port) — writes SKYCOINWEBADDR in skywire.conf")
	cmd.Flags().StringVar(&v.SkycoinwebNodes, "skycoinwebnodes", "", "node URLs the skycoin web wallet talks to, comma-separated — writes SKYCOINWEBNODES in skywire.conf")
	cmd.Flags().StringVar(&v.SkycoinwebWallet, "skycoinwebwallet", "", "skycoin web wallet dir override — writes SKYCOINWEBWALLET in skywire.conf")
	cmd.Flags().StringVar(&v.SkycoinwebUser, "skycoinwebuser", "", "skycoin web UID (empty inherits visor UID) — writes SKYCOINWEBUSER in skywire.conf")

	// --- Visor runtime ---
	cmd.Flags().StringVar(&v.BinPath, "binpath", "", "bin_path for visor native apps — writes BINPATH in skywire.conf")
	cmd.Flags().StringVar(&v.LogLevel, "loglvl", "", "log level (debug/info/warn/error) — writes LOGLVL in skywire.conf")
	cmd.Flags().StringVar(&v.ShutdownTimeout, "timeout", "", "graceful shutdown timeout (e.g. 10s) — writes SHUTDOWNTIMEOUT in skywire.conf")
	cmd.Flags().StringVar(&v.RegTimeout, "regtimeout", "", "public visor registration timeout (e.g. 10m) — writes REGTIMEOUT in skywire.conf")

	return cmd
}

// EnvMap returns the flag → SKYENV-var mapping. Keys are CLI flag
// long names (without leading "--"). Flags absent from the map
// (currently only --verbose) have no .conf-file effect.
//
// A fresh map is returned each call so callers can mutate it
// without affecting the package-level data.
func EnvMap() map[string]EnvMapping {
	out := make(map[string]EnvMapping, len(envMap))
	for k, v := range envMap {
		out[k] = v
	}
	return out
}

// envMap is the package-level flag-name → SKYENV-mapping table.
// Negation flags (--no-X) carry Negate=true so the bool literal
// passed at the CLI (always true when the flag is present) gets
// inverted to "false" at .conf-render time.
//
// Kept in the same group order as the New() flag-registration
// section comments so cross-referencing stays one-to-one.
var envMap = map[string]EnvMapping{
	// Hypervisor / identity
	"hvpks":   {Key: "HYPERVISORPKS", Format: EnvFormatBashArray},
	"ishv":    {Key: "ISHYPERVISOR", Format: EnvFormatBool},
	"no-ishv": {Key: "ISHYPERVISOR", Format: EnvFormatBool, Negate: true},
	"hvaddr":  {Key: "HVHTTPADDR", Format: EnvFormatString},
	"sk":      {Key: "SK", Format: EnvFormatString},
	"version": {Key: "VERSION", Format: EnvFormatString},

	// Visor public/private + autoconnect
	"rewardaddr":              {Key: "REWARDSKYADDR", Format: EnvFormatString},
	"public":                  {Key: "VISORISPUBLIC", Format: EnvFormatBool},
	"no-public":               {Key: "VISORISPUBLIC", Format: EnvFormatBool, Negate: true},
	"publicip":                {Key: "DISPLAYNODEIP", Format: EnvFormatBool},
	"disable-public-autoconn": {Key: "DISABLEPUBLICAUTOCONN", Format: EnvFormatBool},

	// Service discovery / deployment
	"testenv":  {Key: "TESTENV", Format: EnvFormatBool},
	"dmsghttp": {Key: "DMSGHTTP", Format: EnvFormatBool},
	"dmsgconf": {Key: "DMSGCONF", Format: EnvFormatString},
	"url":      {Key: "SVCCONFADDR", Format: EnvFormatBashArray},
	"svcconf":  {Key: "SVCCONF", Format: EnvFormatString},
	"minsess":  {Key: "MINDMSGSESS", Format: EnvFormatInt},
	"stun":     {Key: "STUNSERVERS", Format: EnvFormatBashArray},

	// Transport ports
	"stcpr":           {Key: "STCPRPORT", Format: EnvFormatInt},
	"sudph":           {Key: "SUDPHPORT", Format: EnvFormatInt},
	"lan-dmsg-port":   {Key: "LANDMSGPORT", Format: EnvFormatInt},
	"lan-dmsg-public": {Key: "LANDMSGPUBLIC", Format: EnvFormatString},

	// Whitelists
	"dmsgpty-pks": {Key: "DMSGPTYPKS", Format: EnvFormatBashArray},
	"survey":      {Key: "SURVEYPKS", Format: EnvFormatBashArray},
	"routesetup":  {Key: "ROUTESETUPPKS", Format: EnvFormatBashArray},
	"tpsetup":     {Key: "TPSETUPPKS", Format: EnvFormatBashArray},

	// Route calculation
	"calculate-routes": {Key: "CALCULATEROUTES", Format: EnvFormatBool},

	// VPN server
	"vpnserver":    {Key: "VPNSERVER", Format: EnvFormatBool},
	"no-vpnserver": {Key: "VPNSERVER", Format: EnvFormatBool, Negate: true},
	"killsw":       {Key: "VPNKS", Format: EnvFormatString},
	"addvpn":       {Key: "ADDVPNPK", Format: EnvFormatString},
	"vpnwl":        {Key: "VPNSERVERWL", Format: EnvFormatBashArray},
	"secure":       {Key: "VPNSEVERSECURE", Format: EnvFormatString},
	"netifc":       {Key: "VPNSEVERNETIFC", Format: EnvFormatString},

	// Proxy
	"proxyserver":      {Key: "PROXYSERVER", Format: EnvFormatBool},
	"no-proxyserver":   {Key: "PROXYSERVER", Format: EnvFormatBool, Negate: true},
	"proxyclientpk":    {Key: "PROXYCLIENTPK", Format: EnvFormatString},
	"startproxyclient": {Key: "STARTPROXYCLIENT", Format: EnvFormatBool},
	"proxywl":          {Key: "PROXYSERVERWL", Format: EnvFormatBashArray},

	// SOCKS5 web bridges
	"dmsgweb":            {Key: "DMSGWEB", Format: EnvFormatBool},
	"no-dmsgweb":         {Key: "DMSGWEB", Format: EnvFormatBool, Negate: true},
	"skynetweb":          {Key: "SKYNETWEB", Format: EnvFormatBool},
	"no-skynetweb":       {Key: "SKYNETWEB", Format: EnvFormatBool, Negate: true},
	"dmsgweb-upstream":   {Key: "DMSGWEBUPSTREAM", Format: EnvFormatString},
	"skynetweb-upstream": {Key: "SKYNETWEBUPSTREAM", Format: EnvFormatString},

	// Skychat
	"skychat":          {Key: "SKYCHAT", Format: EnvFormatBool},
	"no-skychat":       {Key: "SKYCHAT", Format: EnvFormatBool, Negate: true},
	"chataddr":         {Key: "SKYCHATADDR", Format: EnvFormatString},
	"servechatpair":    {Key: "SKYCHATPAIR", Format: EnvFormatBool},
	"no-servechatpair": {Key: "SKYCHATPAIR", Format: EnvFormatBool, Negate: true},

	// Skymail bridge
	"skymail-bridge":    {Key: "SKYMAILBRIDGE", Format: EnvFormatBool},
	"no-skymail-bridge": {Key: "SKYMAILBRIDGE", Format: EnvFormatBool, Negate: true},

	// Skycoin daemon
	"skycoind":          {Key: "SKYCOIND", Format: EnvFormatBool},
	"no-skycoind":       {Key: "SKYCOIND", Format: EnvFormatBool, Negate: true},
	"skycoindfiber":     {Key: "SKYCOIND_FIBER_TOML", Format: EnvFormatString},
	"skycoindapi":       {Key: "SKYCOIND_API_SETS", Format: EnvFormatString},
	"skycoindUSER":      {Key: "SKYCOIND_USER", Format: EnvFormatString},
	"skycoindinstances": {Key: "SKYCOIND_INSTANCES", Format: EnvFormatBashArray},
	"skycoindflags":     {Key: "SKYCOIND_FLAGS", Format: EnvFormatString},

	// Skycoin web wallet
	"skycoinweb":       {Key: "SKYCOINWEB", Format: EnvFormatBool},
	"no-skycoinweb":    {Key: "SKYCOINWEB", Format: EnvFormatBool, Negate: true},
	"skycoinwebaddr":   {Key: "SKYCOINWEBADDR", Format: EnvFormatString},
	"skycoinwebnodes":  {Key: "SKYCOINWEBNODES", Format: EnvFormatBashArray},
	"skycoinwebwallet": {Key: "SKYCOINWEBWALLET", Format: EnvFormatString},
	"skycoinwebuser":   {Key: "SKYCOINWEBUSER", Format: EnvFormatString},

	// Visor runtime
	"binpath":    {Key: "BINPATH", Format: EnvFormatString},
	"loglvl":     {Key: "LOGLVL", Format: EnvFormatString},
	"timeout":    {Key: "SHUTDOWNTIMEOUT", Format: EnvFormatString},
	"regtimeout": {Key: "REGTIMEOUT", Format: EnvFormatString},
}

// longDesc matches the pre-factory autoconfig command's Long field
// verbatim so --help output is unchanged.
const longDesc = `Automatic visor configuration. Reads /etc/skywire.conf (or whatever
SKYENV points at), generates the visor config, manages the systemd
drop-in, and restarts (or prompts to start) the service.

Mode is selected by PKGENV/USRENV in the env file:

  PKGENV=true            → system install at /opt/skywire,
                           system-level systemd unit
  PKGENV=true
  SKYWIRE_USER=_skywire  → same, but visor runs as _skywire (drop-in
                           writes User=_skywire, /opt/skywire chowned)

  USRENV=true            → user install at $HOME, systemctl --user

  neither set            → falls back to PKGENV when run as root,
                           USRENV otherwise.

Every flag below corresponds to a SKYENV variable in skywire.conf.
Passing a flag rewrites that line (uncommenting it if necessary);
omitting a flag leaves the existing line untouched. The downstream
'cli config gen' invocation then sources skywire.conf so the new
values take effect on the same run.`
