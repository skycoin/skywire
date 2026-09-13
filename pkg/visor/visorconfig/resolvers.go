// Package visorconfig pkg/visor/visorconfig/resolvers.go c3-vis-core
//
// ResolverConfig declares an ADDITIONAL resolving proxy beyond the two
// singular blocks (dmsg_web, skynet_web) the visor has always hosted.
//
// Why a list alongside the singular blocks rather than turning dmsg_web into
// a list: every skywire-config.json in the field carries `dmsg_web` /
// `skynet_web` objects, and `cli resolver up/down/bind`, the proxy-status
// pages and the RPC surface all address exactly those two by name. Reshaping
// them into an array would invalidate every on-disk config and every caller
// at once for no gain — the two primaries ARE the default resolver pair, and
// what was missing is the ability to add more. So `resolvers` is purely
// additive: absent (the case for every config written before this) means
// exactly today's behavior, and an entry in it is a third, fourth, … proxy
// that runs beside the primaries.
//
// The knobs are a deliberate subset of DmsgWebConfig/SkynetWebConfig rather
// than an embedding of either. Embedding would flatten `secret_key` and
// `forward_proxy` (dmsg-only) into a skynet entry and `route_timeout`
// (skynet-only) into a dmsg entry, where they would be silently ignored —
// the kind of config that reads as supported and does nothing. Naming the
// fields once here, and rejecting the ones that don't apply to an entry's
// kind at parse time, keeps "what you wrote is what runs" true.
package visorconfig

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/skycoin/skywire/pkg/cipher"
)

// Resolver kinds. A ResolverConfig with an empty Kind is a dmsg resolver —
// the far more common case, and the one that can carry its own identity.
const (
	// ResolverKindDmsg resolves <pk><suffix> hostnames over dmsg, the same
	// way the dmsg_web block does. Default when Kind is empty.
	ResolverKindDmsg = "dmsg"
	// ResolverKindSkynet resolves them over skywire routing (the router),
	// the same way the skynet_web block does.
	ResolverKindSkynet = "skynet"
)

// DefaultDmsgWebProxyPort / DefaultSkynetWebProxyPort are the SOCKS5 ports the
// two primary resolvers bind when their block leaves proxy_port at zero. They
// live here (not only in pkg/visor) because port-conflict detection has to know
// what an unset port resolves to — a config that sets an extra resolver to 4445
// collides with a dmsg_web block that never mentions 4445 at all.
const (
	DefaultDmsgWebProxyPort   = 4445
	DefaultSkynetWebProxyPort = 4446
)

