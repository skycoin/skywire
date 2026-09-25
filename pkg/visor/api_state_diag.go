// Package visor pkg/visor/api_state_diag.go c3-vis-api
//
// The `diag` section of `visor state`: the in-process plumbing — router
// intake, transport read queue and per-transport handlers/pong health, VStream
// muxes, dmsg session ping health and relay state, Go runtime — that until now
// could only be inferred from logs. Every value is a cheap read of live
// counters; the section is built for the default snapshot.
package visor

import (
	"runtime"
	"sort"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// DiagSnapshot builds the diag section. Every part is best-effort on a
// partially initialized visor: a missing subsystem leaves its field nil.
func (v *Visor) DiagSnapshot() *visorapi.DiagSnapshot {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	d := &visorapi.DiagSnapshot{Runtime: visorapi.DiagRuntime{
		Goroutines:  runtime.NumGoroutine(),
		HeapAllocMB: float64(m.HeapAlloc) / (1 << 20),
		SysMB:       float64(m.Sys) / (1 << 20),
		NumGC:       m.NumGC,
		GoVersion:   runtime.Version(),
	}}

	if n, last := logging.PanicStats(); n > 0 {
		d.Panics = &visorapi.DiagPanics{Count: n, Last: last}
	}

	if v.tpM != nil {
		depth, capacity := v.tpM.ReadQueue()
		d.TransportReadQueue = &visorapi.DiagQueue{Depth: depth, Capacity: capacity}
		d.TransportEvents = v.tpM.Events()
		d.TransportLastClose = v.tpM.LastCloses()
		now := time.Now()
		v.tpM.WalkTransports(func(mt *transport.ManagedTransport) bool {
			if mt.IsClosed() {
				return true
			}
			ago := -1.0
			if at := mt.LastRecvAt(); !at.IsZero() {
				ago = now.Sub(at).Seconds()
			}
			d.Transports = append(d.Transports, visorapi.DiagTransport{
				ID:              mt.Entry.ID,
				Remote:          mt.Remote(),
				Type:            string(mt.Type()),
				MissedPongs:     mt.MissedPongs(),
				PongSeen:        mt.PongSeen(),
				LastRecvAgoS:    ago,
				MalformedFrames: mt.MalformedFrames(),
				Handlers:        mt.Handlers(),
			})
			return true
		})
		sort.Slice(d.Transports, func(i, j int) bool { return d.Transports[i].ID.String() < d.Transports[j].ID.String() })
	}

	if rs, ok := v.router.(interface {
		RouteSourceStats() router.RouteSourceStats
	}); ok {
		stats := rs.RouteSourceStats()
		d.RouteSource = &stats
	}

	if is, ok := v.router.(interface{ IntakeStats() router.IntakeStats }); ok {
		stats := is.IntakeStats()
		d.Intake = &stats
	}

	if ss, ok := v.router.(interface {
		SetupPathStats() router.SetupPathStats
	}); ok {
		stats := ss.SetupPathStats()
		d.RouteSetup = &stats
	}

	if v.router != nil {
		d.MuxEvents = v.router.MuxEvents()
	}

	for _, mux := range []struct {
		name string
		m    *transport.VStreamMux
	}{
		{"skynet_forward", v.skynetFwdMux},
		{"app_direct", v.appDirectMux},
		{"visor_rpc", v.transportRPCMux},
	} {
		if mux.m == nil {
			continue
		}
		d.VStream = append(d.VStream, visorapi.DiagVStreamMux{
			Name:            mux.name,
			VStreamMuxStats: mux.m.Stats(),
			StreamList:      mux.m.StreamInfo(""),
		})
	}

	if v.dmsgC != nil {
		dd := &visorapi.DiagDmsg{Unpublished: v.dmsgC.Unpublished()}
		for _, s := range v.dmsgC.AllSessions() {
			dd.Sessions = append(dd.Sessions, visorapi.DiagDmsgSession{
				PK:        s.RemotePK(),
				Carrier:   s.Carrier(),
				Protocol:  s.Protocol(),
				Streams:   s.NumStreams(),
				LatencyMS: float64(s.LastPing()) / 1e6,
				PingFails: s.PingFails(),
			})
		}
		sort.Slice(dd.Sessions, func(i, j int) bool { return dd.Sessions[i].PK.String() < dd.Sessions[j].PK.String() })
		dd.RelayNominees = sortedPKs(v.dmsgC.RelayPeers())
		for pk, n := range v.dmsgC.RelaySessionStreams() {
			dd.RelayingFor = append(dd.RelayingFor, visorapi.DiagRelayClient{PK: pk, Streams: n})
		}
		sort.Slice(dd.RelayingFor, func(i, j int) bool { return dd.RelayingFor[i].PK.String() < dd.RelayingFor[j].PK.String() })
		dd.RelayBackoff = relayTimers(v.dmsgC.RelayBackoffs())
		dd.RelayDialSkip = relayTimers(v.dmsgC.RelayDialSkips())
		d.Dmsg = dd
	}
	return d
}

func relayTimers(m map[cipher.PubKey]time.Time) []visorapi.DiagRelayTimer {
	if len(m) == 0 {
		return nil
	}
	now := time.Now()
	out := make([]visorapi.DiagRelayTimer, 0, len(m))
	for pk, until := range m {
		out = append(out, visorapi.DiagRelayTimer{PK: pk, RemainingS: until.Sub(now).Seconds()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PK.String() < out[j].PK.String() })
	return out
}
