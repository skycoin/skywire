// Package spec pkg/dmsg/dmsgc/spec/spec.go c1-net-dmsg
// and methods here describe the visor's dmsg-subsystem configuration
// at the wire (JSON) layer only — no dmsg client construction, no
// network I/O, no transitive pull on operational packages.
//
// Why split: pkg/dmsg/dmsgc itself imports pkg/dmsg/dmsg (the dmsg client
// implementation) which transitively brings in pty / ipc / bbolt
// vendor packages that don't compile under GOOS=js. That made
// pkg/visor/visorconfig.V1, which has a *dmsgc.DmsgConfig field,
// uncompilable from any WASM consumer (apt-repo install page, doc
// generators, schema validators).
//
// Moving the wire types out into this leaf package lets V1 reference
// dmsgcspec.DmsgConfig instead — V1 compiles under WASM, the
// install-page can render and emit live visor configs, and the
// operational pkg/dmsg/dmsgc keeps doing its job for the visor binary
// via a `type DmsgConfig = spec.DmsgConfig` alias.
//
// All methods on DmsgConfig are pure: JSON marshalers and getters
// over the in-memory slice. None of them touch network, filesystem,
// or transitive operational packages. Adding an operational method
// (one that dials, fetches, etc.) belongs in pkg/dmsg/dmsgc, not here.
package spec

import (
	"net/url"
	"strings"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
)

// Deployment is one (dmsg-discovery, transit-servers) pair. Each
// Deployment is independent — different deployments may use different
// servers, sessions counts, protocols, or LAN preferences.
//
// The discovery's PK is encoded in DiscoveryDmsg (form
// `dmsg://<PK>:<port>`) and is extracted on demand by callers that
// need it for dmsgfirst registration; there is no separate PK field.
type Deployment struct {
	Discovery            string        `json:"discovery,omitempty"`
	DiscoveryDmsg        string        `json:"discovery_dmsg,omitempty"`
	SessionsCount        int           `json:"sessions_count,omitempty"`
	Servers              []*disc.Entry `json:"servers,omitempty"`
	ConnectedServersType string        `json:"servers_type,omitempty"`
	Protocol             string        `json:"protocol,omitempty"`
	// Carriers is the ordered carrier preference for reaching dmsg servers —
	// "tcp", "quic", "ws", "wt" (see dmsg.Config.Carriers). Empty = the native
	// default (QUIC when the server advertises it, else TCP). Set e.g. ["ws"]
	// or ["wt"] on networks that only allow 443/HTTPS egress.
	Carriers   []string      `json:"carriers,omitempty"`
	LANServers []*disc.Entry `json:"lan_servers,omitempty"`
	// HypervisorDiscovery is an optional override URL pointing at a
	// hypervisor-hosted dmsg-discovery proxy. When set, the dmsg client
	// queries this URL first and falls back to Discovery (the canonical
	// public dmsg-discovery) on error. Pushed automatically by
	// hypervisors with their embedded dmsg server enabled — see
	// LANDmsgServerInfo.DiscoveryURL.
	HypervisorDiscovery string `json:"hypervisor_discovery,omitempty"`
}

