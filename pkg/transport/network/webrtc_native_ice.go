//go:build !(js && wasm) && (!tinygo || linux)

package network

import (
	"net"
	"sort"
	"strings"
	"sync"

	"github.com/pion/ice/v4"

	"github.com/skycoin/skywire/pkg/netutil"
)

// keepICEInterface and keepICEIP apply the virtual interface filter and the
// advertise policy, so ICE offers what the address resolver is told.
func keepICEInterface(name string) bool {
	pol := netutil.AdvertisePolicy()
	if netutil.IsVirtualInterface(name) && !pol.AllowsNamed(name) {
		return false
	}
	return !pol.DeniesInterface(name)
}

func keepICEIP(ip net.IP) bool { return netutil.IsAdvertisableIP(ip) && netutil.PolicyPermitsIP(ip) }

// iceLocalIPs lists the addresses a shared mux binds, as a stable key.
func iceLocalIPs() string {
	ips, err := netutil.LocalAddresses()
	if err != nil {
		return ""
	}
	sort.Strings(ips)
	return strings.Join(ips, ",")
}

// sharedUDPMux lets every PeerConnection of one role share a socket per local
// address. Once replaced after an address change it closes with its last user.
type sharedUDPMux struct {
	ice.UDPMux

	mu     sync.Mutex
	ufrags map[string]struct{}
	stale  bool
}

func (m *sharedUDPMux) GetConn(ufrag string, addr net.Addr) (net.PacketConn, error) {
	c, err := m.UDPMux.GetConn(ufrag, addr)
	if err == nil {
		m.mu.Lock()
		m.ufrags[ufrag] = struct{}{}
		m.mu.Unlock()
	}
	return c, err
}

func (m *sharedUDPMux) RemoveConnByUfrag(ufrag string) {
	m.UDPMux.RemoveConnByUfrag(ufrag)
	m.mu.Lock()
	delete(m.ufrags, ufrag)
	closeNow := m.stale && len(m.ufrags) == 0
	m.mu.Unlock()
	if closeNow {
		m.UDPMux.Close() //nolint:errcheck,gosec
	}
}

func (m *sharedUDPMux) retire() {
	m.mu.Lock()
	m.stale = true
	m.mu.Unlock()
}

// iceMuxPool hands out the current shared mux, rebuilding it when the set of
// local addresses changes so host candidates never name a stale address.
type iceMuxPool struct {
	mu  sync.Mutex
	key string
	cur *sharedUDPMux
}

// Dialed and accepted connections use separate muxes. A mux routes by remote
// address, so a dial and an accept between the same two visors must not share.
var (
	iceDialMux   iceMuxPool
	iceAcceptMux iceMuxPool
)

// get returns nil when no mux can be bound; pion then opens its own sockets.
func (p *iceMuxPool) get() ice.UDPMux {
	key := iceLocalIPs()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cur != nil && p.key == key {
		return p.cur
	}
	mux, err := ice.NewMultiUDPMuxFromPort(0,
		ice.UDPMuxFromPortWithInterfaceFilter(keepICEInterface),
		ice.UDPMuxFromPortWithIPFilter(keepICEIP),
	)
	if err != nil {
		wrtcLog.WithError(err).Warn("webrtc: shared ICE UDP mux unavailable")
		return nil
	}
	if p.cur != nil {
		p.cur.retire()
	}
	p.key = key
	p.cur = &sharedUDPMux{UDPMux: mux, ufrags: map[string]struct{}{}}
	return p.cur
}