// ResolverConfig is ONE additional resolving proxy: its own SOCKS5 listener,
// its own domain suffix, and — for a dmsg resolver — optionally its own dmsg
// identity.
//
// The point of more than one is that a resolver's listener is also its access
// policy. A loopback resolver on 4445 serves this host; a second one bound to
// 0.0.0.0 on another port with a DIFFERENT secret key serves the LAN under an
// identity the operator can whitelist (or revoke) separately, without the LAN's
// traffic ever appearing to come from the visor's own key. Before this existed
// the only way to get that was a second visor.
type ResolverConfig struct {
	// Name identifies this resolver in logs and as the launcher app row
	// ("resolver-<name>", so `cli visor app start resolver-lan` works). It
	// must be unique among resolvers and is restricted to [a-z0-9-] so it
	// stays a usable app name. Empty gets "r<index>" assigned at load.
	Name string `json:"name,omitempty"`
	// Kind is "dmsg" (default) or "skynet" — which transport the resolver
	// dials destinations over. See the ResolverKind* constants.
	Kind string `json:"kind,omitempty"`
	// Enable must be true for this resolver to bind its listener at boot.
	// A disabled entry is still constructed, so it can be started later
	// through the launcher, and is excluded from port-conflict checking
	// (it binds nothing).
	Enable bool `json:"enable"`
	// SecretKey runs a dmsg resolver under its OWN dmsg identity instead of
	// the visor's, attached in-process to the visor's relay — see
	// DmsgWebConfig.SecretKey for the full rationale and for why this is not
	// the second-full-client pattern that got removed in #4500/#4501.
	//
	// Meaningless for a skynet resolver, which reaches destinations through
	// the visor's router and therefore always speaks as the visor; setting it
	// on one is rejected rather than ignored.
	SecretKey *cipher.SecKey `json:"secret_key,omitempty"`
	// ProxyPort is this resolver's SOCKS5 listener port. REQUIRED and
	// non-zero: unlike the primaries there is no sensible default to fall
	// back to, and defaulting would silently land a second resolver on 4445
	// on top of dmsg_web.
	ProxyPort uint `json:"proxy_port"`
	// ProxyAddr is the host the listener binds. Empty = loopback
	// ("127.0.0.1"). "0.0.0.0" or a LAN IP serves other devices — only do
	// that on a trusted network, and prefer giving such a resolver its own
	// SecretKey so the LAN's requests are attributable to a key you can
	// revoke without rotating the visor's.
	ProxyAddr string `json:"proxy_addr,omitempty"`
	// DomainSuffix is the TLD this resolver treats as mesh addresses.
	// Empty uses the kind's default (".dmsg" / ".skynet"). A second resolver
	// may serve the SAME suffix as the primary — that is the LAN-gateway
	// case, two listeners answering for .dmsg under different identities.
	DomainSuffix string `json:"domain_suffix,omitempty"`
	// UpstreamSOCKS forwards CONNECTs that don't match DomainSuffix to this
	// SOCKS5 server. Set explicitly it always wins; left empty its value
	// depends on Chain.
	UpstreamSOCKS string `json:"upstream_socks,omitempty"`
	// Chain, when nil or true (the default), gives an empty UpstreamSOCKS the
	// same auto-chain the matching primary gets: a dmsg resolver chains to the
	// skynet_web listener (so one browser proxy entry covers .dmsg and .skynet),
	// and a skynet resolver chains to skysocks-client (so clearnet exits over
	// the mesh). Set false to leave the resolver unchained — non-matching
	// CONNECTs then leave this host directly, which is what a LAN .dmsg gateway
	// that must NOT become an open clearnet proxy wants.
	Chain *bool `json:"chain,omitempty"`
	// Alias is the friendly hostname label for THIS visor's own PK under this
	// resolver's suffix. Defaults to "skywire".
	Alias string `json:"alias,omitempty"`
	// SelfLoopback / SelfLoopbackAuthenticated mirror the identically-named
	// DmsgWebConfig fields; nil means on.
	SelfLoopback              *bool `json:"self_loopback,omitempty"`
	SelfLoopbackAuthenticated *bool `json:"self_loopback_authenticated,omitempty"`
	// RouteTimeout is the keepalive for routes a SKYNET resolver creates.
	// Zero uses the router default. Meaningless for a dmsg resolver.
	RouteTimeout Duration `json:"route_timeout,omitempty"`
	// TLSMITM / TLSPort / TLSCAPath / TLSCAKeyPath mirror the primaries' TLS
	// interception knobs; see DmsgWebConfig for the trust model. Off by
	// default, and a CA that fails to load downgrades to plain HTTP with a
	// warning rather than failing the resolver.
	TLSMITM      bool   `json:"tls_mitm,omitempty"`
	TLSPort      uint16 `json:"tls_port,omitempty"`
	TLSCAPath    string `json:"tls_ca_path,omitempty"`
	TLSCAKeyPath string `json:"tls_ca_key_path,omitempty"`
}

// IsSkynet reports whether this entry resolves over skywire routing.
func (r *ResolverConfig) IsSkynet() bool { return r.Kind == ResolverKindSkynet }

// Chained reports whether an unset UpstreamSOCKS should inherit the matching
// primary's auto-chain. Nil (the common case, and every entry written before
// the knob existed) means yes.
func (r *ResolverConfig) Chained() bool { return r.Chain == nil || *r.Chain }

// AppName is the launcher app row this resolver appears as. Prefixed so a
// resolver can never shadow a built-in app's registration — a resolver named
// "vpn-client" would otherwise replace the VPN client's entry in the global
// app registry, and the visor would launch a SOCKS5 proxy where the operator
// asked for a VPN.
func (r *ResolverConfig) AppName() string { return "resolver-" + r.Name }

