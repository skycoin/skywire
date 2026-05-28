//go:build js

// Package genvisor pkg/skywireconfig/genvisor/marshal_js.go
//
// Hand-rolled streaming JSON serializer for visorconfig.V1. Lives
// in the js build-tag because the WASM-clean path can't use
// encoding/json (reflect runtime helpers TinyGo's stdlib doesn't
// link). The output matches what `json.MarshalIndent(v, "", "  ")`
// would produce on the !js build path, byte-for-byte for the
// common cases (and structurally for the rest), so the
// "Download skywire.json" button on the TinyGo install-page
// variant produces a config equivalent to the Go-WASM version.
//
// Implementation style:
//
//   - One `*objCtx` per JSON object; the close method emits the
//     trailing `\n  }` for nonempty objects and a bare `}` for
//     empty ones.
//   - `field(key)` is called BEFORE writing the value; it emits
//     the comma-newline-indent-quoted-key-colon-space prefix. The
//     value writer (e.g. `writeQuoted`, `marshalDmsg`) appends
//     immediately after.
//   - omitempty fields are guarded by an explicit `if`; the
//     writer never sees a skipped field.
//
// Field ORDER mirrors the V1 struct's declaration order (which
// is the order encoding/json uses for marshaling). Embedded
// *Common fields (version, sk, pk) appear at the V1 level where
// the embedding declaration sits — at the top of the object.
package genvisor

