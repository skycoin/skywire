// Package dmsgclient pkg/dmsg/dmsgclient/streampool.go c1-net-dmsg
package dmsgclient

import (
	"bufio"
	"sync"
	"time"

	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
)

// poolIdleTimeout is how long an idle stream may sit in the pool before we
// drop it. It MUST stay below the dmsg-HTTP server's IdleTimeout
// (dmsg.StreamIdleTimeout-5s, i.e. 115s — see dmsghttp/http.go) so we retire
// a stream before the server does; otherwise every reuse races the server's
// teardown and we pay the reconnect anyway. 90s matches the net/http-side
// pool in dmsghttp.HTTPTransport.
const poolIdleTimeout = 90 * time.Second

// maxIdlePerHost caps idle streams held per destination. Sustained traffic to
// one host collapses onto a couple of long-lived streams; a burst of parallel
// requests may briefly exceed this, and the surplus is closed on return
// rather than pooled.
const maxIdlePerHost = 4

// pooledStream is an idle dmsg stream plus the bufio.Reader that owns its
// buffered bytes. The reader MUST travel with the stream: after a keep-alive
// response the reader may hold bytes already pulled off the wire, so pairing a
// pooled stream with a fresh reader would silently drop them.
type pooledStream struct {
	s      *dmsg.Stream
	br     *bufio.Reader
	idleAt time.Time
}

// streamPool holds idle keep-alive streams per destination for one dmsg.Client.
type streamPool struct {
	mu   sync.Mutex
	idle map[dmsg.Addr][]*pooledStream
}

// clientPools maps each dmsg.Client to its own pool. Keyed by client because
// streams are only interchangeable within the client that dialed them.
//
// Entries are dropped by releasePools when a client is closed. A visor holds
// one long-lived dmsg.Client, so this map stays tiny in practice.
var (
	clientPoolsMu sync.Mutex
	clientPools   = make(map[*dmsg.Client]*streamPool)
)

// poolFor returns (creating if needed) the pool for dmsgC.
func poolFor(dmsgC *dmsg.Client) *streamPool {
	clientPoolsMu.Lock()
	defer clientPoolsMu.Unlock()
	p, ok := clientPools[dmsgC]
	if !ok {
		p = &streamPool{idle: make(map[dmsg.Addr][]*pooledStream)}
		clientPools[dmsgC] = p
	}
	return p
}

// ReleasePools closes and forgets every pooled stream belonging to dmsgC. Call
// it when a dmsg.Client is torn down so the pool doesn't pin dead streams (and
// the client itself) alive. Safe to call more than once.
func ReleasePools(dmsgC *dmsg.Client) {
	clientPoolsMu.Lock()
	p, ok := clientPools[dmsgC]
	delete(clientPools, dmsgC)
	clientPoolsMu.Unlock()
	if !ok {
		return
	}
	p.mu.Lock()
	streams := p.idle
	p.idle = make(map[dmsg.Addr][]*pooledStream)
	p.mu.Unlock()
	for _, list := range streams {
		for _, ps := range list {
			ps.s.Close() //nolint:errcheck,gosec
		}
	}
}

// get pops a live idle stream for addr, or returns nil if none is usable.
// Streams idle past poolIdleTimeout are closed rather than returned — the
// server may already have retired them.
func (p *streamPool) get(addr dmsg.Addr) *pooledStream {
	p.mu.Lock()
	defer p.mu.Unlock()

	list := p.idle[addr]
	for len(list) > 0 {
		// Take the most recently returned stream: it has the longest
		// remaining time before the server's idle timeout fires.
		ps := list[len(list)-1]
		list = list[:len(list)-1]
		if time.Since(ps.idleAt) < poolIdleTimeout {
			p.idle[addr] = list
			return ps
		}
		ps.s.Close() //nolint:errcheck,gosec
	}
	if len(list) == 0 {
		delete(p.idle, addr)
	} else {
		p.idle[addr] = list
	}
	return nil
}

// put returns a stream to the idle pool. The stream must have had its response
// body fully consumed, so the next request starts at a clean message boundary.
func (p *streamPool) put(addr dmsg.Addr, ps *pooledStream) {
	ps.idleAt = time.Now()

	p.mu.Lock()
	if len(p.idle[addr]) >= maxIdlePerHost {
		p.mu.Unlock()
		ps.s.Close() //nolint:errcheck,gosec
		return
	}
	p.idle[addr] = append(p.idle[addr], ps)
	p.mu.Unlock()
}
