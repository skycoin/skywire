// Package skysocks pkg/skysocks/localresolver.go c4-app-proxy
//
// Mesh names answered by the visor's own resolving proxy, over the data plane
// this app already holds.
//
// Before this, a browser wanting both clearnet and `<pk>.skynet` had to be
// pointed at a CHAIN of localhost proxies: the resolvers' listeners in front,
// forwarding what they did not resolve to this client's :1080. Every clearnet
// request then paid two extra localhost SOCKS handshakes, and the operator
// configured the same port numbers twice.
//
// The visor publishes each resolving proxy as a local service (see
// pkg/app/appnet/local_service.go), so this client can keep ONE listener and
// hand a mesh name straight to the resolver: a dial to its own visor's PK, which
// the visor answers in-process over net.Pipe. The resolver is still the only
// thing that resolves; this only removes the hop.
//
// A mesh name needs no exit, which is why the lookup happens before a tunnel is
// picked: `<pk>.skynet` resolves while the exit is down, and while there is no
// exit configured at all.
package skysocks

import (
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/routing"
)

// LocalResolvers is the set of resolving proxies on this visor an app may hand a
// hostname to, and the dial that reaches one. A nil *LocalResolvers answers for
// nothing, so every caller can hold one unconditionally.
type LocalResolvers struct {
	svcs []appnet.LocalService
	dial func(port routing.Port) (net.Conn, error)
}

// NewLocalResolvers keeps the services that answer for at least one hostname
// suffix. dial must reach the service's port on this visor's own PK; a nil dial
// (or no suffix-bearing service) yields a set that answers for nothing.
func NewLocalResolvers(svcs []appnet.LocalService, dial func(port routing.Port) (net.Conn, error)) *LocalResolvers {
	if dial == nil {
		return nil
	}
	keep := make([]appnet.LocalService, 0, len(svcs))
	for _, svc := range svcs {
		if len(svc.Suffixes) > 0 {
			keep = append(keep, svc)
		}
	}
	if len(keep) == 0 {
		return nil
	}
	return &LocalResolvers{svcs: keep, dial: dial}
}

// Suffixes lists every hostname suffix this set answers for, for a log line.
func (r *LocalResolvers) Suffixes() []string {
	if r == nil {
		return nil
	}
	var out []string
	for _, svc := range r.svcs {
		out = append(out, svc.Suffixes...)
	}
	return out
}

// PortFor returns the resolver that answers for host. A suffix matches on a
// label boundary, so ".skynet" matches "<pk>.skynet" and "status.skynet" but
// never "notskynet".
func (r *LocalResolvers) PortFor(host string) (routing.Port, bool) {
	if r == nil || host == "" {
		return 0, false
	}
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	for _, svc := range r.svcs {
		for _, suffix := range svc.Suffixes {
			s := strings.ToLower(suffix)
			if !strings.HasPrefix(s, ".") {
				s = "." + s
			}
			// A name that is only the suffix ("skynet") is not a mesh name: there
			// is no destination label in front of it.
			if len(h) > len(s) && strings.HasSuffix(h, s) {
				return svc.Port, true
			}
		}
	}
	return 0, false
}

// Dial opens a connection to the resolver on port.
func (r *LocalResolvers) Dial(port routing.Port) (net.Conn, error) {
	if r == nil || r.dial == nil {
		return nil, fmt.Errorf("no local resolver dial for port %d", port)
	}
	return r.dial(port)
}

// openWindow bounds the handshake with the resolver. It is in-process, so this
// is only a guard against a wedged handler pinning the browser's goroutine.
const openWindow = 10 * time.Second

