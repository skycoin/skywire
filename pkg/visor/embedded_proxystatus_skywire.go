// Package visor pkg/visor/embedded_proxystatus_skywire.go c3-vis-core
//
// The skywire status surface: every app and visor subsystem that holds a route
// group or a direct stream, each drawn with the same tunnel model as the proxy
// surfaces.
package visor

import (
	"fmt"
	"sort"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/proxystatus"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// tunnelFrom converts one route group into a status Tunnel.
func (p *visorStatusProvider) tunnelFrom(index int, info visorapi.MuxRouteGroupInfo, self cipher.PubKey) proxystatus.Tunnel {
	// The exit is the descriptor end that is NOT this visor, which the
	// router now names outright: a client route group's descriptor carries
	// the local visor as Dst and the exit as Src, so hardcoding DstPK
	// mislabeled the local visor as the exit.
	// The LOCAL port is the far end's mirror image: a route group's
	// LocalAddr is desc.Dst(), so the port on whichever descriptor end
	// is this visor is the one the dialing app knows its tunnel by.
	exit, localPort, remotePort := info.FarEndPK, info.Desc.DstPort, info.Desc.SrcPort
	if exit == info.Desc.DstPK {
		localPort, remotePort = info.Desc.SrcPort, info.Desc.DstPort
	}
	if exit.Null() {
		exit, localPort, remotePort = info.Desc.DstPK, info.Desc.SrcPort, info.Desc.DstPort
		if exit == self {
			exit, localPort, remotePort = info.Desc.SrcPK, info.Desc.DstPort, info.Desc.SrcPort
		}
	}
	t := proxystatus.Tunnel{
		Index:      index,
		ExitPK:     exit.String(),
		MuxEnabled: info.MuxEnabled,
		// "active" / "standby", as the dialing app labeled it. Empty
		// unless this visor is the one holding the tunnels.
		Role:       info.TunnelRole,
		LegReserve: info.LegReserve,
		AuditionMS: info.AgeMS,
		LocalPort:  uint16(localPort),
		RemotePort: uint16(remotePort),
	}
	for _, leg := range info.Legs {
		t.Legs = append(t.Legs, proxyLegFrom(leg, p.hopThroughputBps))
	}
	return t
}

// directTunnel models one direct (0-hop) stream as a one-leg tunnel. A
// shortcut-eligible dial builds no route group, so without this a working
// session had nothing to draw.
func (p *visorStatusProvider) directTunnel(index int, s transport.VStreamInfo, self cipher.PubKey) proxystatus.Tunnel {
	tpType, rtt := p.directTpDetail(s.TpID)
	return proxystatus.Tunnel{
		Index:  index,
		ExitPK: s.RemotePK.String(),
		Legs: []proxystatus.Leg{{
			Index:       index,
			TransportID: s.TpID.String(),
			TpType:      tpType,
			RemotePK:    s.RemotePK.String(),
			LatencyMS:   rtt,
			// The direct path IS one hop, so describe it as one
			// rather than as a leg with no path. hopToNode then
			// renders the type, transport id and RTT exactly as
			// it does for a routed hop — without it the tree drew
			// a bare public key and nothing else.
			Hops: []proxystatus.Hop{{
				From:          self.String(),
				To:            s.RemotePK.String(),
				TpID:          s.TpID.String(),
				TpType:        tpType,
				LatencyMS:     rtt,
				ThroughputBps: p.hopThroughputBps(s.TpID.String()),
			}},
			Direct: true,
			// Alive is what the tree renderer gates on: it skips
			// every leg that is not alive, so leaving this false
			// drew a tree with the local PK and nothing under it
			// — no exit — on a proxy that was carrying traffic.
			// A stream in the mux's live map IS alive; StreamInfo
			// lists no other kind.
			Alive:     true,
			SentBytes: s.SentBytes,
			RecvBytes: s.RecvBytes,
		}},
	}
}

// skywireSnapshot groups every route group and direct stream on the visor by
// the app that dialed it. A route group with no app name is one this visor
// accepted; it is named after the local app listening on its port.
func (p *visorStatusProvider) skywireSnapshot() proxystatus.Snapshot {
	snap := proxystatus.Snapshot{Surface: proxystatus.SurfaceSkywire, App: "visor", Running: true}
	var self cipher.PubKey
	if p.v.conf != nil && p.v.conf.Common != nil {
		self = p.v.conf.PK
		snap.SelfPK = self.String()
	}

	byName := map[string]*proxystatus.Consumer{}
	consumer := func(name string, inbound bool) *proxystatus.Consumer {
		c, ok := byName[name]
		if !ok {
			c = &proxystatus.Consumer{Name: name, Inbound: inbound}
			byName[name] = c
		}
		return c
	}

	if infos, err := p.v.AllRouteGroupMuxInfo(); err == nil {
		for _, info := range infos {
			t := p.tunnelFrom(0, info, self)
			name, inbound := info.AppName, false
			if name == "" {
				name, inbound = p.inboundName(t.LocalPort), true
			}
			c := consumer(name, inbound)
			t.Index = len(c.Tunnels)
			c.Tunnels = append(c.Tunnels, t)
		}
	} else {
		snap.Note = appendNote(snap.Note, "route groups unavailable: "+err.Error())
	}
	if direct, err := p.v.AppDirectStreams(""); err == nil {
		for _, s := range direct {
			name := s.AppName
			if name == "" {
				// The visor's own streams (skymail, dmsg over skynet) carry no app name.
				name = "visor"
			}
			c := consumer(name, false)
			c.Tunnels = append(c.Tunnels, p.directTunnel(len(c.Tunnels), s, self))
		}
	}

	for _, c := range byName {
		if !c.Inbound && p.v.procM != nil {
			proc, ok := p.v.procM.ProcByName(c.Name)
			c.Running = ok && proc != nil
		}
		snap.Consumers = append(snap.Consumers, *c)
	}
	// Dialed first, then accepted; by name within each.
	sort.Slice(snap.Consumers, func(i, j int) bool {
		a, b := snap.Consumers[i], snap.Consumers[j]
		if a.Inbound != b.Inbound {
			return !a.Inbound
		}
		return a.Name < b.Name
	})
	if len(snap.Consumers) == 0 {
		snap.Note = appendNote(snap.Note, "nothing on this visor holds a route or a direct stream right now")
	}
	return snap
}

// inboundName names an accepted route group after the launcher app that
// listens on its local port, or by the port alone.
func (p *visorStatusProvider) inboundName(port uint16) string {
	if p.v.conf != nil && p.v.conf.Launcher != nil {
		for _, app := range p.v.conf.Launcher.Apps {
			if uint16(app.Port) == port {
				return app.Name + " (inbound)"
			}
		}
	}
	return fmt.Sprintf("inbound :%d", port)
}
