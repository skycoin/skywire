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

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/transport"
)

// DiagSnapshot is the `diag` section of a StateSnapshot.
type DiagSnapshot struct {
	Runtime DiagRuntime `json:"runtime"`
	// TransportReadQueue is the shared queue every transport read loop feeds
	// and the router drains; at capacity, every transport stalls behind the
	// router (pings and pongs included).
	TransportReadQueue *DiagQueue `json:"transport_read_queue,omitempty"`
	// Intake is the router's inbound-path view: unknown/control packets by
	// type, stale-route drops, and each route group's app queue depth.
	Intake *router.IntakeStats `json:"intake,omitempty"`
	// VStream is one entry per virtual-stream mux (skynet forwarding,
	// app-direct dials, visor RPC): open streams, relay legs, and frames that
	// arrived for streams this side does not have.
	VStream []DiagVStreamMux `json:"vstream,omitempty"`
	// Dmsg is per-session ping health plus the relay nominee/backoff state.
	Dmsg *DiagDmsg `json:"dmsg,omitempty"`
	// Transports is per-transport liveness: missed pongs, last packet age,
	// and which route-ID-0 handlers are wired (a missing one means that
	// packet type is dropped by the router — #4725).
	Transports []DiagTransport `json:"transports,omitempty"`
}

// DiagRuntime is the Go runtime at a glance.
type DiagRuntime struct {
	Goroutines  int     `json:"goroutines"`
	HeapAllocMB float64 `json:"heap_alloc_mb"`
	SysMB       float64 `json:"sys_mb"`
	NumGC       uint32  `json:"num_gc"`
	GoVersion   string  `json:"go_version"`
}

// DiagQueue is a bounded queue's depth and capacity.
type DiagQueue struct {
	Depth    int `json:"depth"`
	Capacity int `json:"capacity"`
}

// DiagVStreamMux names one VStream mux and carries its counters.
type DiagVStreamMux struct {
	Name string `json:"name"`
	transport.VStreamMuxStats
}

// DiagDmsg is the dmsg client's session and relay state.
type DiagDmsg struct {
	Sessions      []DiagDmsgSession `json:"sessions,omitempty"`
	RelayNominees []cipher.PubKey   `json:"relay_nominees,omitempty"`
	RelayingFor   []DiagRelayClient `json:"relaying_for,omitempty"`
	RelayBackoff  []DiagRelayTimer  `json:"relay_backoff,omitempty"`
	RelayDialSkip []DiagRelayTimer  `json:"relay_dial_skip,omitempty"`
}

// DiagDmsgSession is one dmsg session's liveness.
type DiagDmsgSession struct {
	PK        cipher.PubKey `json:"pk"`
	Carrier   string        `json:"carrier"`
	Protocol  string        `json:"protocol"`
	Streams   int           `json:"streams"`
	LatencyMS float64       `json:"latency_ms"`
	// PingFails is the consecutive liveness-ping failures; 2 closes the session.
	PingFails int `json:"ping_fails"`
}

// DiagRelayClient is a peer attached to this visor as its dmsg relay.
type DiagRelayClient struct {
	PK      cipher.PubKey `json:"pk"`
	Streams int           `json:"streams"`
}

// DiagRelayTimer is a relay-side backoff or skip with its remaining time.
type DiagRelayTimer struct {
	PK         cipher.PubKey `json:"pk"`
	RemainingS float64       `json:"remaining_s"`
}

// DiagTransport is one transport's liveness and wiring.
type DiagTransport struct {
	ID          uuid.UUID     `json:"id"`
	Remote      cipher.PubKey `json:"remote"`
	Type        string        `json:"type"`
	MissedPongs int64         `json:"missed_pongs"`
	PongSeen    bool          `json:"pong_seen"`
	// LastRecvAgoS is seconds since the read loop last received any packet
	// (-1 = never). A transport with pongs flowing but climbing app-level
	// timeouts is the router-intake case, not a link problem.
	LastRecvAgoS float64 `json:"last_recv_ago_s"`
	// MalformedFrames counts frames whose type byte was outside the known
	// range — the peer wrote a packet whose size field did not match it.
	MalformedFrames int64    `json:"malformed_frames"`
	Handlers        []string `json:"handlers,omitempty"`
}

// DiagSnapshot builds the diag section. Every part is best-effort on a
// partially initialized visor: a missing subsystem leaves its field nil.
func (v *Visor) DiagSnapshot() *DiagSnapshot {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	d := &DiagSnapshot{Runtime: DiagRuntime{
		Goroutines:  runtime.NumGoroutine(),
		HeapAllocMB: float64(m.HeapAlloc) / (1 << 20),
		SysMB:       float64(m.Sys) / (1 << 20),
		NumGC:       m.NumGC,
		GoVersion:   runtime.Version(),
	}}

	if v.tpM != nil {
		depth, capacity := v.tpM.ReadQueue()
		d.TransportReadQueue = &DiagQueue{Depth: depth, Capacity: capacity}
		now := time.Now()
		v.tpM.WalkTransports(func(mt *transport.ManagedTransport) bool {
			if mt.IsClosed() {
				return true
			}
			ago := -1.0
			if at := mt.LastRecvAt(); !at.IsZero() {
				ago = now.Sub(at).Seconds()
			}
			d.Transports = append(d.Transports, DiagTransport{
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

	if is, ok := v.router.(interface{ IntakeStats() router.IntakeStats }); ok {
		stats := is.IntakeStats()
		d.Intake = &stats
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
		d.VStream = append(d.VStream, DiagVStreamMux{Name: mux.name, VStreamMuxStats: mux.m.Stats()})
	}

	if v.dmsgC != nil {
		dd := &DiagDmsg{}
		for _, s := range v.dmsgC.AllSessions() {
			dd.Sessions = append(dd.Sessions, DiagDmsgSession{
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
			dd.RelayingFor = append(dd.RelayingFor, DiagRelayClient{PK: pk, Streams: n})
		}
		sort.Slice(dd.RelayingFor, func(i, j int) bool { return dd.RelayingFor[i].PK.String() < dd.RelayingFor[j].PK.String() })
		dd.RelayBackoff = relayTimers(v.dmsgC.RelayBackoffs())
		dd.RelayDialSkip = relayTimers(v.dmsgC.RelayDialSkips())
		d.Dmsg = dd
	}
	return d
}

func relayTimers(m map[cipher.PubKey]time.Time) []DiagRelayTimer {
	if len(m) == 0 {
		return nil
	}
	now := time.Now()
	out := make([]DiagRelayTimer, 0, len(m))
	for pk, until := range m {
		out = append(out, DiagRelayTimer{PK: pk, RemainingS: until.Sub(now).Seconds()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PK.String() < out[j].PK.String() })
	return out
}