// ToDmsgWeb projects this entry onto the DmsgWebConfig the EmbeddedDmsgWeb
// runtime consumes. The returned config is a fresh value the runtime owns:
// SetUpstream/SetBind mutate it, and those runtime-only changes deliberately
// do not write back into V1.Resolvers (which is the on-disk shape).
func (r *ResolverConfig) ToDmsgWeb() *DmsgWebConfig {
	return &DmsgWebConfig{
		Enable:                    r.Enable,
		SecretKey:                 r.SecretKey,
		ProxyPort:                 r.ProxyPort,
		ProxyAddr:                 r.ProxyAddr,
		DomainSuffix:              r.DomainSuffix,
		UpstreamSOCKS:             r.UpstreamSOCKS,
		Alias:                     r.Alias,
		SelfLoopback:              r.SelfLoopback,
		SelfLoopbackAuthenticated: r.SelfLoopbackAuthenticated,
		TLSMITM:                   r.TLSMITM,
		TLSPort:                   r.TLSPort,
		TLSCAPath:                 r.TLSCAPath,
		TLSCAKeyPath:              r.TLSCAKeyPath,
	}
}

// ToSkynetWeb is ToDmsgWeb's counterpart for a skynet entry. SecretKey is
// dropped on purpose — see the field comment.
func (r *ResolverConfig) ToSkynetWeb() *SkynetWebConfig {
	return &SkynetWebConfig{
		Enable:                    r.Enable,
		ProxyPort:                 r.ProxyPort,
		ProxyAddr:                 r.ProxyAddr,
		DomainSuffix:              r.DomainSuffix,
		UpstreamSOCKS:             r.UpstreamSOCKS,
		RouteTimeout:              r.RouteTimeout,
		Alias:                     r.Alias,
		SelfLoopback:              r.SelfLoopback,
		SelfLoopbackAuthenticated: r.SelfLoopbackAuthenticated,
		TLSMITM:                   r.TLSMITM,
		TLSPort:                   r.TLSPort,
		TLSCAPath:                 r.TLSCAPath,
		TLSCAKeyPath:              r.TLSCAKeyPath,
	}
}

// ResolverListenAddr renders a resolving proxy's SOCKS5 listener the way its
// runtime binds it: an empty addr means loopback, and a zero port means the
// SOCKS5 front-end is disabled (rendered as empty, never as ":0").
func ResolverListenAddr(addr string, port uint) string {
	if port == 0 {
		return ""
	}
	if strings.TrimSpace(addr) == "" {
		addr = "127.0.0.1"
	}
	return fmt.Sprintf("%s:%d", addr, port)
}

// NormalizeResolvers fills in the defaults a hand-edited config may omit —
// today just the name, which everything downstream (log fields, app rows)
// needs to be non-empty and unique. Called from ValidateResolvers so both the
// config generator and the visor see the same names.
func NormalizeResolvers(rs []ResolverConfig) {
	for i := range rs {
		if strings.TrimSpace(rs[i].Name) == "" {
			rs[i].Name = "r" + strconv.Itoa(i)
		}
	}
}