// DmsgConfig is the visor-side dmsg subsystem configuration.
//
// JSON polymorphism: a single Deployment object is the single-
// deployment shape that visor configs have always used; an array of
// Deployments is the multi-deployment shape, where each entry pairs
// a dmsg-discovery with the dmsg-servers that reach it. Internally
// the type stores a slice; the single-object shape unmarshals into
// a one-element slice and marshals back to an object.
//
// For backward compatibility with code that reads
// `v.conf.Dmsg.Discovery` etc. directly, the legacy top-level fields
// are kept as a mirror of Deployments[0]. Writers that build a
// DmsgConfig by setting top-level fields directly still work — the
// MarshalJSON synthesizes a one-element Deployments from the
// top-level fields when Deployments is empty.
type DmsgConfig struct {
	// Deployments is the canonical list, populated by UnmarshalJSON.
	Deployments []Deployment `json:"-"`

	// Top-level mirror fields. Read-only after UnmarshalJSON; the
	// canonical state lives in Deployments. Kept to avoid churning
	// the many existing call sites that read Dmsg.Discovery /
	// Dmsg.DiscoveryDmsg / Dmsg.Servers / etc. directly.
	Discovery            string        `json:"-"`
	DiscoveryDmsg        string        `json:"-"`
	SessionsCount        int           `json:"-"`
	Servers              []*disc.Entry `json:"-"`
	ConnectedServersType string        `json:"-"`
	Protocol             string        `json:"-"`
	Carriers             []string      `json:"-"`
	LANServers           []*disc.Entry `json:"-"`
	HypervisorDiscovery  string        `json:"-"`

	// DirectOnly selects the single dmsg client's discovery MODE. Default
	// false = "discovery" (registering fallback: the visor publishes its
	// entry to dmsg-discovery and resolves via it, direct-first). true =
	// "direct-only": static direct client ONLY, no dmsg-discovery — the visor
	// does not register (unreachable by lookup, can't relay, NOT
	// rewards-eligible), a stealthy leaf reaching only statically-known
	// services/servers. Experimental; default stays discovery.
	DirectOnly bool `json:"direct_only,omitempty"`

	// LookupCXO opts the visor into resolving peer dmsg discovery
	// entries from the local CXO clients-by-server snapshot before
	// falling back to an HTTP-over-dmsg lookup. Default false. When
	// true the visor holds dmsg-discovery's clients-by-server feed
	// warm (via cxosub) and serves peer entry lookups already in the
	// snapshot with no per-lookup Noise handshake — the discovery-
	// service CPU cost the deployment-services-over-CXO roadmap
	// targets. HTTP stays the fallback on any snapshot miss, so
	// correctness is unchanged; the flag only trades a periodic bulk
	// snapshot sync for the per-lookup handshake. Conservative
	// default-off, matching how CXO features roll out (opt-in first,
	// then always-on once proven).
	LookupCXO bool `json:"lookup_cxo,omitempty"`

	// Server, when non-nil AND Enabled, runs a dmsg SERVER in-process under
	// the visor's own PK/SK (shared identity), reusing the visor's existing
	// dmsg client for discovery rather than standing up a second transit
	// client. Nil (the default) = no in-process server. See DmsgServerConfig.
	Server *DmsgServerConfig `json:"server,omitempty"`

	// RelayMaxStreams bounds the concurrent streams this visor will relay for
	// peers attached to its dmsg relay acceptor. 0 uses
	// dmsg.DefaultClientMaxRelayedStreams. A negative value refuses to relay at
	// all, for a visor that should never carry other people's traffic.
	RelayMaxStreams int `json:"relay_max_streams,omitempty"`

	// LocalRelay, when non-nil AND Enabled, opens a LOCAL acceptor (unix
	// socket, optionally a loopback listener) onto the same relay the visor
	// already serves over skynet, so a standalone process on this host can
	// hold a dmsg identity WITHOUT holding dmsg server sessions of its own.
	// Nil (the default) = no local acceptor: this is a grant of the visor's
	// sessions and transports, so it is opt-in. See DmsgLocalRelayConfig.
	LocalRelay *DmsgLocalRelayConfig `json:"local_relay,omitempty"`
}

