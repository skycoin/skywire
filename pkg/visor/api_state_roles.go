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

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

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
func (v *Visor) RolesSnapshot() *visorapi.RolesSnapshot {
	r := &visorapi.RolesSnapshot{DmsgServer: dmsgServerRole(v.conf, v.dmsgSrvRole.Load())}
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
func dmsgServerRole(conf *visorconfig.V1, live *visorapi.DmsgServerRole) visorapi.DmsgServerRole {
	var role visorapi.DmsgServerRole
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
func dmsgRelayRole(nominees []cipher.PubKey, sessions []dmsgSessionView,
	clientStreams map[cipher.PubKey]int, relayed, maxStreams, refused int) visorapi.DmsgRelayRole {

	role := visorapi.DmsgRelayRole{
		Port:              skyenv.DmsgRelayPort,
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
		role.Attached = append(role.Attached, visorapi.DmsgRelayAttachment(s))
	}
	sort.Slice(role.Attached, func(i, j int) bool { return role.Attached[i].PK.String() < role.Attached[j].PK.String() })

	for pk, n := range clientStreams {
		role.RelayClients = append(role.RelayClients, visorapi.DmsgRelayClient{PK: pk, Streams: n})
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
func dmsgServerSessions(srv *dmsg.Server) (total, peers int, clients []visorapi.DmsgServerClient) {
	sessions := srv.GetSessions()
	clients = make([]visorapi.DmsgServerClient, 0, len(sessions))
	for pk, ses := range sessions {
		if ses == nil {
			continue
		}
		isPeer := srv.IsPeerPK(pk)
		if isPeer {
			peers++
		}
		clients = append(clients, visorapi.DmsgServerClient{
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
