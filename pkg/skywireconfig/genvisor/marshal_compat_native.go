//go:build !js

// Package genvisor pkg/skywireconfig/genvisor/marshal_compat_native.go
//
// Native-build copy of the hand-rolled marshaler in marshal_js.go,
// with every symbol renamed to a `Native` suffix to avoid colliding
// with the real MustMarshalJSON in genvisor_native.go. Purpose: the
// comparison test (marshal_compare_test.go) needs to run the
// hand-rolled marshaler under GOOS=linux/darwin/windows to validate
// its output against encoding/json.MarshalIndent. Without this
// duplicate the test would require a TinyGo build + wasm runner —
// not worth the CI complexity for a regression check that fires
// once per V1 schema change.
//
// Drift policy: this file MUST stay in sync with marshal_js.go.
// Regenerate by stripping the //go:build js line and running the
// rename sed pipeline (see the commit message in the PR
// introducing this file). The comparison test will fail if you
// forget — the rendered JSON would diverge from the stdlib codec.
//
// Original file header from marshal_js.go follows:
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
//   - One `*objCtxNative` per JSON object; the close method emits the
//     trailing `\n  }` for nonempty objects and a bare `}` for
//     empty ones.
//   - `field(key)` is called BEFORE writing the value; it emits
//     the comma-newline-indent-quoted-key-colon-space prefix. The
//     value writer (e.g. `writeQuotedNative`, `marshalDmsgNative`) appends
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

// MustMarshalJSONNative renders v as indented JSON. Mirrors the !js
// signature so the WASM install-page calls the same function in
// either Go-WASM or TinyGo-WASM builds.
func MustMarshalJSONNative(v *visorconfig.V1) []byte {
	if v == nil {
		return []byte("null")
	}
	var b strings.Builder
	marshalV1Native(&b, v, 0)
	return []byte(b.String())
}

// ---------- writer infrastructure ----------

// objCtxNative tracks emission state for one JSON object level. The
// "written" flag becomes true after the first field, so subsequent
// fields know to emit a leading comma. close() also reads it to
// decide whether the closing `}` needs a preceding newline+indent.
type objCtxNative struct {
	w       *strings.Builder
	indent  int
	written bool
}

// newObjNative opens a JSON object at the given indent and returns its
// emission context. Indent is the depth of THIS object's `{`; field
// values inside it are emitted at indent+1.
func newObjNative(w *strings.Builder, indent int) *objCtxNative {
	w.WriteByte('{')
	return &objCtxNative{w: w, indent: indent}
}

// close finalizes the object. Empty objects collapse to `{}` (no
// newline before the close brace); nonempty objects end with a
// newline+indent matching the opening `{`'s level.
func (o *objCtxNative) close() {
	if o.written {
		o.w.WriteByte('\n')
		writeIndentNative(o.w, o.indent)
	}
	o.w.WriteByte('}')
}

// field emits the comma-newline-indent-quoted-key-colon-space
// prefix for the next field. Callers immediately append the value.
func (o *objCtxNative) field(name string) {
	if o.written {
		o.w.WriteByte(',')
	}
	o.w.WriteByte('\n')
	writeIndentNative(o.w, o.indent+1)
	o.w.WriteByte('"')
	o.w.WriteString(name)
	o.w.WriteString(`": `)
	o.written = true
}

// writeIndentNative writes `depth*2` spaces. Matches the indent= "  "
// argument passed to json.MarshalIndent in the !js build.
func writeIndentNative(w *strings.Builder, depth int) {
	for i := 0; i < depth; i++ {
		w.WriteString("  ")
	}
}

// writeQuotedNative writes a JSON-quoted string with stdlib escaping.
// strconv.Quote produces double-quoted output with the standard
// escape sequences and \u escapes — same as encoding/json.
func writeQuotedNative(w *strings.Builder, s string) {
	w.WriteString(strconv.Quote(s))
}

// writeBoolNative writes "true" or "false".
func writeBoolNative(w *strings.Builder, b bool) {
	if b {
		w.WriteString("true")
	} else {
		w.WriteString("false")
	}
}

// writeIntNative writes a base-10 signed integer.
func writeIntNative(w *strings.Builder, n int64) {
	w.WriteString(strconv.FormatInt(n, 10))
}

// writeUintNative writes a base-10 unsigned integer.
func writeUintNative(w *strings.Builder, n uint64) {
	w.WriteString(strconv.FormatUint(n, 10))
}

// writePubKeyNative writes a cipher.PubKey as its hex form quoted. Empty
// (zero-array) PubKeys produce "000…000" — encoding/json's
// MarshalText for cipher.PubKey returns the same.
func writePubKeyNative(w *strings.Builder, pk cipher.PubKey) {
	w.WriteByte('"')
	w.WriteString(pk.Hex())
	w.WriteByte('"')
}