// DmsgLocalRelayConfig configures the visor's LOCAL dmsg relay acceptor
// (dmsg.Client.ServeLocalRelay). Default off — the whole struct is nil in
// generated configs.
//
// What it grants: a process that attaches here dials dmsg under ITS OWN key,
// but its streams are forwarded over THIS visor's dmsg sessions and transports
// and are charged to this visor's relay slots (dmsg.relay_max_streams). The
// attached key never rotates and never appears in dmsg-discovery.
//
// How it is authorized, in two independent layers:
//
//   - The listener. A unix socket at Socket, created with SocketMode (0600 by
//     default), makes the filesystem the gate: only a process running as the
//     visor's user can open it — and such a process can already read the
//     visor's own secret key, so attaching grants it nothing new.
//   - AllowedKeys. The dmsg session handshake is Noise XK, which PROVES the
//     attaching process holds the secret key for the public key it claims, so
//     an entry in this list is authentication and not a hint. Empty means "any
//     key that can open the listener", which is the right default for a 0600
//     socket and the wrong one for anything else.
//
// TCPAddress exists for the cases a unix socket cannot serve (a container
// sharing the host's loopback but not its filesystem, Windows). It has no
// filesystem gate, so it REQUIRES a non-empty AllowedKeys and a loopback
// address; the visor refuses to bind it otherwise rather than quietly opening
// an unauthenticated path to its own transports.
type DmsgLocalRelayConfig struct {
	// Enabled gates the whole acceptor. False (or a nil
	// *DmsgLocalRelayConfig) = the visor serves no local relay.
	Enabled bool `json:"enabled"`
	// Socket is the unix socket path the acceptor listens on. Empty means
	// "<local_path>/dmsg_relay.sock". Set it to "-" to run with no unix
	// socket at all (TCPAddress only).
	Socket string `json:"socket,omitempty"`
	// SocketMode is the unix socket's file mode in octal, as a string
	// ("0600", "0660"). Empty = "0600" (owner only). Widening it to a group
	// mode is how an operator lets a service account that is NOT the visor's
	// user attach; pair that with AllowedKeys, because the group is then the
	// only thing standing between any of its members and this visor's
	// transports.
	SocketMode string `json:"socket_mode,omitempty"`
	// TCPAddress is an optional loopback listen address ("127.0.0.1:7070").
	// Empty = none. Requires AllowedKeys; a non-loopback host is refused.
	TCPAddress string `json:"tcp_address,omitempty"`
	// AllowedKeys, when non-empty, admits ONLY these public keys. Empty
	// leaves the listener's own gate as the only one (see the type comment).
	AllowedKeys []cipher.PubKey `json:"allowed_keys,omitempty"`
}

// DmsgServerConfig configures the OPTIONAL in-process dmsg server co-resident
// with the visor. Default off (the whole struct is nil in generated configs).
// The server shares the visor's PK/SK and its dmsg client, so it advertises
// under the same PK the visor already uses — no second identity, no second
// transit client.
type DmsgServerConfig struct {
	// Enabled gates the in-process server. False (or a nil *DmsgServerConfig)
	// = the visor runs no server.
	Enabled bool `json:"enabled"`
	// LocalAddress is the TCP listen address for inbound sessions. Empty
	// means the server SHARES the visor's transport TCP port — the same cmux
	// that already splits stcpr from WS gains a dmsg branch — so no second
	// port is opened and none has to be forwarded. Set it to pin the server
	// to an address of its own instead (the old default was ":8081", which is
	// what a visor with no transport cmux still falls back to).
	LocalAddress string `json:"local_address,omitempty"`
	// PublicAddress is the externally-reachable address advertised in the
	// server's discovery entry. Empty = don't advertise a public address
	// (the server is reachable only over whatever the listener resolves to).
	PublicAddress string `json:"public_address,omitempty"`
	// ConfigPath, when set, runs the full dmsg-server service (pkg/services/dmsgsrv)
	// inside the visor process from that standalone dmsg-server config file
	// — its own key, listen/public/wss addresses, health endpoint and
	// max_sessions — instead of the bare server on the visor key above.
	// LocalAddress and PublicAddress are ignored when it is set. This is
	// how a host that ran `skywire dmsg server start <file>` as a separate
	// unit folds that server into its visor (DMSGSERVERCONF in skywire.conf).
	ConfigPath string `json:"config_path,omitempty"`
}

// MarshalJSON and UnmarshalJSON live in spec_native.go under
// //go:build !js. encoding/json's reflect-based codec drags
// runtime helpers TinyGo's stdlib lacks; the WASM install-page
// path doesn't need to (de)serialize DmsgConfig — it composes
// the V1 in memory and emits it through genvisor.MustMarshalJSON
// (which itself is build-tag-split for the same reason).

