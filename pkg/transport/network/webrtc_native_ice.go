//go:build !(js && wasm) && (!tinygo || linux)

package network

import (
	"net"
	"sort"
	"strings"
	"sync"

	"github.com/pion/ice/v4"
)

// iceSkipPrefixes names interfaces that never carry a path to a peer:
// container bridges, veth pairs and the visor's own tunnels.
var iceSkipPrefixes = []string{"docker", "br-", "veth", "skydns", "tun"}

func keepICEInterface(name string) bool {
	for _, p := range iceSkipPrefixes {
		if strings.HasPrefix(name, p) {
			return false
		}
	}
	return true
}

func keepICEIP(ip net.IP) bool {
	return !ip.IsLinkLocalUnicast() && !ip.IsLoopback()
}

// iceLocalIPs lists the addresses a shared mux binds, as a stable key.
func iceLocalIPs() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	var ips []string
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || !keepICEInterface(ifc.Name) {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && keepICEIP(n.IP) {
				ips = append(ips, n.IP.String())
			}
		}
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