// writeSecKeyNative writes a cipher.SecKey as its hex form quoted.
func writeSecKeyNative(w *strings.Builder, sk cipher.SecKey) {
	w.WriteByte('"')
	w.WriteString(sk.Hex())
	w.WriteByte('"')
}

// writeDurationNative writes a visorconfig.Duration as a quoted
// time.Duration string (e.g. `"10s"`). Matches Duration's
// MarshalJSON method in the !js build.
func writeDurationNative(w *strings.Builder, d visorconfig.Duration) {
	w.WriteByte('"')
	w.WriteString(time.Duration(d).String())
	w.WriteByte('"')
}

// writeStringSliceNative writes a JSON array of strings. Empty or nil
// slices emit `[]`. Indent is the depth of the slice's opening `[`.
func writeStringSliceNative(w *strings.Builder, vals []string, indent int) {
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
		writeIndentNative(w, indent+1)
		writeQuotedNative(w, s)
	}
	w.WriteByte('\n')
	writeIndentNative(w, indent)
	w.WriteByte(']')
}

// writePubKeyNativeSlice writes a JSON array of cipher.PubKeys. Nil
// slices emit `null` (encoding/json behavior); empty non-nil
// slices emit `[]`.
func writePubKeyNativeSlice(w *strings.Builder, vals []cipher.PubKey, indent int) {
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
		writeIndentNative(w, indent+1)
		writePubKeyNative(w, pk)
	}
	w.WriteByte('\n')
	writeIndentNative(w, indent)
	w.WriteByte(']')
}

// ---------- per-type marshalers ----------

// marshalV1Native emits the top-level V1 JSON.
func marshalV1Native(w *strings.Builder, v *visorconfig.V1, indent int) {
	o := newObjNative(w, indent)

	// *Common — embedded; fields appear at this object level.
	if v.Common != nil {
		o.field("version")
		writeQuotedNative(w, v.Common.Version)
		o.field("sk")
		writeSecKeyNative(w, v.Common.SK)
		o.field("pk")
		writePubKeyNative(w, v.Common.PK)
	}

	// dmsg (no omitempty)
	o.field("dmsg")
	if v.Dmsg == nil {
		w.WriteString("null")
	} else {
		marshalDmsgNative(w, v.Dmsg, o.indent+1)
	}

	if v.Pty != nil {
		o.field("pty")
		marshalPtyNative(w, v.Pty, o.indent+1)
	}
	if v.Dmsgscp != nil {
		o.field("dmsgscp")
		marshalDmsgscpNative(w, v.Dmsgscp, o.indent+1)
	}
	if v.UIServer != nil {
		o.field("ui_server")
		marshalUIServerNative(w, v.UIServer, o.indent+1)
	}
	if v.LogServer != nil {
		o.field("log_server")
		marshalLogServerNative(w, v.LogServer, o.indent+1)
	}
	if v.DmsgWeb != nil {
		o.field("dmsg_web")
		marshalDmsgWebNative(w, v.DmsgWeb, o.indent+1)
	}
	if v.SkynetWeb != nil {
		o.field("skynet_web")
		marshalSkynetWebNative(w, v.SkynetWeb, o.indent+1)
	}
	if v.SkymailBridge != nil {
		o.field("skymail_bridge")
		marshalSkymailBridgeNative(w, v.SkymailBridge, o.indent+1)
	}
	if v.Rewards != nil {
		o.field("rewards")
		marshalRewardsNative(w, v.Rewards, o.indent+1)
	}
	if v.STCP != nil {
		o.field("skywire-tcp")
		marshalSTCPNative(w, v.STCP, o.indent+1)
	}

	// transport (no omitempty)
	o.field("transport")
	if v.Transport == nil {
		w.WriteString("null")
	} else {
		marshalTransportNative(w, v.Transport, o.indent+1)
	}

	// routing (no omitempty)
	o.field("routing")
	if v.Routing == nil {
		w.WriteString("null")
	} else {
		marshalRoutingNative(w, v.Routing, o.indent+1)
	}

	if v.UptimeTracker != nil {
		o.field("uptime_tracker")
		marshalUptimeTrackerNative(w, v.UptimeTracker, o.indent+1)
	}

	// launcher (no omitempty)
	o.field("launcher")
	if v.Launcher == nil {
		w.WriteString("null")
	} else {
		marshalLauncherNative(w, v.Launcher, o.indent+1)
	}

	if v.Stats != nil {
		o.field("stats")
		marshalStatsNative(w, v.Stats, o.indent+1)
	}
	if v.Skychat != nil {
		o.field("skychat")
		marshalSkychatNative(w, v.Skychat, o.indent+1)
	}

	o.field("survey_whitelist")
	writePubKeyNativeSlice(w, v.SurveyWhitelist, o.indent+1)

	if len(v.UserSurveyWhitelist) > 0 {
		o.field("user_survey_whitelist")
		writePubKeyNativeSlice(w, v.UserSurveyWhitelist, o.indent+1)
	}

	o.field("hypervisors")
	writePubKeyNativeSlice(w, v.Hypervisors, o.indent+1)

	o.field("cli_addr")
	writeQuotedNative(w, v.CLIAddr)
	o.field("log_level")
	writeQuotedNative(w, v.LogLevel)
	o.field("local_path")
	writeQuotedNative(w, v.LocalPath)
	o.field("stun_servers")
	writeStringSliceNative(w, v.StunServers, o.indent+1)

	if v.ShutdownTimeout != 0 {
		o.field("shutdown_timeout")
		writeDurationNative(w, v.ShutdownTimeout)
	}

	o.field("is_public")
	writeBoolNative(w, v.IsPublic)

	if v.PublicVisorConfig != nil {
		o.field("public_visor")
		marshalPublicVisorNative(w, v.PublicVisorConfig, o.indent+1)
	}

	o.field("geoip")
	writeQuotedNative(w, v.GeoIP)

	o.field("persistent_transports")
	marshalPersistentTransportsNative(w, v.PersistentTransports, o.indent+1)

	if v.ConfService != "" {
		o.field("conf_service")
		writeQuotedNative(w, v.ConfService)
	}
	if v.ConfServiceDmsg != "" {
		o.field("conf_service_dmsg")
		writeQuotedNative(w, v.ConfServiceDmsg)
	}
	if v.SurveyClientSK != "" {
		o.field("survey_client_sk")
		writeQuotedNative(w, v.SurveyClientSK)
	}
	if v.RewardAddress != "" {
		o.field("reward_address")
		writeQuotedNative(w, v.RewardAddress)
	}
	if v.RewardSystem != "" {
		o.field("reward_system")
		writeQuotedNative(w, v.RewardSystem)
	}
	if v.RewardSystemDmsg != "" {
		o.field("reward_system_dmsg")
		writeQuotedNative(w, v.RewardSystemDmsg)
	}
	if v.MemoryLimit != "" {
		o.field("memory_limit")
		writeQuotedNative(w, v.MemoryLimit)
	}

	if v.Hypervisor != nil {
		o.field("hypervisor")
		marshalHypervisorNative(w, v.Hypervisor, o.indent+1)
	}

	o.close()
}