// Open dials the resolver on port and completes the SOCKS5 handshake with it on
// the browser's behalf: the greeting and CONNECT request the browser already
// sent are replayed byte-for-byte. The returned conn is positioned where the
// browser's conn is — the resolver's CONNECT reply is the next thing on it, for
// the caller's splice to carry back.
//
// The exchange is strictly sequential, unlike the exit's pipelined open. A local
// service is served over net.Pipe, which has NO buffer: the resolver answers the
// greeting before it reads the request, so its reply write blocks until someone
// reads it — and a caller still writing a pipelined request would be that
// someone. Pipelining here deadlocks both ends. In-process there is no round
// trip to save anyway.
func (r *LocalResolvers) Open(port routing.Port, greeting, req []byte) (net.Conn, error) {
	conn, err := r.Dial(port)
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(openWindow)) //nolint:errcheck

	if _, err := conn.Write(greeting); err != nil {
		closeConn(conn)
		return nil, fmt.Errorf("local resolver: write greeting: %w", err)
	}

	method := make([]byte, 2)
	if _, err := io.ReadFull(conn, method); err != nil {
		closeConn(conn)
		return nil, fmt.Errorf("local resolver: read method reply: %w", err)
	}
	if method[0] != 0x05 || method[1] != 0x00 {
		closeConn(conn)
		return nil, fmt.Errorf("local resolver selected non-no-auth method %v", method)
	}

	if _, err := conn.Write(req); err != nil {
		closeConn(conn)
		return nil, fmt.Errorf("local resolver: write request: %w", err)
	}

	// The splice that follows is deadline-free, like the exit's.
	_ = conn.SetDeadline(time.Time{}) //nolint:errcheck
	return conn, nil
}

func closeConn(conn net.Conn) {
	_ = conn.Close() //nolint:errcheck
}

// Splice copies between a browser conn and a resolver conn until both directions
// have ended, half-closing each way as it goes so a client that shuts down its
// write side once its request is complete still receives the whole reply — the
// same half-close rule the exit splice follows.
//
// It is here rather than on Client because the disconnected listener has no
// Client and serves mesh names too.
func Splice(browser, resolver net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(resolver, browser) //nolint:errcheck
		halfCloseWrite(resolver)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(browser, resolver) //nolint:errcheck
		halfCloseWrite(browser)
		done <- struct{}{}
	}()
	<-done
	<-done
}

// localResolverRefreshInterval is how often a client holding NO resolvers
// re-asks the visor for them.
const localResolverRefreshInterval = time.Minute

// resolverRefresher boxes the lookup so it can be swapped atomically: the app
// installs it after NewClient has already started the keepalive goroutine that
// reads it.
type resolverRefresher struct{ fn func() *LocalResolvers }

// SetLocalResolverRefresh installs the lookup that re-reads the visor's
// published resolving proxies, consulted on the keepalive tick while this client
// has none.
//
// It exists because this app and the resolvers are all launcher apps started in
// no fixed order: an empty answer at connect time often just means the resolver
// had not published itself yet, and waiting for the next reconnect cycle to find
// out would be hours of mesh names going to an exit that cannot reach them.
func (c *Client) SetLocalResolverRefresh(refresh func() *LocalResolvers) {
	if refresh == nil {
		c.resolverRefresh.Store(nil)
		return
	}
	c.resolverRefresh.Store(&resolverRefresher{fn: refresh})
}

// pullLocalResolvers re-reads the published resolvers while there are none, at
// most once per localResolverRefreshInterval. Once a set is in hand nothing is
// re-read: a change lands on the next connect cycle, which re-asks anyway.
//
// Called only from the keepalive goroutine, which is what lets refreshedAt be a
// plain field.
func (c *Client) pullLocalResolvers(now time.Time) {
	refresh := c.resolverRefresh.Load()
	if refresh == nil || refresh.fn == nil || c.localResolvers() != nil {
		return
	}
	if !c.resolverRefreshedAt.IsZero() && now.Sub(c.resolverRefreshedAt) < localResolverRefreshInterval {
		return
	}
	c.resolverRefreshedAt = now
	if r := refresh.fn(); r != nil {
		c.SetLocalResolvers(r)
	}
}