// ValidateResolvers checks the extra resolvers against each other AND against
// the two primary blocks, and returns the FIRST problem found, naming both
// sides of it.
//
// The failure this exists to prevent is a silent one. Two SOCKS5 listeners on
// the same port do not both work and do not both fail: whichever binds first
// wins, the other's Run loop returns "address already in use" into a log line
// nobody reads, and the operator sees a resolver that is enabled, shows as
// running in `app ls`, and answers nothing. Naming the conflict at config time
// — and refusing to construct the loser at boot — turns that into a sentence
// an operator can act on.
//
// Only ENABLED entries are port-checked: a disabled resolver binds nothing, so
// flagging it would fail configs that are perfectly fine today. Everything
// else (names, kinds, required ports, kind/field mismatches) is checked
// regardless, since those are wrong whether or not the entry is running.
//
// A wildcard bind ("0.0.0.0" / "::" / "*") conflicts with ANY address on the
// same port, because it already covers loopback — "0.0.0.0:4445" and
// "127.0.0.1:4445" are not two listeners, they are one bind and one EADDRINUSE.
func (v *V1) ValidateResolvers() error {
	NormalizeResolvers(v.Resolvers)

	// owner tracks who claimed a given "<addr>:<port>", for the error message.
	type claim struct {
		owner string
		addr  string
	}
	byPort := map[uint][]claim{}
	claimPort := func(owner, addr string, port uint) error {
		for _, c := range byPort[port] {
			if isWildcardBindAddr(addr) || isWildcardBindAddr(c.addr) || c.addr == addr {
				return fmt.Errorf("resolver port conflict on %d: %s binds %s and %s binds %s",
					port, c.owner, ResolverListenAddr(c.addr, port), owner, ResolverListenAddr(addr, port))
			}
		}
		byPort[port] = append(byPort[port], claim{owner: owner, addr: addr})
		return nil
	}

	if v.DmsgWeb != nil && v.DmsgWeb.Enable {
		port := v.DmsgWeb.ProxyPort
		if port == 0 {
			port = DefaultDmsgWebProxyPort
		}
		if err := claimPort("dmsg_web", v.DmsgWeb.ProxyAddr, port); err != nil {
			return err
		}
	}
	if v.SkynetWeb != nil && v.SkynetWeb.Enable {
		port := v.SkynetWeb.ProxyPort
		if port == 0 {
			port = DefaultSkynetWebProxyPort
		}
		if err := claimPort("skynet_web", v.SkynetWeb.ProxyAddr, port); err != nil {
			return err
		}
	}

	seenNames := map[string]int{}
	for i := range v.Resolvers {
		r := &v.Resolvers[i]
		label := fmt.Sprintf("resolvers[%d] (%q)", i, r.Name)
		if err := validResolverName(r.Name); err != nil {
			return fmt.Errorf("%s: %w", label, err)
		}
		if prev, dup := seenNames[r.Name]; dup {
			return fmt.Errorf("%s: duplicate resolver name, already used by resolvers[%d]", label, prev)
		}
		seenNames[r.Name] = i

		switch r.Kind {
		case "", ResolverKindDmsg:
		case ResolverKindSkynet:
			if r.SecretKey != nil {
				return fmt.Errorf("%s: secret_key is only meaningful for a dmsg resolver; a skynet resolver "+
					"dials through this visor's router and always speaks as the visor", label)
			}
		default:
			return fmt.Errorf("%s: unknown kind %q (want %q or %q)", label, r.Kind, ResolverKindDmsg, ResolverKindSkynet)
		}
		if !r.IsSkynet() && r.RouteTimeout != 0 {
			return fmt.Errorf("%s: route_timeout only applies to a skynet resolver", label)
		}
		if r.ProxyPort == 0 {
			return fmt.Errorf("%s: proxy_port is required (there is no default for an additional resolver; "+
				"defaulting one would land it on top of dmsg_web's %d)", label, DefaultDmsgWebProxyPort)
		}
		if !r.Enable {
			continue
		}
		if err := claimPort("resolvers["+strconv.Itoa(i)+"] "+r.Name, r.ProxyAddr, r.ProxyPort); err != nil {
			return err
		}
	}
	return nil
}

// isWildcardBindAddr reports whether addr binds every interface, so that a
// listener on it excludes every other bind on the same port.
func isWildcardBindAddr(addr string) bool {
	switch strings.TrimSpace(addr) {
	case "0.0.0.0", "::", "[::]", "*":
		return true
	}
	return false
}

// validResolverName keeps a resolver name usable as a launcher app row and as
// a log field: lowercase letters, digits and dashes, no leading/trailing dash.
func validResolverName(name string) error {
	if name == "" {
		return fmt.Errorf("empty resolver name")
	}
	if strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") {
		return fmt.Errorf("resolver name %q may not start or end with '-'", name)
	}
	for _, c := range name {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-':
		default:
			return fmt.Errorf("resolver name %q may only contain lowercase letters, digits and '-'", name)
		}
	}
	return nil
}

