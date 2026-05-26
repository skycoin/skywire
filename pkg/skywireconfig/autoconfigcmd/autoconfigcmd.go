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
type Values struct {
	Verbose         bool
	Hvpks           string
	Ishv            bool
	NoIshv          bool
	RewardAddr      string
	Public          bool
	NoPublic        bool
	StcprPort       int
	SudphPort       int
	LanDmsgPort     int
	LanDmsgPublic   string
	DmsgptyPks      string
	VpnServer       bool
	NoVpnServer     bool
	ProxyServer     bool
	NoProxyServer   bool
	Skychat         bool
	NoSkychat       bool
	Dmsgweb         bool
	NoDmsgweb       bool
	Skynetweb       bool
	NoSkynetweb     bool
	DisablePubAuto  bool
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

	cmd.Flags().BoolVarP(&v.Verbose, "verbose", "v", false, "show reward address, support links, and other details")
	cmd.Flags().StringVar(&v.Hvpks, "hvpks", "", "set HYPERVISORPKS in skywire.conf (comma-separated PKs)")
	cmd.Flags().BoolVar(&v.Ishv, "ishv", false, "set ISHYPERVISOR=true in skywire.conf")
	cmd.Flags().BoolVar(&v.NoIshv, "no-ishv", false, "set ISHYPERVISOR=false in skywire.conf")
	cmd.Flags().StringVar(&v.RewardAddr, "rewardaddr", "", "set REWARDSKYADDR in skywire.conf")
	cmd.Flags().BoolVar(&v.Public, "public", false, "set VISORISPUBLIC=true in skywire.conf")
	cmd.Flags().BoolVar(&v.NoPublic, "no-public", false, "set VISORISPUBLIC=false in skywire.conf")
	cmd.Flags().IntVar(&v.StcprPort, "stcpr", 0, "set STCPRPORT in skywire.conf (0 = leave unchanged)")
	cmd.Flags().IntVar(&v.SudphPort, "sudph", 0, "set SUDPHPORT in skywire.conf (0 = leave unchanged)")
	cmd.Flags().IntVar(&v.LanDmsgPort, "lan-dmsg-port", 0, "set LANDMSGPORT in skywire.conf (0 = leave unchanged)")
	cmd.Flags().StringVar(&v.LanDmsgPublic, "lan-dmsg-public", "", "set LANDMSGPUBLIC in skywire.conf (host:port)")
	cmd.Flags().StringVar(&v.DmsgptyPks, "dmsgpty-pks", "", "set DMSGPTYPKS in skywire.conf (comma-separated PKs)")
	cmd.Flags().BoolVar(&v.VpnServer, "vpnserver", false, "set VPNSERVER=true in skywire.conf")
	cmd.Flags().BoolVar(&v.NoVpnServer, "no-vpnserver", false, "set VPNSERVER=false in skywire.conf")
	cmd.Flags().BoolVar(&v.ProxyServer, "proxyserver", false, "set PROXYSERVER=true in skywire.conf")
	cmd.Flags().BoolVar(&v.NoProxyServer, "no-proxyserver", false, "set PROXYSERVER=false in skywire.conf")
	cmd.Flags().BoolVar(&v.Skychat, "skychat", false, "set SKYCHAT=true in skywire.conf")
	cmd.Flags().BoolVar(&v.NoSkychat, "no-skychat", false, "set SKYCHAT=false in skywire.conf")
	cmd.Flags().BoolVar(&v.Dmsgweb, "dmsgweb", false, "set DMSGWEB=true in skywire.conf")
	cmd.Flags().BoolVar(&v.NoDmsgweb, "no-dmsgweb", false, "set DMSGWEB=false in skywire.conf")
	cmd.Flags().BoolVar(&v.Skynetweb, "skynetweb", false, "set SKYNETWEB=true in skywire.conf")
	cmd.Flags().BoolVar(&v.NoSkynetweb, "no-skynetweb", false, "set SKYNETWEB=false in skywire.conf")
	cmd.Flags().BoolVar(&v.DisablePubAuto, "disable-public-autoconn", false, "set DISABLEPUBLICAUTOCONN=true in skywire.conf")

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
var envMap = map[string]EnvMapping{
	"hvpks":                   {Key: "HYPERVISORPKS", Format: EnvFormatBashArray},
	"ishv":                    {Key: "ISHYPERVISOR", Format: EnvFormatBool},
	"no-ishv":                 {Key: "ISHYPERVISOR", Format: EnvFormatBool, Negate: true},
	"rewardaddr":              {Key: "REWARDSKYADDR", Format: EnvFormatString},
	"public":                  {Key: "VISORISPUBLIC", Format: EnvFormatBool},
	"no-public":               {Key: "VISORISPUBLIC", Format: EnvFormatBool, Negate: true},
	"stcpr":                   {Key: "STCPRPORT", Format: EnvFormatInt},
	"sudph":                   {Key: "SUDPHPORT", Format: EnvFormatInt},
	"lan-dmsg-port":           {Key: "LANDMSGPORT", Format: EnvFormatInt},
	"lan-dmsg-public":         {Key: "LANDMSGPUBLIC", Format: EnvFormatString},
	"dmsgpty-pks":             {Key: "DMSGPTYPKS", Format: EnvFormatBashArray},
	"vpnserver":               {Key: "VPNSERVER", Format: EnvFormatBool},
	"no-vpnserver":            {Key: "VPNSERVER", Format: EnvFormatBool, Negate: true},
	"proxyserver":             {Key: "PROXYSERVER", Format: EnvFormatBool},
	"no-proxyserver":          {Key: "PROXYSERVER", Format: EnvFormatBool, Negate: true},
	"skychat":                 {Key: "SKYCHAT", Format: EnvFormatBool},
	"no-skychat":              {Key: "SKYCHAT", Format: EnvFormatBool, Negate: true},
	"dmsgweb":                 {Key: "DMSGWEB", Format: EnvFormatBool},
	"no-dmsgweb":              {Key: "DMSGWEB", Format: EnvFormatBool, Negate: true},
	"skynetweb":               {Key: "SKYNETWEB", Format: EnvFormatBool},
	"no-skynetweb":            {Key: "SKYNETWEB", Format: EnvFormatBool, Negate: true},
	"disable-public-autoconn": {Key: "DISABLEPUBLICAUTOCONN", Format: EnvFormatBool},
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
                           USRENV otherwise.`