// marshalDmsgNative emits the dmsg config — uses the polymorphic
// single-object-vs-array shape encoded by DmsgConfig.MarshalJSON
// in the !js build. Here we replicate the same logic: emit the
// single Deployment object when Deployments has exactly one entry
// (or when Deployments is empty but the top-level mirror fields
// have content, synthesize a one-element Deployments).
func marshalDmsgNative(w *strings.Builder, c *dmsgspec.DmsgConfig, indent int) {
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
		marshalDmsgDeploymentNative(w, deployments[0], indent)
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
		writeIndentNative(w, indent+1)
		marshalDmsgDeploymentNative(w, d, indent+1)
	}
	w.WriteByte('\n')
	writeIndentNative(w, indent)
	w.WriteByte(']')
}

func marshalDmsgDeploymentNative(w *strings.Builder, d dmsgspec.Deployment, indent int) {
	o := newObjNative(w, indent)
	if d.Discovery != "" {
		o.field("discovery")
		writeQuotedNative(w, d.Discovery)
	}
	if d.DiscoveryDmsg != "" {
		o.field("discovery_dmsg")
		writeQuotedNative(w, d.DiscoveryDmsg)
	}
	if d.SessionsCount != 0 {
		o.field("sessions_count")
		writeIntNative(w, int64(d.SessionsCount))
	}
	if len(d.Servers) > 0 {
		o.field("servers")
		marshalDiscEntriesNative(w, d.Servers, o.indent+1)
	}
	if d.ConnectedServersType != "" {
		o.field("servers_type")
		writeQuotedNative(w, d.ConnectedServersType)
	}
	if d.Protocol != "" {
		o.field("protocol")
		writeQuotedNative(w, d.Protocol)
	}
	if len(d.LANServers) > 0 {
		o.field("lan_servers")
		marshalDiscEntriesNative(w, d.LANServers, o.indent+1)
	}
	if d.HypervisorDiscovery != "" {
		o.field("hypervisor_discovery")
		writeQuotedNative(w, d.HypervisorDiscovery)
	}
	o.close()
}