// ParseResolverSpec parses ONE entry of the RESOLVERS bash array / the
// --resolvers CSV into a ResolverConfig.
//
// Grammar, chosen so a whole resolver fits in one shell-array element and a
// comma can still separate entries:
//
//	<kind>:<port>[;key=value]...
//
// e.g.
//
//	dmsg:4447
//	dmsg:4447;addr=0.0.0.0;name=lan;sk=<64-hex>;chain=false
//	skynet:4448;suffix=.skynet;upstream=127.0.0.1:1080
//
// Recognized keys: name, addr, suffix, sk, upstream, chain, alias. An
// unrecognized key is an ERROR, not a shrug — a typo'd "adrr=0.0.0.0" that
// parsed silently would hand the operator a loopback-only resolver they
// believe is serving the LAN. Everything else (TLS interception, route
// timeouts, self-loopback) stays JSON-only: those are set once by hand, not
// from a .conf one-liner.
func ParseResolverSpec(spec string) (ResolverConfig, error) {
	var out ResolverConfig
	parts := strings.Split(strings.TrimSpace(spec), ";")
	head := strings.TrimSpace(parts[0])
	colon := strings.LastIndex(head, ":")
	if colon < 0 {
		return out, fmt.Errorf("resolver spec %q: want <kind>:<port>[;key=value…]", spec)
	}
	out.Kind = strings.ToLower(strings.TrimSpace(head[:colon]))
	switch out.Kind {
	case "", ResolverKindDmsg:
		out.Kind = ResolverKindDmsg
	case ResolverKindSkynet:
	default:
		return out, fmt.Errorf("resolver spec %q: unknown kind %q (want %q or %q)",
			spec, out.Kind, ResolverKindDmsg, ResolverKindSkynet)
	}
	port, err := strconv.ParseUint(strings.TrimSpace(head[colon+1:]), 10, 16)
	if err != nil || port == 0 {
		return out, fmt.Errorf("resolver spec %q: invalid port %q", spec, head[colon+1:])
	}
	out.ProxyPort = uint(port)
	out.Enable = true // an entry in RESOLVERS is a request to run it

	for _, kv := range parts[1:] {
		kv = strings.TrimSpace(kv)
		if kv == "" {
			continue
		}
		eq := strings.Index(kv, "=")
		if eq < 0 {
			return out, fmt.Errorf("resolver spec %q: option %q is not key=value", spec, kv)
		}
		key := strings.ToLower(strings.TrimSpace(kv[:eq]))
		val := strings.TrimSpace(kv[eq+1:])
		switch key {
		case "name":
			out.Name = val
		case "addr":
			out.ProxyAddr = val
		case "suffix":
			// Tolerate "dmsg" for ".dmsg": the leading dot is easy to drop in a
			// shell array and a suffix without it matches nothing.
			if val != "" && !strings.HasPrefix(val, ".") {
				val = "." + val
			}
			out.DomainSuffix = val
		case "sk":
			if out.Kind == ResolverKindSkynet {
				return out, fmt.Errorf("resolver spec %q: sk= is only meaningful for a dmsg resolver", spec)
			}
			var sk cipher.SecKey
			if err := sk.Set(val); err != nil {
				return out, fmt.Errorf("resolver spec %q: invalid sk=: %w", spec, err)
			}
			out.SecretKey = &sk
		case "upstream":
			out.UpstreamSOCKS = val
		case "chain":
			b, err := strconv.ParseBool(val)
			if err != nil {
				return out, fmt.Errorf("resolver spec %q: chain= wants true/false, got %q", spec, val)
			}
			out.Chain = &b
		case "alias":
			out.Alias = val
		default:
			return out, fmt.Errorf("resolver spec %q: unknown option %q (want name, addr, suffix, sk, upstream, chain or alias)", spec, key)
		}
	}
	return out, nil
}

// ParseResolverSpecs parses a comma-separated list of specs — the shape
// cmdutil.SkyenvArray hands back for RESOLVERS=('…' '…') — assigning each
// unnamed entry a stable default name.
func ParseResolverSpecs(csv string) ([]ResolverConfig, error) {
	var out []ResolverConfig
	for _, spec := range strings.Split(csv, ",") {
		if strings.TrimSpace(spec) == "" {
			continue
		}
		r, err := ParseResolverSpec(spec)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	// Deterministic order regardless of how the shell array was written, so a
	// regen of the same skywire.conf produces a byte-identical config. Sort
	// BEFORE naming, or the generated r0/r1 names would not match the order
	// they end up in and would shuffle between regens.
	sort.SliceStable(out, func(i, j int) bool { return out[i].ProxyPort < out[j].ProxyPort })
	NormalizeResolvers(out)
	return out, nil
}