// mirrorPrimary copies Deployments[0] into the legacy top-level fields
// so existing readers keep working in the single-deployment case. For
// multi-deployment configs the mirror reflects the first entry — code
// that needs to iterate uses Deployments directly.
func (c *DmsgConfig) mirrorPrimary() {
	if len(c.Deployments) == 0 {
		return
	}
	d := c.Deployments[0]
	c.Discovery = d.Discovery
	c.DiscoveryDmsg = d.DiscoveryDmsg
	c.SessionsCount = d.SessionsCount
	c.Servers = d.Servers
	c.ConnectedServersType = d.ConnectedServersType
	c.Protocol = d.Protocol
	c.Carriers = d.Carriers
	c.LANServers = d.LANServers
	c.HypervisorDiscovery = d.HypervisorDiscovery
}

// toDeployment snapshots the legacy top-level fields into a Deployment.
func (c *DmsgConfig) toDeployment() Deployment {
	return Deployment{
		Discovery:            c.Discovery,
		DiscoveryDmsg:        c.DiscoveryDmsg,
		SessionsCount:        c.SessionsCount,
		Servers:              c.Servers,
		ConnectedServersType: c.ConnectedServersType,
		Protocol:             c.Protocol,
		Carriers:             c.Carriers,
		LANServers:           c.LANServers,
		HypervisorDiscovery:  c.HypervisorDiscovery,
	}
}

// Primary returns the first deployment, synthesizing one from the
// legacy top-level fields when Deployments is empty. Callers can rely
// on Primary always returning a non-nil pointer for any non-empty
// DmsgConfig.
func (c *DmsgConfig) Primary() *Deployment {
	if len(c.Deployments) > 0 {
		return &c.Deployments[0]
	}
	d := c.toDeployment()
	return &d
}

// AllDeployments returns the list of deployments, synthesizing a
// one-element list from the legacy top-level fields when Deployments
// is empty. Returns nil only when the config is fully zero.
func (c *DmsgConfig) AllDeployments() []Deployment {
	if len(c.Deployments) > 0 {
		return c.Deployments
	}
	if c.Discovery == "" && c.DiscoveryDmsg == "" && len(c.Servers) == 0 && c.HypervisorDiscovery == "" {
		return nil
	}
	return []Deployment{c.toDeployment()}
}

// ResolvedServers unions servers from all deployments, deduping by PK.
// The result is what the direct.Client should be preloaded with so the
// client can dmsg-dial every configured discovery — whether
// deployments share a server set or have disjoint per-deployment sets.
func (c *DmsgConfig) ResolvedServers() []*disc.Entry {
	seen := make(map[cipher.PubKey]struct{})
	var out []*disc.Entry
	add := func(e *disc.Entry) {
		if e == nil {
			return
		}
		if _, ok := seen[e.Static]; ok {
			return
		}
		seen[e.Static] = struct{}{}
		out = append(out, e)
	}
	for _, d := range c.AllDeployments() {
		for _, e := range d.Servers {
			add(e)
		}
	}
	return out
}

// PKFromDmsgURL extracts the dmsg PK from a URL of the form
// `dmsg://<PK>:<port>[/path]`. Returns the zero PK when the URL is
// empty, malformed, or doesn't contain a valid PK in the host part.
//
// Exported (vs the pre-split lowercase pkFromDmsgURL) because the
// spec package is the natural home for parse helpers that depend
// only on stdlib + cipher. Internal callers in pkg/dmsg/dmsgc now use
// spec.PKFromDmsgURL.
func PKFromDmsgURL(s string) cipher.PubKey {
	if s == "" {
		return cipher.PubKey{}
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return cipher.PubKey{}
	}
	host := u.Hostname()
	if host == "" {
		host = strings.SplitN(u.Host, ":", 2)[0]
	}
	var pk cipher.PubKey
	if err := pk.Set(host); err != nil {
		return cipher.PubKey{}
	}
	return pk
}