func marshalDiscEntriesNative(w *strings.Builder, entries []*disc.Entry, indent int) {
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
		writeIndentNative(w, indent+1)
		marshalDiscEntryNative(w, e, indent+1)
	}
	w.WriteByte('\n')
	writeIndentNative(w, indent)
	w.WriteByte(']')
}

func marshalDiscEntryNative(w *strings.Builder, e *disc.Entry, indent int) {
	if e == nil {
		w.WriteString("null")
		return
	}
	o := newObjNative(w, indent)
	o.field("version")
	writeQuotedNative(w, e.Version)
	o.field("sequence")
	writeUintNative(w, e.Sequence)
	o.field("timestamp")
	writeIntNative(w, e.Timestamp)
	o.field("static")
	writePubKeyNative(w, e.Static)
	if e.Client != nil {
		o.field("client")
		marshalDiscClientNative(w, e.Client, o.indent+1)
	}
	if e.ClientType != "" {
		o.field("client_type")
		writeQuotedNative(w, e.ClientType)
	}
	if e.Server != nil {
		o.field("server")
		marshalDiscServerNative(w, e.Server, o.indent+1)
	}
	if e.Signature != "" {
		o.field("signature")
		writeQuotedNative(w, e.Signature)
	}
	if e.Protocol != "" {
		o.field("protocol")
		writeQuotedNative(w, e.Protocol)
	}
	o.close()
}

func marshalDiscClientNative(w *strings.Builder, c *disc.Client, indent int) {
	o := newObjNative(w, indent)
	o.field("delegated_servers")
	writePubKeyNativeSlice(w, c.DelegatedServers, o.indent+1)
	o.close()
}

func marshalDiscServerNative(w *strings.Builder, s *disc.Server, indent int) {
	o := newObjNative(w, indent)
	o.field("address")
	writeQuotedNative(w, s.Address)
	if s.AddressV6 != "" {
		o.field("address_v6")
		writeQuotedNative(w, s.AddressV6)
	}
	o.field("availableSessions")
	writeIntNative(w, int64(s.AvailableSessions))
	if s.ServerType != "" {
		o.field("serverType")
		writeQuotedNative(w, s.ServerType)
	}
	if s.DHTBootstrap {
		o.field("dht_bootstrap")
		writeBoolNative(w, s.DHTBootstrap)
	}
	o.close()
}

func marshalPtyNative(w *strings.Builder, d *visorconfig.Pty, indent int) {
	o := newObjNative(w, indent)
	o.field("dmsg_port")
	writeUintNative(w, uint64(d.DmsgPort))
	o.field("cli_network")
	writeQuotedNative(w, d.CLINet)
	o.field("cli_address")
	writeQuotedNative(w, d.CLIAddr)
	o.field("whitelist")
	writePubKeyNativeSlice(w, d.Whitelist, o.indent+1)
	if d.SshListen != "" {
		o.field("ssh_listen")
		writeQuotedNative(w, d.SshListen)
	}
	o.close()
}

func marshalDmsgscpNative(w *strings.Builder, d *visorconfig.Dmsgscp, indent int) {
	o := newObjNative(w, indent)
	if d.Disabled {
		o.field("disabled")
		writeBoolNative(w, d.Disabled)
	}
	if d.DmsgPort != 0 {
		o.field("dmsg_port")
		writeUintNative(w, uint64(d.DmsgPort))
	}
	if d.RootDir != "" {
		o.field("root_dir")
		writeQuotedNative(w, d.RootDir)
	}
	if len(d.Whitelist) > 0 {
		o.field("whitelist")
		writePubKeyNativeSlice(w, d.Whitelist, o.indent+1)
	}
	o.close()
}

func marshalUIServerNative(w *strings.Builder, u *visorconfig.UIServer, indent int) {
	o := newObjNative(w, indent)
	o.field("enable")
	writeBoolNative(w, u.Enable)
	o.field("local_addr")
	writeQuotedNative(w, u.LocalAddr)
	o.field("dmsg_port")
	writeUintNative(w, uint64(u.DmsgPort))
	o.field("dmsg_whitelist")
	writePubKeyNativeSlice(w, u.DmsgWhitelist, o.indent+1)
	o.field("survey_dir")
	writeQuotedNative(w, u.SurveyDir)
	o.close()
}

func marshalLogServerNative(w *strings.Builder, l *visorconfig.LogServer, indent int) {
	o := newObjNative(w, indent)
	o.field("local_addr")
	writeQuotedNative(w, l.LocalAddr)
	o.close()
}