import (
	"strconv"
	"strings"
	"time"

	appspec "github.com/skycoin/skywire/pkg/app/appserver/spec"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	dmsgspec "github.com/skycoin/skywire/pkg/dmsgc/spec"
	tnspec "github.com/skycoin/skywire/pkg/transport/network/spec"
	tspec "github.com/skycoin/skywire/pkg/transport/spec"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// MustMarshalJSON renders v as indented JSON. Mirrors the !js
// signature so the WASM install-page calls the same function in
// either Go-WASM or TinyGo-WASM builds.
func MustMarshalJSON(v *visorconfig.V1) []byte {
	if v == nil {
		return []byte("null")
	}
	var b strings.Builder
	marshalV1(&b, v, 0)
	return []byte(b.String())
}

// ---------- writer infrastructure ----------

// objCtx tracks emission state for one JSON object level. The
// "written" flag becomes true after the first field, so subsequent
// fields know to emit a leading comma. close() also reads it to
// decide whether the closing `}` needs a preceding newline+indent.
type objCtx struct {
	w       *strings.Builder
	indent  int
	written bool
}

// newObj opens a JSON object at the given indent and returns its
// emission context. Indent is the depth of THIS object's `{`; field
// values inside it are emitted at indent+1.
func newObj(w *strings.Builder, indent int) *objCtx {
	w.WriteByte('{')
	return &objCtx{w: w, indent: indent}
}

// close finalizes the object. Empty objects collapse to `{}` (no
// newline before the close brace); nonempty objects end with a
// newline+indent matching the opening `{`'s level.
func (o *objCtx) close() {
	if o.written {
		o.w.WriteByte('\n')
		writeIndent(o.w, o.indent)
	}
	o.w.WriteByte('}')
}

// field emits the comma-newline-indent-quoted-key-colon-space
// prefix for the next field. Callers immediately append the value.
func (o *objCtx) field(name string) {
	if o.written {
		o.w.WriteByte(',')
	}
	o.w.WriteByte('\n')
	writeIndent(o.w, o.indent+1)
	o.w.WriteByte('"')
	o.w.WriteString(name)
	o.w.WriteString(`": `)
	o.written = true
}

// writeIndent writes `depth*2` spaces. Matches the indent= "  "
// argument passed to json.MarshalIndent in the !js build.
func writeIndent(w *strings.Builder, depth int) {
	for i := 0; i < depth; i++ {
		w.WriteString("  ")
	}
}

// writeQuoted writes a JSON-quoted string with stdlib escaping.
// strconv.Quote produces double-quoted output with the standard
// escape sequences and \u escapes — same as encoding/json.
func writeQuoted(w *strings.Builder, s string) {
	w.WriteString(strconv.Quote(s))
}

// writeBool writes "true" or "false".
func writeBool(w *strings.Builder, b bool) {
	if b {
		w.WriteString("true")
	} else {
		w.WriteString("false")
	}
}

// writeInt writes a base-10 signed integer.
func writeInt(w *strings.Builder, n int64) {
	w.WriteString(strconv.FormatInt(n, 10))
}

// writeUint writes a base-10 unsigned integer.
func writeUint(w *strings.Builder, n uint64) {
	w.WriteString(strconv.FormatUint(n, 10))
}

// writePubKey writes a cipher.PubKey as its hex form quoted. Empty
// (zero-array) PubKeys produce "000…000" — encoding/json's
// MarshalText for cipher.PubKey returns the same.
func writePubKey(w *strings.Builder, pk cipher.PubKey) {
	w.WriteByte('"')
	w.WriteString(pk.Hex())
	w.WriteByte('"')
}

// writeSecKey writes a cipher.SecKey as its hex form quoted.
func writeSecKey(w *strings.Builder, sk cipher.SecKey) {
	w.WriteByte('"')
	w.WriteString(sk.Hex())
	w.WriteByte('"')
}

// writeDuration writes a visorconfig.Duration as a quoted
// time.Duration string (e.g. `"10s"`). Matches Duration's
// MarshalJSON method in the !js build.
func writeDuration(w *strings.Builder, d visorconfig.Duration) {
	w.WriteByte('"')
	w.WriteString(time.Duration(d).String())
	w.WriteByte('"')
}

// writeStringSlice writes a JSON array of strings. Empty or nil
// slices emit `[]`. Indent is the depth of the slice's opening `[`.
func writeStringSlice(w *strings.Builder, vals []string, indent int) {
	if len(vals) == 0 {
		w.WriteString("[]")
		return
	}
	w.WriteByte('[')
	for i, s := range vals {
		if i > 0 {
			w.WriteByte(',')
		}
		w.WriteByte('\n')
		writeIndent(w, indent+1)
		writeQuoted(w, s)
	}
	w.WriteByte('\n')
	writeIndent(w, indent)
	w.WriteByte(']')
}

// writePubKeySlice writes a JSON array of cipher.PubKeys. Nil
// slices emit `null` (encoding/json behavior); empty non-nil
// slices emit `[]`.
func writePubKeySlice(w *strings.Builder, vals []cipher.PubKey, indent int) {
	if vals == nil {
		w.WriteString("null")
		return
	}
	if len(vals) == 0 {
		w.WriteString("[]")
		return
	}
	w.WriteByte('[')
	for i, pk := range vals {
		if i > 0 {
			w.WriteByte(',')
		}
		w.WriteByte('\n')
		writeIndent(w, indent+1)
		writePubKey(w, pk)
	}
	w.WriteByte('\n')
	writeIndent(w, indent)
	w.WriteByte(']')
}

// ---------- per-type marshalers ----------

// marshalV1 emits the top-level V1 JSON.
func marshalV1(w *strings.Builder, v *visorconfig.V1, indent int) {
	o := newObj(w, indent)

	// *Common — embedded; fields appear at this object level.
	if v.Common != nil {
		o.field("version")
		writeQuoted(w, v.Common.Version)
		o.field("sk")
		writeSecKey(w, v.Common.SK)
		o.field("pk")
		writePubKey(w, v.Common.PK)
	}

	// dmsg (no omitempty)
	o.field("dmsg")
	if v.Dmsg == nil {
		w.WriteString("null")
	} else {
		marshalDmsg(w, v.Dmsg, o.indent+1)
	}

	if v.Pty != nil {
		o.field("dmsgpty")
		marshalPty(w, v.Pty, o.indent+1)
	}
	if v.Dmsgscp != nil {
		o.field("dmsgscp")
		marshalDmsgscp(w, v.Dmsgscp, o.indent+1)
	}
	if v.UIServer != nil {
		o.field("ui_server")
		marshalUIServer(w, v.UIServer, o.indent+1)
	}
	if v.LogServer != nil {
		o.field("log_server")
		marshalLogServer(w, v.LogServer, o.indent+1)
	}
	if v.DmsgWeb != nil {
		o.field("dmsg_web")
		marshalDmsgWeb(w, v.DmsgWeb, o.indent+1)
	}
	if v.SkynetWeb != nil {
		o.field("skynet_web")
		marshalSkynetWeb(w, v.SkynetWeb, o.indent+1)
	}
	if v.SkymailBridge != nil {
		o.field("skymail_bridge")
		marshalSkymailBridge(w, v.SkymailBridge, o.indent+1)
	}
	if v.Rewards != nil {
		o.field("rewards")
		marshalRewards(w, v.Rewards, o.indent+1)
	}
	if v.STCP != nil {
		o.field("skywire-tcp")
		marshalSTCP(w, v.STCP, o.indent+1)
	}

	// transport (no omitempty)
	o.field("transport")
	if v.Transport == nil {
		w.WriteString("null")
	} else {
		marshalTransport(w, v.Transport, o.indent+1)
	}

	// routing (no omitempty)
	o.field("routing")
	if v.Routing == nil {
		w.WriteString("null")
	} else {
		marshalRouting(w, v.Routing, o.indent+1)
	}

	if v.UptimeTracker != nil {
		o.field("uptime_tracker")
		marshalUptimeTracker(w, v.UptimeTracker, o.indent+1)
	}

	// launcher (no omitempty)
	o.field("launcher")
	if v.Launcher == nil {
		w.WriteString("null")
	} else {
		marshalLauncher(w, v.Launcher, o.indent+1)
	}

	if v.Stats != nil {
		o.field("stats")
		marshalStats(w, v.Stats, o.indent+1)
	}
	if v.Skychat != nil {
		o.field("skychat")
		marshalSkychat(w, v.Skychat, o.indent+1)
	}

	o.field("survey_whitelist")
	writePubKeySlice(w, v.SurveyWhitelist, o.indent+1)

	if len(v.UserSurveyWhitelist) > 0 {
		o.field("user_survey_whitelist")
		writePubKeySlice(w, v.UserSurveyWhitelist, o.indent+1)
	}

	o.field("hypervisors")
	writePubKeySlice(w, v.Hypervisors, o.indent+1)

	o.field("cli_addr")
	writeQuoted(w, v.CLIAddr)
	o.field("log_level")
	writeQuoted(w, v.LogLevel)
	o.field("local_path")
	writeQuoted(w, v.LocalPath)
	o.field("stun_servers")
	writeStringSlice(w, v.StunServers, o.indent+1)

	if v.ShutdownTimeout != 0 {
		o.field("shutdown_timeout")
		writeDuration(w, v.ShutdownTimeout)
	}

	o.field("is_public")
	writeBool(w, v.IsPublic)

	if v.PublicVisorConfig != nil {
		o.field("public_visor")
		marshalPublicVisor(w, v.PublicVisorConfig, o.indent+1)
	}

	o.field("geoip")
	writeQuoted(w, v.GeoIP)

	o.field("persistent_transports")
	marshalPersistentTransports(w, v.PersistentTransports, o.indent+1)

	if v.ConfService != "" {
		o.field("conf_service")
		writeQuoted(w, v.ConfService)
	}
	if v.ConfServiceDmsg != "" {
		o.field("conf_service_dmsg")
		writeQuoted(w, v.ConfServiceDmsg)
	}
	if v.SurveyClientSK != "" {
		o.field("survey_client_sk")
		writeQuoted(w, v.SurveyClientSK)
	}
	if v.RewardAddress != "" {
		o.field("reward_address")
		writeQuoted(w, v.RewardAddress)
	}
	if v.RewardSystem != "" {
		o.field("reward_system")
		writeQuoted(w, v.RewardSystem)
	}
	if v.RewardSystemDmsg != "" {
		o.field("reward_system_dmsg")
		writeQuoted(w, v.RewardSystemDmsg)
	}
	if v.MemoryLimit != "" {
		o.field("memory_limit")
		writeQuoted(w, v.MemoryLimit)
	}

	if v.Hypervisor != nil {
		o.field("hypervisor")
		marshalHypervisor(w, v.Hypervisor, o.indent+1)
	}

	o.close()
}

// marshalDmsg emits the dmsg config — uses the polymorphic
// single-object-vs-array shape encoded by DmsgConfig.MarshalJSON
// in the !js build. Here we replicate the same logic: emit the
// single Deployment object when Deployments has exactly one entry
// (or when Deployments is empty but the top-level mirror fields
// have content, synthesize a one-element Deployments).
func marshalDmsg(w *strings.Builder, c *dmsgspec.DmsgConfig, indent int) {
	deployments := c.Deployments
	if len(deployments) == 0 && (c.Discovery != "" || c.DiscoveryDmsg != "" || len(c.Servers) > 0 || c.SessionsCount != 0 || c.ConnectedServersType != "" || c.Protocol != "" || len(c.LANServers) > 0 || c.HypervisorDiscovery != "") {
		deployments = []dmsgspec.Deployment{{
			Discovery:            c.Discovery,
			DiscoveryDmsg:        c.DiscoveryDmsg,
			SessionsCount:        c.SessionsCount,
			Servers:              c.Servers,
			ConnectedServersType: c.ConnectedServersType,
			Protocol:             c.Protocol,
			LANServers:           c.LANServers,
			HypervisorDiscovery:  c.HypervisorDiscovery,
		}}
	}
	if len(deployments) == 1 {
		marshalDmsgDeployment(w, deployments[0], indent)
		return
	}
	// Multi-deployment: array of objects.
	if len(deployments) == 0 {
		w.WriteString("[]")
		return
	}
	w.WriteByte('[')
	for i, d := range deployments {
		if i > 0 {
			w.WriteByte(',')
		}
		w.WriteByte('\n')
		writeIndent(w, indent+1)
		marshalDmsgDeployment(w, d, indent+1)
	}
	w.WriteByte('\n')
	writeIndent(w, indent)
	w.WriteByte(']')
}

func marshalDmsgDeployment(w *strings.Builder, d dmsgspec.Deployment, indent int) {
	o := newObj(w, indent)
	if d.Discovery != "" {
		o.field("discovery")
		writeQuoted(w, d.Discovery)
	}
	if d.DiscoveryDmsg != "" {
		o.field("discovery_dmsg")
		writeQuoted(w, d.DiscoveryDmsg)
	}
	if d.SessionsCount != 0 {
		o.field("sessions_count")
		writeInt(w, int64(d.SessionsCount))
	}
	if len(d.Servers) > 0 {
		o.field("servers")
		marshalDiscEntries(w, d.Servers, o.indent+1)
	}
	if d.ConnectedServersType != "" {
		o.field("servers_type")
		writeQuoted(w, d.ConnectedServersType)
	}
	if d.Protocol != "" {
		o.field("protocol")
		writeQuoted(w, d.Protocol)
	}
	if len(d.LANServers) > 0 {
		o.field("lan_servers")
		marshalDiscEntries(w, d.LANServers, o.indent+1)
	}
	if d.HypervisorDiscovery != "" {
		o.field("hypervisor_discovery")
		writeQuoted(w, d.HypervisorDiscovery)
	}
	o.close()
}

func marshalDiscEntries(w *strings.Builder, entries []*disc.Entry, indent int) {
	if entries == nil {
		w.WriteString("null")
		return
	}
	if len(entries) == 0 {
		w.WriteString("[]")
		return
	}
	w.WriteByte('[')
	for i, e := range entries {
		if i > 0 {
			w.WriteByte(',')
		}
		w.WriteByte('\n')
		writeIndent(w, indent+1)
		marshalDiscEntry(w, e, indent+1)
	}
	w.WriteByte('\n')
	writeIndent(w, indent)
	w.WriteByte(']')
}

func marshalDiscEntry(w *strings.Builder, e *disc.Entry, indent int) {
	if e == nil {
		w.WriteString("null")
		return
	}
	o := newObj(w, indent)
	o.field("version")
	writeQuoted(w, e.Version)
	o.field("sequence")
	writeUint(w, e.Sequence)
	o.field("timestamp")
	writeInt(w, e.Timestamp)
	o.field("static")
	writePubKey(w, e.Static)
	if e.Client != nil {
		o.field("client")
		marshalDiscClient(w, e.Client, o.indent+1)
	}
	if e.ClientType != "" {
		o.field("client_type")
		writeQuoted(w, e.ClientType)
	}
	if e.Server != nil {
		o.field("server")
		marshalDiscServer(w, e.Server, o.indent+1)
	}
	if e.Signature != "" {
		o.field("signature")
		writeQuoted(w, e.Signature)
	}
	if e.Protocol != "" {
		o.field("protocol")
		writeQuoted(w, e.Protocol)
	}
	o.close()
}

func marshalDiscClient(w *strings.Builder, c *disc.Client, indent int) {
	o := newObj(w, indent)
	o.field("delegated_servers")
	writePubKeySlice(w, c.DelegatedServers, o.indent+1)
	o.close()
}

func marshalDiscServer(w *strings.Builder, s *disc.Server, indent int) {
	o := newObj(w, indent)
	o.field("address")
	writeQuoted(w, s.Address)
	if s.AddressV6 != "" {
		o.field("address_v6")
		writeQuoted(w, s.AddressV6)
	}
	o.field("availableSessions")
	writeInt(w, int64(s.AvailableSessions))
	if s.ServerType != "" {
		o.field("serverType")
		writeQuoted(w, s.ServerType)
	}
	if s.DHTBootstrap {
		o.field("dht_bootstrap")
		writeBool(w, s.DHTBootstrap)
	}
	o.close()
}

func marshalPty(w *strings.Builder, d *visorconfig.Pty, indent int) {
	o := newObj(w, indent)
	o.field("dmsg_port")
	writeUint(w, uint64(d.DmsgPort))
	o.field("cli_network")
	writeQuoted(w, d.CLINet)
	o.field("cli_address")
	writeQuoted(w, d.CLIAddr)
	o.field("whitelist")
	writePubKeySlice(w, d.Whitelist, o.indent+1)
	if d.SshListen != "" {
		o.field("ssh_listen")
		writeQuoted(w, d.SshListen)
	}
	o.close()
}

func marshalDmsgscp(w *strings.Builder, d *visorconfig.Dmsgscp, indent int) {
	o := newObj(w, indent)
	if d.Disabled {
		o.field("disabled")
		writeBool(w, d.Disabled)
	}
	if d.DmsgPort != 0 {
		o.field("dmsg_port")
		writeUint(w, uint64(d.DmsgPort))
	}
	if d.RootDir != "" {
		o.field("root_dir")
		writeQuoted(w, d.RootDir)
	}
	if len(d.Whitelist) > 0 {
		o.field("whitelist")
		writePubKeySlice(w, d.Whitelist, o.indent+1)
	}
	o.close()
}

func marshalUIServer(w *strings.Builder, u *visorconfig.UIServer, indent int) {
	o := newObj(w, indent)
	o.field("enable")
	writeBool(w, u.Enable)
	o.field("local_addr")
	writeQuoted(w, u.LocalAddr)
	o.field("dmsg_port")
	writeUint(w, uint64(u.DmsgPort))
	o.field("dmsg_whitelist")
	writePubKeySlice(w, u.DmsgWhitelist, o.indent+1)
	o.field("survey_dir")
	writeQuoted(w, u.SurveyDir)
	o.close()
}

func marshalLogServer(w *strings.Builder, l *visorconfig.LogServer, indent int) {
	o := newObj(w, indent)
	o.field("local_addr")
	writeQuoted(w, l.LocalAddr)
	o.close()
}

func marshalRewards(w *strings.Builder, r *visorconfig.RewardsConfig, indent int) {
	o := newObj(w, indent)
	o.field("enable")
	writeBool(w, r.Enable)
	o.field("work_dir")
	writeQuoted(w, r.WorkDir)
	if len(r.Whitelist) > 0 {
		o.field("whitelist")
		writePubKeySlice(w, r.Whitelist, o.indent+1)
	}
	if r.CanonicalDomain != "" {
		o.field("canonical_domain")
		writeQuoted(w, r.CanonicalDomain)
	}
	if r.SkycoinNode != "" {
		o.field("skycoin_node")
		writeQuoted(w, r.SkycoinNode)
	}
	if r.LoginNode != "" {
		o.field("login_node")
		writeQuoted(w, r.LoginNode)
	}
	o.close()
}

func marshalDmsgWeb(w *strings.Builder, d *visorconfig.DmsgWebConfig, indent int) {
	o := newObj(w, indent)
	o.field("enable")
	writeBool(w, d.Enable)
	if d.ProxyPort != 0 {
		o.field("proxy_port")
		writeUint(w, uint64(d.ProxyPort))
	}
	if d.WebPort != 0 {
		o.field("web_port")
		writeUint(w, uint64(d.WebPort))
	}
	if d.DomainSuffix != "" {
		o.field("domain_suffix")
		writeQuoted(w, d.DomainSuffix)
	}
	if d.UpstreamSOCKS != "" {
		o.field("upstream_socks")
		writeQuoted(w, d.UpstreamSOCKS)
	}
	if d.TLSMITM {
		o.field("tls_mitm")
		writeBool(w, d.TLSMITM)
	}
	if d.TLSPort != 0 {
		o.field("tls_port")
		writeUint(w, uint64(d.TLSPort))
	}
	if d.TLSCAPath != "" {
		o.field("tls_ca_path")
		writeQuoted(w, d.TLSCAPath)
	}
	if d.TLSCAKeyPath != "" {
		o.field("tls_ca_key_path")
		writeQuoted(w, d.TLSCAKeyPath)
	}
	o.close()
}

func marshalSkynetWeb(w *strings.Builder, s *visorconfig.SkynetWebConfig, indent int) {
	o := newObj(w, indent)
	o.field("enable")
	writeBool(w, s.Enable)
	if s.ProxyPort != 0 {
		o.field("proxy_port")
		writeUint(w, uint64(s.ProxyPort))
	}
	if s.WebPort != 0 {
		o.field("web_port")
		writeUint(w, uint64(s.WebPort))
	}
	if s.DomainSuffix != "" {
		o.field("domain_suffix")
		writeQuoted(w, s.DomainSuffix)
	}
	if s.UpstreamSOCKS != "" {
		o.field("upstream_socks")
		writeQuoted(w, s.UpstreamSOCKS)
	}
	if s.RouteTimeout != 0 {
		o.field("route_timeout")
		writeDuration(w, s.RouteTimeout)
	}
	if s.TLSMITM {
		o.field("tls_mitm")
		writeBool(w, s.TLSMITM)
	}
	if s.TLSPort != 0 {
		o.field("tls_port")
		writeUint(w, uint64(s.TLSPort))
	}
	if s.TLSCAPath != "" {
		o.field("tls_ca_path")
		writeQuoted(w, s.TLSCAPath)
	}
	if s.TLSCAKeyPath != "" {
		o.field("tls_ca_key_path")
		writeQuoted(w, s.TLSCAKeyPath)
	}
	o.close()
}

func marshalSkymailBridge(w *strings.Builder, s *visorconfig.SkymailBridgeConfig, indent int) {
	o := newObj(w, indent)
	o.field("enable")
	writeBool(w, s.Enable)
	if s.Addr != "" {
		o.field("addr")
		writeQuoted(w, s.Addr)
	}
	if s.Mode != "" {
		o.field("mode")
		writeQuoted(w, s.Mode)
	}
	if s.Suffix != "" {
		o.field("suffix")
		writeQuoted(w, s.Suffix)
	}
	if s.HeloName != "" {
		o.field("helo_name")
		writeQuoted(w, s.HeloName)
	}
	if s.RemotePort != 0 {
		o.field("remote_port")
		writeUint(w, uint64(s.RemotePort))
	}
	o.close()
}

func marshalSTCP(w *strings.Builder, s *tnspec.STCPConfig, indent int) {
	o := newObj(w, indent)
	o.field("pk_table")
	marshalPKTable(w, s.PKTable, o.indent+1)
	o.field("listening_address")
	writeQuoted(w, s.ListeningAddress)
	o.close()
}

func marshalPKTable(w *strings.Builder, m map[cipher.PubKey]string, indent int) {
	if m == nil {
		w.WriteString("null")
		return
	}
	if len(m) == 0 {
		w.WriteString("{}")
		return
	}
	o := newObj(w, indent)
	for pk, addr := range m {
		o.field(pk.Hex())
		writeQuoted(w, addr)
	}
	o.close()
}

func marshalTransport(w *strings.Builder, t *visorconfig.Transport, indent int) {
	o := newObj(w, indent)
	o.field("discovery")
	writeQuoted(w, t.Discovery)
	if t.DiscoveryDmsg != "" {
		o.field("discovery_dmsg")
		writeQuoted(w, t.DiscoveryDmsg)
	}
	o.field("address_resolver")
	writeQuoted(w, t.AddressResolver)
	if t.AddressResolverDmsg != "" {
		o.field("address_resolver_dmsg")
		writeQuoted(w, t.AddressResolverDmsg)
	}
	o.field("public_autoconnect")
	writeBool(w, t.PublicAutoconnect)
	o.field("transport_setup")
	writePubKeySlice(w, t.TransportSetupPKs, o.indent+1)
	if len(t.UserTransportSetupPKs) > 0 {
		o.field("user_transport_setup")
		writePubKeySlice(w, t.UserTransportSetupPKs, o.indent+1)
	}
	if t.TPSetupSK != nil {
		o.field("tps_sk")
		writeSecKey(w, *t.TPSetupSK)
	}
	if t.TPSDmsg != nil {
		o.field("tps_dmsg")
		marshalTPSDmsg(w, t.TPSDmsg, o.indent+1)
	}
	o.field("log_store")
	if t.LogStore == nil {
		w.WriteString("null")
	} else {
		marshalLogStore(w, t.LogStore, o.indent+1)
	}
	o.field("stcpr_port")
	writeInt(w, int64(t.StcprPort))
	o.field("sudph_port")
	writeInt(w, int64(t.SudphPort))
	if t.ARTransportLimit != 0 {
		o.field("ar_transport_limit")
		writeInt(w, int64(t.ARTransportLimit))
	}
	o.close()
}

func marshalTPSDmsg(w *strings.Builder, t *visorconfig.TPSDmsgConfig, indent int) {
	o := newObj(w, indent)
	o.field("min_sessions")
	writeInt(w, int64(t.MinSessions))
	o.field("server_type")
	writeQuoted(w, t.ServerType)
	o.close()
}

func marshalLogStore(w *strings.Builder, l *visorconfig.LogStore, indent int) {
	o := newObj(w, indent)
	o.field("type")
	writeQuoted(w, l.Type)
	o.field("location")
	writeQuoted(w, l.Location)
	o.field("rotation_interval")
	writeDuration(w, l.RotationInterval)
	o.close()
}

func marshalRouting(w *strings.Builder, r *visorconfig.Routing, indent int) {
	o := newObj(w, indent)
	if len(r.RouteSetupNodes) > 0 {
		o.field("route_setup_nodes")
		writePubKeySlice(w, r.RouteSetupNodes, o.indent+1)
	}
	if len(r.UserRouteSetupNodes) > 0 {
		o.field("user_route_setup_nodes")
		writePubKeySlice(w, r.UserRouteSetupNodes, o.indent+1)
	}
	if r.RouteSetupSK != nil {
		o.field("route_setup_sk")
		writeSecKey(w, *r.RouteSetupSK)
	}
	o.field("route_finder")
	writeQuoted(w, r.RouteFinder)
	if r.RouteFinderDmsg != "" {
		o.field("route_finder_dmsg")
		writeQuoted(w, r.RouteFinderDmsg)
	}
	if r.RouteFinderTimeout != 0 {
		o.field("route_finder_timeout")
		writeDuration(w, r.RouteFinderTimeout)
	}
	o.field("min_hops")
	writeUint(w, uint64(r.MinHops))
	if r.CalculateRoutes {
		o.field("calculate_routes")
		writeBool(w, r.CalculateRoutes)
	}
	if r.MuxRoutes != 0 {
		o.field("mux_routes")
		writeInt(w, int64(r.MuxRoutes))
	}
	if len(r.TransportPreference) > 0 {
		o.field("transport_preference")
		writeStringSlice(w, r.TransportPreference, o.indent+1)
	}
	o.close()
}

func marshalUptimeTracker(w *strings.Builder, u *visorconfig.UptimeTracker, indent int) {
	o := newObj(w, indent)
	o.field("addr")
	writeQuoted(w, u.Addr)
	if u.AddrDmsg != "" {
		o.field("addr_dmsg")
		writeQuoted(w, u.AddrDmsg)
	}
	o.close()
}

func marshalPublicVisor(w *strings.Builder, p *visorconfig.PublicVisorConfig, indent int) {
	o := newObj(w, indent)
	if p.RegistrationTimeout != 0 {
		o.field("registration_timeout")
		writeDuration(w, p.RegistrationTimeout)
	}
	if p.MaxTransports != 0 {
		o.field("max_transports")
		writeInt(w, int64(p.MaxTransports))
	}
	o.close()
}

func marshalLauncher(w *strings.Builder, l *visorconfig.Launcher, indent int) {
	o := newObj(w, indent)
	o.field("service_discovery")
	writeQuoted(w, l.ServiceDisc)
	if l.ServiceDiscDmsg != "" {
		o.field("service_discovery_dmsg")
		writeQuoted(w, l.ServiceDiscDmsg)
	}
	o.field("apps")
	marshalAppsList(w, []appspec.AppConfig(l.Apps), o.indent+1)
	o.field("server_addr")
	writeQuoted(w, l.ServerAddr)
	o.field("bin_path")
	writeQuoted(w, l.BinPath)
	o.field("display_node_ip")
	writeBool(w, l.DisplayNodeIP)
	if l.HeartbeatInterval != 0 {
		o.field("heartbeat_interval")
		writeDuration(w, l.HeartbeatInterval)
	}
	o.close()
}

// marshalAppsList renders the visor's app list using the same
// "Args as a shell-like string" rendering the !js
// appsList.MarshalJSON method uses. Nil and empty both emit `[]`
// — the native MarshalJSON materializes a non-nil
// `make([]appConfigOnDisk, len(apps))` slice unconditionally, so
// json.Marshal there always writes `[]` for a zero-length input,
// never `null`. We mirror that.
func marshalAppsList(w *strings.Builder, apps []appspec.AppConfig, indent int) {
	if len(apps) == 0 {
		w.WriteString("[]")
		return
	}
	w.WriteByte('[')
	for i, a := range apps {
		if i > 0 {
			w.WriteByte(',')
		}
		w.WriteByte('\n')
		writeIndent(w, indent+1)
		marshalApp(w, a, indent+1)
	}
	w.WriteByte('\n')
	writeIndent(w, indent)
	w.WriteByte(']')
}

func marshalApp(w *strings.Builder, a appspec.AppConfig, indent int) {
	o := newObj(w, indent)
	o.field("name")
	writeQuoted(w, a.Name)
	if a.Binary != "" {
		o.field("binary")
		writeQuoted(w, a.Binary)
	}
	if len(a.Args) > 0 {
		o.field("args")
		writeQuoted(w, joinShellArgs(a.Args))
	}
	o.field("auto_start")
	writeBool(w, a.AutoStart)
	o.field("port")
	writeUint(w, uint64(a.Port))
	if a.User != "" {
		o.field("user")
		writeQuoted(w, a.User)
	}
	if a.Group != "" {
		o.field("group")
		writeQuoted(w, a.Group)
	}
	if a.WorkDir != "" {
		o.field("work_dir")
		writeQuoted(w, a.WorkDir)
	}
	if len(a.Env) > 0 {
		o.field("env")
		writeStringSlice(w, a.Env, o.indent+1)
	}
	if a.LauncherMode != "" {
		o.field("launcher_mode")
		writeQuoted(w, a.LauncherMode)
	}
	o.close()
}

func marshalStats(w *strings.Builder, s *visorconfig.Stats, indent int) {
	o := newObj(w, indent)
	if s.Path != "" {
		o.field("path")
		writeQuoted(w, s.Path)
	}
	if s.SampleInterval != 0 {
		o.field("sample_interval")
		writeDuration(w, s.SampleInterval)
	}
	if s.RetentionDays != 0 {
		o.field("retention_days")
		writeInt(w, int64(s.RetentionDays))
	}
	if s.CXOPublishWindow != 0 {
		o.field("cxo_publish_window")
		writeInt(w, int64(s.CXOPublishWindow))
	}
	if s.Disabled {
		o.field("disabled")
		writeBool(w, s.Disabled)
	}
	o.close()
}

func marshalSkychat(w *strings.Builder, s *visorconfig.Skychat, indent int) {
	o := newObj(w, indent)
	if s.GroupHistoryDB != "" {
		o.field("group_history_db")
		writeQuoted(w, s.GroupHistoryDB)
	}
	o.close()
}

func marshalPersistentTransports(w *strings.Builder, pt []tspec.PersistentTransports, indent int) {
	if pt == nil {
		w.WriteString("null")
		return
	}
	if len(pt) == 0 {
		w.WriteString("[]")
		return
	}
	w.WriteByte('[')
	for i, p := range pt {
		if i > 0 {
			w.WriteByte(',')
		}
		w.WriteByte('\n')
		writeIndent(w, indent+1)
		o := newObj(w, indent+1)
		o.field("pk")
		writePubKey(w, p.PK)
		o.field("type")
		writeQuoted(w, string(p.NetType))
		o.close()
	}
	w.WriteByte('\n')
	writeIndent(w, indent)
	w.WriteByte(']')
}

func marshalHypervisor(w *strings.Builder, h *visorconfig.HypervisorConfig, indent int) {
	o := newObj(w, indent)
	o.field("enable")
	writeBool(w, h.Enable)
	// UIAssets / PK / SK / DmsgDiscovery are json:"-" — omitted.
	o.field("db_path")
	writeQuoted(w, h.DBPath)
	o.field("enable_auth")
	writeBool(w, h.EnableAuth)
	o.field("cookies")
	marshalCookieConfig(w, h.Cookies, o.indent+1)
	if h.DmsgPort != 0 {
		o.field("dmsg_port")
		writeUint(w, uint64(h.DmsgPort))
	}
	o.field("http_addr")
	writeQuoted(w, h.HTTPAddr)
	o.field("enable_tls")
	writeBool(w, h.EnableTLS)
	o.field("tls_cert_file")
	writeQuoted(w, h.TLSCertFile)
	o.field("tls_key_file")
	writeQuoted(w, h.TLSKeyFile)
	o.field("tp_viz")
	marshalTPViz(w, h.TPViz, o.indent+1)
	if h.LANDmsgServer != nil {
		o.field("lan_dmsg_server")
		marshalLANDmsgServer(w, h.LANDmsgServer, o.indent+1)
	}
	if h.CXOSubscribeInterval != 0 {
		o.field("cxo_subscribe_interval")
		writeDuration(w, h.CXOSubscribeInterval)
	}
	o.close()
}

func marshalCookieConfig(w *strings.Builder, c visorconfig.CookieConfig, indent int) {
	o := newObj(w, indent)
	o.field("hash_key")
	writeQuoted(w, c.HashKey.String())
	o.field("block_key")
	writeQuoted(w, c.BlockKey.String())
	o.field("expires_duration")
	writeInt(w, int64(c.ExpiresDuration))
	o.field("path")
	writeQuoted(w, c.Path)
	o.field("domain")
	writeQuoted(w, c.Domain)
	o.close()
}

func marshalTPViz(w *strings.Builder, t visorconfig.TPVizConfig, indent int) {
	o := newObj(w, indent)
	o.field("enable")
	writeBool(w, t.Enable)
	if t.SurveyDir != "" {
		o.field("survey_dir")
		writeQuoted(w, t.SurveyDir)
	}
	if t.CacheMaxAge != 0 {
		o.field("cache_max_age")
		writeInt(w, int64(t.CacheMaxAge))
	}
	o.close()
}

// joinShellArgs mirrors the unexported visorconfig.joinArgs (in
// args.go) so the WASM-side marshaler emits app Args as the same
// shell-quoted string the native MarshalJSON produces. Inlined
// rather than imported because joinArgs isn't exported and the
// WASM-clean code path doesn't pull the rest of visorconfig's args
// helpers either way.
func joinShellArgs(args []string) string {
	if len(args) == 0 {
		return ""
	}
	parts := make([]string, len(args))
	for i, a := range args {
		switch {
		case a == "":
			parts[i] = `""`
		case strings.ContainsAny(a, " \t\n\"'\\"):
			esc := strings.ReplaceAll(a, `\`, `\\`)
			esc = strings.ReplaceAll(esc, `"`, `\"`)
			parts[i] = `"` + esc + `"`
		default:
			parts[i] = a
		}
	}
	return strings.Join(parts, " ")
}

func marshalLANDmsgServer(w *strings.Builder, l *visorconfig.LANDmsgServerConf, indent int) {
	o := newObj(w, indent)
	o.field("enable")
	writeBool(w, l.Enable)
	if l.Port != 0 {
		o.field("port")
		writeInt(w, int64(l.Port))
	}
	if l.PublicAddress != "" {
		o.field("public_address")
		writeQuoted(w, l.PublicAddress)
	}
	if l.MaxSessions != 0 {
		o.field("max_sessions")
		writeInt(w, int64(l.MaxSessions))
	}
	// PK / SK with omitempty: emit only when not the zero array. The
	// type doesn't expose an IsNull predicate but cipher.PubKey does
	// via Null().
	if !l.PK.Null() {
		o.field("pk")
		writePubKey(w, l.PK)
	}
	if !l.SK.Null() {
		o.field("sk")
		writeSecKey(w, l.SK)
	}
	if l.PublicDiscoveryURL != "" {
		o.field("public_discovery_url")
		writeQuoted(w, l.PublicDiscoveryURL)
	}
	o.close()
}
