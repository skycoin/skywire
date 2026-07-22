// Package meshgw pkg/vpnrouter/meshgw/ippool.go c4-app-vpn
package meshgw

import (
	"encoding/binary"
	"fmt"
	"net"
	"sync"
)

// ipPool leases synthetic IPv4 addresses from a CIDR, one per distinct mesh
// target. Addresses are never reused within a run (a bounded gateway serves far
// fewer names than a /16 has hosts); exhaustion is a hard error rather than a
// silent wrap, so a stale mapping can never be handed to a new target.
type ipPool struct {
	net  *net.IPNet
	mu   sync.Mutex
	base uint32 // first usable host (network+1)
	last uint32 // last usable host (broadcast-1)
	next uint32 // next to hand out
}

func newIPPool(cidr string) (*ipPool, error) {
	_, ipn, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, fmt.Errorf("meshgw: bad synthetic CIDR %q: %w", cidr, err)
	}
	v4 := ipn.IP.To4()
	if v4 == nil {
		return nil, fmt.Errorf("meshgw: synthetic CIDR %q is not IPv4", cidr)
	}
	network := binary.BigEndian.Uint32(v4)
	mask := binary.BigEndian.Uint32(net.IP(ipn.Mask).To4())
	broadcast := network | ^mask
	base := network + 1
	if base >= broadcast {
		return nil, fmt.Errorf("meshgw: synthetic CIDR %q too small", cidr)
	}
	return &ipPool{net: ipn, base: base, last: broadcast - 1, next: base}, nil
}

// nextIP leases the next synthetic address.
func (p *ipPool) nextIP() ([4]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.next > p.last {
		return [4]byte{}, fmt.Errorf("meshgw: synthetic IP pool exhausted (%s)", p.net)
	}
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], p.next)
	p.next++
	return b, nil
}