func marshalRewardsNative(w *strings.Builder, r *visorconfig.RewardsConfig, indent int) {
	o := newObjNative(w, indent)
	o.field("enable")
	writeBoolNative(w, r.Enable)
	o.field("work_dir")
	writeQuotedNative(w, r.WorkDir)
	if len(r.Whitelist) > 0 {
		o.field("whitelist")
		writePubKeyNativeSlice(w, r.Whitelist, o.indent+1)
	}
	if r.CanonicalDomain != "" {
		o.field("canonical_domain")
		writeQuotedNative(w, r.CanonicalDomain)
	}
	if r.SkycoinNode != "" {
		o.field("skycoin_node")
		writeQuotedNative(w, r.SkycoinNode)
	}
	if r.LoginNode != "" {
		o.field("login_node")
		writeQuotedNative(w, r.LoginNode)
	}
	o.close()
}

func marshalDmsgWebNative(w *strings.Builder, d *visorconfig.DmsgWebConfig, indent int) {
	o := newObjNative(w, indent)
	o.field("enable")
	writeBoolNative(w, d.Enable)
	if d.ProxyPort != 0 {
		o.field("proxy_port")
		writeUintNative(w, uint64(d.ProxyPort))
	}
	if d.WebPort != 0 {
		o.field("web_port")
		writeUintNative(w, uint64(d.WebPort))
	}
	if d.DomainSuffix != "" {
		o.field("domain_suffix")
		writeQuotedNative(w, d.DomainSuffix)
	}
	if d.UpstreamSOCKS != "" {
		o.field("upstream_socks")
		writeQuotedNative(w, d.UpstreamSOCKS)
	}
	if d.TLSMITM {
		o.field("tls_mitm")
		writeBoolNative(w, d.TLSMITM)
	}
	if d.TLSPort != 0 {
		o.field("tls_port")
		writeUintNative(w, uint64(d.TLSPort))
	}
	if d.TLSCAPath != "" {
		o.field("tls_ca_path")
		writeQuotedNative(w, d.TLSCAPath)
	}
	if d.TLSCAKeyPath != "" {
		o.field("tls_ca_key_path")
		writeQuotedNative(w, d.TLSCAKeyPath)
	}
	o.close()
}

func marshalSkynetWebNative(w *strings.Builder, s *visorconfig.SkynetWebConfig, indent int) {
	o := newObjNative(w, indent)
	o.field("enable")
	writeBoolNative(w, s.Enable)
	if s.ProxyPort != 0 {
		o.field("proxy_port")
		writeUintNative(w, uint64(s.ProxyPort))
	}
	if s.WebPort != 0 {
		o.field("web_port")
		writeUintNative(w, uint64(s.WebPort))
	}
	if s.DomainSuffix != "" {
		o.field("domain_suffix")
		writeQuotedNative(w, s.DomainSuffix)
	}
	if s.UpstreamSOCKS != "" {
		o.field("upstream_socks")
		writeQuotedNative(w, s.UpstreamSOCKS)
	}
	if s.RouteTimeout != 0 {
		o.field("route_timeout")
		writeDurationNative(w, s.RouteTimeout)
	}
	if s.TLSMITM {
		o.field("tls_mitm")
		writeBoolNative(w, s.TLSMITM)
	}
	if s.TLSPort != 0 {
		o.field("tls_port")
		writeUintNative(w, uint64(s.TLSPort))
	}
	if s.TLSCAPath != "" {
		o.field("tls_ca_path")
		writeQuotedNative(w, s.TLSCAPath)
	}
	if s.TLSCAKeyPath != "" {
		o.field("tls_ca_key_path")
		writeQuotedNative(w, s.TLSCAKeyPath)
	}
	o.close()
}

func marshalSkymailBridgeNative(w *strings.Builder, s *visorconfig.SkymailBridgeConfig, indent int) {
	o := newObjNative(w, indent)
	o.field("enable")
	writeBoolNative(w, s.Enable)
	if s.Addr != "" {
		o.field("addr")
		writeQuotedNative(w, s.Addr)
	}
	if s.Mode != "" {
		o.field("mode")
		writeQuotedNative(w, s.Mode)
	}
	if s.Suffix != "" {
		o.field("suffix")
		writeQuotedNative(w, s.Suffix)
	}
	if s.HeloName != "" {
		o.field("helo_name")
		writeQuotedNative(w, s.HeloName)
	}
	if s.RemotePort != 0 {
		o.field("remote_port")
		writeUintNative(w, uint64(s.RemotePort))
	}
	o.close()
}

func marshalSTCPNative(w *strings.Builder, s *tnspec.STCPConfig, indent int) {
	o := newObjNative(w, indent)
	o.field("pk_table")
	marshalPKTableNative(w, s.PKTable, o.indent+1)
	o.field("listening_address")
	writeQuotedNative(w, s.ListeningAddress)
	o.close()
}

