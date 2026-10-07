package addrresolver

import (
	"context"
	"net"

	"github.com/skycoin/skywire/pkg/cipher"
)

// FastResolve answers a lookup without asking the address resolver, from a
// copy of its bindings the caller holds, or reports false to fall through to
// GET /resolve. It is never asked for sudph: that resolve also has the address
// resolver ask the peer to dial back, which no local copy can do.
type FastResolve func(ctx context.Context, tType string, pk cipher.PubKey) (VisorData, bool)

// SetResolveFastPath installs fn in front of GET /resolve, or removes it with nil.
func (c *httpClient) SetResolveFastPath(fn FastResolve) {
	if fn == nil {
		c.fastResolve.Store(nil)
		return
	}
	c.fastResolve.Store(&fn)
}

// sameHost mirrors the address resolver's sameIP, so an answer from the fast
// path marks IsLocal exactly as GET /resolve would.
func sameHost(addr1, addr2 string) bool {
	h1, _, err := net.SplitHostPort(addr1)
	if err != nil {
		return false
	}
	h2, _, err := net.SplitHostPort(addr2)
	if err != nil {
		return false
	}
	return h1 == h2
}
