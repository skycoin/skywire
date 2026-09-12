// Package visor pkg/visor/api_state_roles.go c3-vis-api
//
// The `roles` section of `visor state`: what this visor is FOR the rest of the
// network, as opposed to what it is doing for itself. Three answers an operator
// used to have to assemble from `mdisc entry`, `ss` on the host and the logs:
//
//   - dmsg SERVER — is one running in-process, under which key, on which
//     address, and is it sharing the visor's transport TCP port (#4785/#4787)
//     or bound to one of its own. An own-key server registers in the SAME
//     discovery entry as the visor's client; a config_path server is the
//     standalone dmsg-server service folded into this process on its OWN key.
//   - dmsg RELAY, both directions (#4789/#4790/#4791) — the peers this visor
//     nominated as its relays and which of them it is actually attached to
//     (with the carrier), plus the peers attached TO this visor and the
//     streams it is carrying for them against the configured slot cap.
//   - TRANSIT — whether this visor refuses to be an intermediate route hop
//     (routing.no_transit, #4788).
//
// Everything here is a cheap read of live counters and config, so the section
// is part of the default snapshot and is selectable on its own.
package visor

import (
	"sort"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// RolesSnapshot is the `roles` section of a StateSnapshot.
type RolesSnapshot struct {
	// PK is the visor's own public key — the key the own-key dmsg server and
	// the relay acceptor both answer under.
	PK cipher.PubKey `json:"pk"`
	// NoTransit reports routing.no_transit: this visor refuses to be an
	// INTERMEDIATE hop on someone else's route (its own edges still work).
	NoTransit bool `json:"no_transit"`

	DmsgServer DmsgServerRole `json:"dmsg_server"`
	DmsgRelay  DmsgRelayRole  `json:"dmsg_relay"`
}

// DmsgServerRole is the in-process dmsg server (dmsg.server.* in the config).
type DmsgServerRole struct {
	// Enabled is what the config asks for; Running is what actually started.
	// Enabled && !Running is the interesting case — e.g. no dmsg discovery PK
	// to register with, so the server no-opped at init.
	Enabled bool `json:"enabled"`
	Running bool `json:"running"`
	// Mode distinguishes the two servers a visor can host: "own_key" is the
	// bare server sharing the visor's PK/SK and dmsg client, "config_path" is
	// the whole standalone dmsg-server service run in-process from its own
	// config file, on its OWN key. Empty when no server is configured.
	Mode string `json:"mode,omitempty"`
	// PK is the key the server registers under: the visor's own key in
	// "own_key" mode, the config file's key in "config_path" mode.
	PK cipher.PubKey `json:"pk,omitempty"`
	// OwnKey is true when the server shares the visor's identity, i.e. one
	// discovery entry carries both the Client and the Server section.
	OwnKey bool `json:"own_key"`
	// SharedTransportPort: the server took the dmsg branch of the transport
	// port's TCP multiplexer instead of opening a listener of its own, so no
	// second port is bound or forwarded.
	SharedTransportPort bool   `json:"shared_transport_port"`
	LocalAddress        string `json:"local_address,omitempty"`
	PublicAddress       string `json:"public_address,omitempty"`
	ConfigPath          string `json:"config_path,omitempty"`
	// StartedAt is when the server was started (zero when it is not running).
	StartedAt time.Time `json:"started_at,omitempty"`
	// SessionCount is how many dmsg sessions this server currently holds, and
	// Clients names them. Answering "who is actually on this dmsg server" used
	// to require asking the discovery — which reports what CLIENTS claim about
	// their delegated servers, not what the server itself is holding, and says
	// nothing about how much traffic each one is running. A co-resident visor
	// knows the real answer; this reports it.
	//
	// Peers are separated from ordinary clients because a server-to-server link
	// is not a client at all: counting the two together makes a server look
	// busier than it is, and the peer mesh is the part an operator tunes
	// separately.
	//
	// Only populated in own_key mode — config_path mode runs the server inside
	// an unexported service type that exposes no session handle.
	SessionCount int                `json:"session_count"`
	PeerCount    int                `json:"peer_count"`
	Clients      []DmsgServerClient `json:"clients,omitempty"`
}

// DmsgServerClient is one session held by this visor's in-process dmsg server:
// the connected key, how many streams it has open, and whether the session is
// another dmsg server (a peer link) rather than a client.
type DmsgServerClient struct {
	PK      cipher.PubKey `json:"pk"`
	Streams int           `json:"streams"`
	Peer    bool          `json:"peer,omitempty"`
}

// DmsgRelayRole is both directions of the dmsg relay: the relays this visor
// attaches to, and the peers it relays for.
type DmsgRelayRole struct {
	// Port is the dmsg port the relay acceptor listens on over skynet.
	Port uint16 `json:"relay_port"`
	// RelayPeers are the peers this visor NOMINATED as its relays
	// (relayNominees → dmsg.Client.SetRelayPeers). Same field name and
	// meaning as `dmsg sessions`.
	RelayPeers []cipher.PubKey `json:"relay_peers,omitempty"`
	// Attached is the subset of RelayPeers this visor currently holds a
	// session with, and the carrier that session rides. Carrier "skynet" is
	// an actual relay attachment; anything else means the nominee was reached
	// as a plain dmsg server instead.
	Attached []DmsgRelayAttachment `json:"attached,omitempty"`
	// RelayClients are the peers attached TO this visor's relay acceptor,
	// each with the streams open on its relay session. Same field name and
	// meaning as `dmsg sessions`, which reports the keys alone.
	RelayClients []DmsgRelayClient `json:"relay_clients,omitempty"`
	// RelayedStreams is the relay slots in use across all attached peers, and
	// MaxRelayedStreams the cap they are charged against (dmsg.relay_max_streams).
	// A cap of 0 or less means this visor relays for nobody.
	RelayedStreams    int `json:"relayed_streams"`
	MaxRelayedStreams int `json:"max_relayed_streams"`
	// RelayRefused counts stream requests turned away at capacity (dmsg error
	// 308) since start. Nonzero means this hub refused traffic it was asked to
	// carry — at the dialer that refusal is indistinguishable from the
	// destination being down, so without this it is only ever diagnosed from
	// the wrong end.
	RelayRefused int `json:"relay_refused"`
}

// DmsgRelayAttachment is one nominated relay this visor holds a session with.
type DmsgRelayAttachment struct {
	PK        cipher.PubKey `json:"pk"`
	Carrier   string        `json:"carrier"`
	Streams   int           `json:"streams"`
	LatencyMS float64       `json:"latency_ms"`
}

// DmsgRelayClient is a peer attached to this visor as its dmsg relay.
type DmsgRelayClient struct {
	PK      cipher.PubKey `json:"pk"`
	Streams int           `json:"streams"`
}

// dmsgSessionView is the handful of fields the roles section needs from a live
// dmsg session, extracted so the assembly below is a pure function.
type dmsgSessionView struct {
	PK        cipher.PubKey
	Carrier   string
	Streams   int
	LatencyMS float64
}

// RolesSnapshot builds the roles section. Cheap and best-effort: a
// not-yet-initialized dmsg client or config leaves the corresponding role
// zero-valued rather than failing.
func (v *Visor) RolesSnapshot() *RolesSnapshot {
	r := &RolesSnapshot{DmsgServer: dmsgServerRole(v.conf, v.dmsgSrvRole.Load())}
	if srv := v.dmsgSrv.Load(); srv != nil {
		r.DmsgServer.SessionCount, r.DmsgServer.PeerCount, r.DmsgServer.Clients = dmsgServerSessions(srv)
	}
	if v.conf != nil {
		r.PK = confPK(v.conf)
		if v.conf.Routing != nil {
			r.NoTransit = v.conf.Routing.NoTransit
		}
	}

	if v.dmsgC == nil {
		return r
	}
	sessions := make([]dmsgSessionView, 0, 8)
	for _, s := range v.dmsgC.AllSessions() {
		sessions = append(sessions, dmsgSessionView{
			PK:        s.RemotePK(),
			Carrier:   s.Carrier(),
			Streams:   s.NumStreams(),
			LatencyMS: float64(s.LastPing()) / 1e6,
		})
	}
	r.DmsgRelay = dmsgRelayRole(
		skyenv.DmsgRelayPort,
		v.dmsgC.RelayPeers(),
		sessions,
		v.dmsgC.RelaySessionStreams(),
		v.dmsgC.RelayedStreams(),
		v.dmsgC.MaxRelayedStreams(),
		v.dmsgC.RelayRefused(),
	)
	return r
}

// dmsgServerRole reports the in-process dmsg server: what the config asks for,
// overlaid with what the running server recorded at init (live == nil means no
// server started).
func dmsgServerRole(conf *visorconfig.V1, live *DmsgServerRole) DmsgServerRole {
	var role DmsgServerRole
	if conf != nil && conf.Dmsg != nil && conf.Dmsg.Server != nil {
		s := conf.Dmsg.Server
		role.Enabled = s.Enabled
		role.ConfigPath = s.ConfigPath
		if s.Enabled {
			if s.ConfigPath != "" {
				role.Mode = dmsgServerModeConfigPath
			} else {
				role.Mode = dmsgServerModeOwnKey
				role.OwnKey = true
				role.PK = confPK(conf)
				role.LocalAddress = s.LocalAddress
				role.PublicAddress = s.PublicAddress
			}
		}
	}
	if live == nil {
		return role
	}
	// The running server is authoritative for everything it knows: the address
	// it actually listens on (the shared branch has no configured address), the
	// key a config_path server loaded from its file, and the start time.
	out := *live
	out.Enabled = true
	out.Running = true
	if out.ConfigPath == "" {
		out.ConfigPath = role.ConfigPath
	}
	return out
}

// confPK is the visor's public key, safe on a half-built config.
func confPK(conf *visorconfig.V1) cipher.PubKey {
	if conf == nil || conf.Common == nil {
		return cipher.PubKey{}
	}
	return conf.PK
}

// dmsgServer modes; see DmsgServerRole.Mode.
const (
	dmsgServerModeOwnKey     = "own_key"
	dmsgServerModeConfigPath = "config_path"
)

// dmsgRelayRole assembles the relay role from the dmsg client's live relay
// state. nominees are this visor's relay nominees, sessions every session the
// client holds (to pair a nominee with the carrier it was reached over),
// clientStreams the per-peer stream count of the peers attached to this visor,
// and relayed/max the slot usage against the configured cap.
func dmsgRelayRole(port uint16, nominees []cipher.PubKey, sessions []dmsgSessionView,
	clientStreams map[cipher.PubKey]int, relayed, maxStreams, refused int) DmsgRelayRole {

	role := DmsgRelayRole{
		Port:              port,
		RelayPeers:        sortedPKs(nominees),
		RelayedStreams:    relayed,
		MaxRelayedStreams: maxStreams,
		RelayRefused:      refused,
	}

	nominated := make(map[cipher.PubKey]struct{}, len(nominees))
	for _, pk := range nominees {
		nominated[pk] = struct{}{}
	}
	for _, s := range sessions {
		if _, ok := nominated[s.PK]; !ok {
			continue
		}
		role.Attached = append(role.Attached, DmsgRelayAttachment{
			PK:        s.PK,
			Carrier:   s.Carrier,
			Streams:   s.Streams,
			LatencyMS: s.LatencyMS,
		})
	}
	sort.Slice(role.Attached, func(i, j int) bool { return role.Attached[i].PK.String() < role.Attached[j].PK.String() })

	for pk, n := range clientStreams {
		role.RelayClients = append(role.RelayClients, DmsgRelayClient{PK: pk, Streams: n})
	}
	sort.Slice(role.RelayClients, func(i, j int) bool {
		return role.RelayClients[i].PK.String() < role.RelayClients[j].PK.String()
	})
	return role
}

// dmsgServerSessions snapshots what the in-process dmsg server is holding:
// the total session count, how many of those are peer servers rather than
// clients, and a stable-ordered list naming each one with its open streams.
//
// This is the server's own view. The discovery's is not a substitute: an entry
// records what a CLIENT claims about the servers it delegated to, so it is
// self-reported, lags reality by the entry refresh interval, and carries no
// per-client stream count at all. For "which clients is this server actually
// carrying, and how hard", only the server knows.
func dmsgServerSessions(srv *dmsg.Server) (total, peers int, clients []DmsgServerClient) {
	sessions := srv.GetSessions()
	clients = make([]DmsgServerClient, 0, len(sessions))
	for pk, ses := range sessions {
		if ses == nil {
			continue
		}
		isPeer := srv.IsPeerPK(pk)
		if isPeer {
			peers++
		}
		clients = append(clients, DmsgServerClient{
			PK:      pk,
			Streams: ses.NumStreams(),
			Peer:    isPeer,
		})
	}
	// Stable order: a caller diffing two snapshots must not see map iteration
	// masquerading as churn.
	sort.Slice(clients, func(i, j int) bool { return clients[i].PK.String() < clients[j].PK.String() })
	return len(clients), peers, clients
}