func marshalPKTableNative(w *strings.Builder, m map[cipher.PubKey]string, indent int) {
	if m == nil {
		w.WriteString("null")
		return
	}
	if len(m) == 0 {
		w.WriteString("{}")
		return
	}
	o := newObjNative(w, indent)
	for pk, addr := range m {
		o.field(pk.Hex())
		writeQuotedNative(w, addr)
	}
	o.close()
}

func marshalTransportNative(w *strings.Builder, t *visorconfig.Transport, indent int) {
	o := newObjNative(w, indent)
	o.field("discovery")
	writeQuotedNative(w, t.Discovery)
	if t.DiscoveryDmsg != "" {
		o.field("discovery_dmsg")
		writeQuotedNative(w, t.DiscoveryDmsg)
	}
	o.field("address_resolver")
	writeQuotedNative(w, t.AddressResolver)
	if t.AddressResolverDmsg != "" {
		o.field("address_resolver_dmsg")
		writeQuotedNative(w, t.AddressResolverDmsg)
	}
	o.field("public_autoconnect")
	writeBoolNative(w, t.PublicAutoconnect)
	o.field("transport_setup")
	writePubKeyNativeSlice(w, t.TransportSetupPKs, o.indent+1)
	if len(t.UserTransportSetupPKs) > 0 {
		o.field("user_transport_setup")
		writePubKeyNativeSlice(w, t.UserTransportSetupPKs, o.indent+1)
	}
	if t.TPSetupSK != nil {
		o.field("tps_sk")
		writeSecKeyNative(w, *t.TPSetupSK)
	}
	if t.TPSDmsg != nil {
		o.field("tps_dmsg")
		marshalTPSDmsgNative(w, t.TPSDmsg, o.indent+1)
	}
	o.field("log_store")
	if t.LogStore == nil {
		w.WriteString("null")
	} else {
		marshalLogStoreNative(w, t.LogStore, o.indent+1)
	}
	o.field("stcpr_port")
	writeIntNative(w, int64(t.StcprPort))
	o.field("sudph_port")
	writeIntNative(w, int64(t.SudphPort))
	if t.ARTransportLimit != 0 {
		o.field("ar_transport_limit")
		writeIntNative(w, int64(t.ARTransportLimit))
	}
	o.close()
}

func marshalTPSDmsgNative(w *strings.Builder, t *visorconfig.TPSDmsgConfig, indent int) {
	o := newObjNative(w, indent)
	o.field("min_sessions")
	writeIntNative(w, int64(t.MinSessions))
	o.field("server_type")
	writeQuotedNative(w, t.ServerType)
	o.close()
}

func marshalLogStoreNative(w *strings.Builder, l *visorconfig.LogStore, indent int) {
	o := newObjNative(w, indent)
	o.field("type")
	writeQuotedNative(w, l.Type)
	o.field("location")
	writeQuotedNative(w, l.Location)
	o.field("rotation_interval")
	writeDurationNative(w, l.RotationInterval)
	o.close()
}

func marshalRoutingNative(w *strings.Builder, r *visorconfig.Routing, indent int) {
	o := newObjNative(w, indent)
	if len(r.RouteSetupNodes) > 0 {
		o.field("route_setup_nodes")
		writePubKeyNativeSlice(w, r.RouteSetupNodes, o.indent+1)
	}
	if len(r.UserRouteSetupNodes) > 0 {
		o.field("user_route_setup_nodes")
		writePubKeyNativeSlice(w, r.UserRouteSetupNodes, o.indent+1)
	}
	if r.RouteSetupSK != nil {
		o.field("route_setup_sk")
		writeSecKeyNative(w, *r.RouteSetupSK)
	}
	o.field("route_finder")
	writeQuotedNative(w, r.RouteFinder)
	if r.RouteFinderDmsg != "" {
		o.field("route_finder_dmsg")
		writeQuotedNative(w, r.RouteFinderDmsg)
	}
	if r.RouteFinderTimeout != 0 {
		o.field("route_finder_timeout")
		writeDurationNative(w, r.RouteFinderTimeout)
	}
	o.field("min_hops")
	writeUintNative(w, uint64(r.MinHops))
	if r.CalculateRoutes {
		o.field("calculate_routes")
		writeBoolNative(w, r.CalculateRoutes)
	}
	if r.MuxRoutes != 0 {
		o.field("mux_routes")
		writeIntNative(w, int64(r.MuxRoutes))
	}
	if len(r.TransportPreference) > 0 {
		o.field("transport_preference")
		writeStringSliceNative(w, r.TransportPreference, o.indent+1)
	}
	o.close()
}

