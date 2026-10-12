// Package dmsg pkg/dmsg/dmsg/traffic.go c2-dmsg-core
package dmsg

import (
	"sort"
	"sync"
	"sync/atomic"
)

// portTraffic counts the streams accepted on one listening port and the bytes
// they carried.
type portTraffic struct {
	streams atomic.Uint64
	in      atomic.Uint64
	out     atomic.Uint64
}

// trafficRegistry holds a client's per-port counters. Counting is off until a
// service turns it on, so a visor pays one atomic load per accepted stream.
type trafficRegistry struct {
	on    atomic.Bool
	mu    sync.Mutex
	ports map[uint16]*portTraffic
}

func (t *trafficRegistry) port(p uint16) *portTraffic {
	if t == nil || !t.on.Load() {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ports == nil {
		t.ports = map[uint16]*portTraffic{}
	}
	pt := t.ports[p]
	if pt == nil {
		pt = &portTraffic{}
		t.ports[p] = pt
	}
	return pt
}

// PortTraffic is what the streams accepted on one port carried since counting
// started: In is bytes read from peers, Out bytes written to them.
type PortTraffic struct {
	Port    uint16
	Streams uint64
	In      uint64
	Out     uint64
}

// CountTraffic starts counting the streams this client accepts on each of its
// listening ports, and the bytes they carry. For services, which serve every
// API and feed on a port of their own.
func (ce *Client) CountTraffic() { ce.traffic.on.Store(true) }

// Traffic returns the per-port totals since CountTraffic, by port.
func (ce *Client) Traffic() []PortTraffic {
	ce.traffic.mu.Lock()
	out := make([]PortTraffic, 0, len(ce.traffic.ports))
	for p, pt := range ce.traffic.ports {
		out = append(out, PortTraffic{Port: p, Streams: pt.streams.Load(), In: pt.in.Load(), Out: pt.out.Load()})
	}
	ce.traffic.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out
}
