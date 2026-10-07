package addrresolver

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	types "github.com/skycoin/skywire/pkg/transport/types"
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

// notFoundFor is how long a peer with no binding of a type is answered "no
// entry" without asking again. A dial to such a peer cannot work, and every
// dial used to ask, so peers without a binding cost a request per attempt. A
// peer that binds within the window is found when it passes.
const notFoundFor = 2 * time.Minute

type notFoundCache struct {
	mu sync.Mutex
	at map[string]time.Time
}

func (n *notFoundCache) recent(key string, now time.Time) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	at, ok := n.at[key]
	return ok && now.Sub(at) < notFoundFor
}

func (n *notFoundCache) note(key string, now time.Time) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.at == nil {
		n.at = make(map[string]time.Time)
	}
	if len(n.at) > 4096 {
		for k, at := range n.at {
			if now.Sub(at) >= notFoundFor {
				delete(n.at, k)
			}
		}
	}
	n.at[key] = now
}

func notFoundKey(tType string, pk cipher.PubKey) string {
	return string(types.NormalizeType(types.Type(tType))) + "/" + pk.Hex()
}