func marshalUptimeTrackerNative(w *strings.Builder, u *visorconfig.UptimeTracker, indent int) {
	o := newObjNative(w, indent)
	o.field("addr")
	writeQuotedNative(w, u.Addr)
	if u.AddrDmsg != "" {
		o.field("addr_dmsg")
		writeQuotedNative(w, u.AddrDmsg)
	}
	o.close()
}

func marshalPublicVisorNative(w *strings.Builder, p *visorconfig.PublicVisorConfig, indent int) {
	o := newObjNative(w, indent)
	if p.RegistrationTimeout != 0 {
		o.field("registration_timeout")
		writeDurationNative(w, p.RegistrationTimeout)
	}
	if p.MaxTransports != 0 {
		o.field("max_transports")
		writeIntNative(w, int64(p.MaxTransports))
	}
	o.close()
}

func marshalLauncherNative(w *strings.Builder, l *visorconfig.Launcher, indent int) {
	o := newObjNative(w, indent)
	o.field("service_discovery")
	writeQuotedNative(w, l.ServiceDisc)
	if l.ServiceDiscDmsg != "" {
		o.field("service_discovery_dmsg")
		writeQuotedNative(w, l.ServiceDiscDmsg)
	}
	o.field("apps")
	marshalAppsListNative(w, []appspec.AppConfig(l.Apps), o.indent+1)
	o.field("server_addr")
	writeQuotedNative(w, l.ServerAddr)
	o.field("bin_path")
	writeQuotedNative(w, l.BinPath)
	o.field("display_node_ip")
	writeBoolNative(w, l.DisplayNodeIP)
	if l.HeartbeatInterval != 0 {
		o.field("heartbeat_interval")
		writeDurationNative(w, l.HeartbeatInterval)
	}
	o.close()
}

// marshalAppsListNative renders the visor's app list using the same
// "Args as a shell-like string" rendering the !js
// appsList.MarshalJSON method uses. Nil and empty both emit `[]`
// — the native MarshalJSON materializes a non-nil
// `make([]appConfigOnDisk, len(apps))` slice unconditionally, so
// json.Marshal there always writes `[]` for a zero-length input,
// never `null`. We mirror that.
func marshalAppsListNative(w *strings.Builder, apps []appspec.AppConfig, indent int) {
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
		writeIndentNative(w, indent+1)
		marshalAppNative(w, a, indent+1)
	}
	w.WriteByte('\n')
	writeIndentNative(w, indent)
	w.WriteByte(']')
}

func marshalAppNative(w *strings.Builder, a appspec.AppConfig, indent int) {
	o := newObjNative(w, indent)
	o.field("name")
	writeQuotedNative(w, a.Name)
	if a.Binary != "" {
		o.field("binary")
		writeQuotedNative(w, a.Binary)
	}
	if len(a.Args) > 0 {
		o.field("args")
		writeQuotedNative(w, joinShellArgsNative(a.Args))
	}
	o.field("auto_start")
	writeBoolNative(w, a.AutoStart)
	o.field("port")
	writeUintNative(w, uint64(a.Port))
	if a.User != "" {
		o.field("user")
		writeQuotedNative(w, a.User)
	}
	if a.Group != "" {
		o.field("group")
		writeQuotedNative(w, a.Group)
	}
	if a.WorkDir != "" {
		o.field("work_dir")
		writeQuotedNative(w, a.WorkDir)
	}
	if len(a.Env) > 0 {
		o.field("env")
		writeStringSliceNative(w, a.Env, o.indent+1)
	}
	if a.LauncherMode != "" {
		o.field("launcher_mode")
		writeQuotedNative(w, a.LauncherMode)
	}
	o.close()
}

func marshalStatsNative(w *strings.Builder, s *visorconfig.Stats, indent int) {
	o := newObjNative(w, indent)
	if s.Path != "" {
		o.field("path")
		writeQuotedNative(w, s.Path)
	}
	if s.SampleInterval != 0 {
		o.field("sample_interval")
		writeDurationNative(w, s.SampleInterval)
	}
	if s.RetentionDays != 0 {
		o.field("retention_days")
		writeIntNative(w, int64(s.RetentionDays))
	}
	if s.CXOPublishWindow != 0 {
		o.field("cxo_publish_window")
		writeIntNative(w, int64(s.CXOPublishWindow))
	}
	if s.Disabled {
		o.field("disabled")
		writeBoolNative(w, s.Disabled)
	}
	o.close()
}

func marshalSkychatNative(w *strings.Builder, s *visorconfig.Skychat, indent int) {
	o := newObjNative(w, indent)
	if s.GroupHistoryDB != "" {
		o.field("group_history_db")
		writeQuotedNative(w, s.GroupHistoryDB)
	}
	o.close()
}

func marshalPersistentTransportsNative(w *strings.Builder, pt []tspec.PersistentTransports, indent int) {
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
		writeIndentNative(w, indent+1)
		o := newObjNative(w, indent+1)
		o.field("pk")
		writePubKeyNative(w, p.PK)
		o.field("type")
		writeQuotedNative(w, string(p.NetType))
		o.close()
	}
	w.WriteByte('\n')
	writeIndentNative(w, indent)
	w.WriteByte(']')
}

func marshalHypervisorNative(w *strings.Builder, h *visorconfig.HypervisorConfig, indent int) {
	o := newObjNative(w, indent)
	o.field("enable")
	writeBoolNative(w, h.Enable)
	// UIAssets / PK / SK / DmsgDiscovery are json:"-" — omitted.
	o.field("db_path")
	writeQuotedNative(w, h.DBPath)
	o.field("enable_auth")
	writeBoolNative(w, h.EnableAuth)
	o.field("enable_pk_endpoint")
	writeBoolNative(w, h.EnablePKEndpoint)
	o.field("cookies")
	marshalCookieConfigNative(w, h.Cookies, o.indent+1)
	if h.DmsgPort != 0 {
		o.field("dmsg_port")
		writeUintNative(w, uint64(h.DmsgPort))
	}
	o.field("http_addr")
	writeQuotedNative(w, h.HTTPAddr)
	o.field("enable_tls")
	writeBoolNative(w, h.EnableTLS)
	o.field("tls_cert_file")
	writeQuotedNative(w, h.TLSCertFile)
	o.field("tls_key_file")
	writeQuotedNative(w, h.TLSKeyFile)
	o.field("tp_viz")
	marshalTPVizNative(w, h.TPViz, o.indent+1)
	if h.LANDmsgServer != nil {
		o.field("lan_dmsg_server")
		marshalLANDmsgServerNative(w, h.LANDmsgServer, o.indent+1)
	}
	if h.CXOSubscribeInterval != 0 {
		o.field("cxo_subscribe_interval")
		writeDurationNative(w, h.CXOSubscribeInterval)
	}
	o.close()
}

func marshalCookieConfigNative(w *strings.Builder, c visorconfig.CookieConfig, indent int) {
	o := newObjNative(w, indent)
	o.field("hash_key")
	writeQuotedNative(w, c.HashKey.String())
	o.field("block_key")
	writeQuotedNative(w, c.BlockKey.String())
	o.field("expires_duration")
	writeIntNative(w, int64(c.ExpiresDuration))
	o.field("path")
	writeQuotedNative(w, c.Path)
	o.field("domain")
	writeQuotedNative(w, c.Domain)
	o.close()
}

func marshalTPVizNative(w *strings.Builder, t visorconfig.TPVizConfig, indent int) {
	o := newObjNative(w, indent)
	o.field("enable")
	writeBoolNative(w, t.Enable)
	if t.SurveyDir != "" {
		o.field("survey_dir")
		writeQuotedNative(w, t.SurveyDir)
	}
	if t.CacheMaxAge != 0 {
		o.field("cache_max_age")
		writeIntNative(w, int64(t.CacheMaxAge))
	}
	o.close()
}

// joinShellArgsNative mirrors the unexported visorconfig.joinArgs (in
// args.go) so the WASM-side marshaler emits app Args as the same
// shell-quoted string the native MarshalJSON produces. Inlined
// rather than imported because joinArgs isn't exported and the
// WASM-clean code path doesn't pull the rest of visorconfig's args
// helpers either way.
func joinShellArgsNative(args []string) string {
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

func marshalLANDmsgServerNative(w *strings.Builder, l *visorconfig.LANDmsgServerConf, indent int) {
	o := newObjNative(w, indent)
	o.field("enable")
	writeBoolNative(w, l.Enable)
	if l.Port != 0 {
		o.field("port")
		writeIntNative(w, int64(l.Port))
	}
	if l.PublicAddress != "" {
		o.field("public_address")
		writeQuotedNative(w, l.PublicAddress)
	}
	if l.MaxSessions != 0 {
		o.field("max_sessions")
		writeIntNative(w, int64(l.MaxSessions))
	}
	// PK / SK with omitempty: emit only when not the zero array. The
	// type doesn't expose an IsNull predicate but cipher.PubKey does
	// via Null().
	if !l.PK.Null() {
		o.field("pk")
		writePubKeyNative(w, l.PK)
	}
	if !l.SK.Null() {
		o.field("sk")
		writeSecKeyNative(w, l.SK)
	}
	if l.PublicDiscoveryURL != "" {
		o.field("public_discovery_url")
		writeQuotedNative(w, l.PublicDiscoveryURL)
	}
	o.close()
}
