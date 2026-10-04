// Package visor pkg/visor/dmsg_over_skynet.go c3-vis-core
//
// "dmsg over skynet transports": serve a .dmsg fetch by reaching the peer's
// :80 over a skynet transport via the VStreamMux relay (direct → 1-hop relay,
// route ID 0, PK-addressed, NO route-finder), with dmsg-servers as the only
// fallback. dmsg is a relay layer — this path never uses a route.
package visor

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// The connections DmsgHTTP keeps open, per visor. A connection costs a dial
// (a relay search and the transport handshakes, or a dmsg stream) and is then
// reused by the requests that follow, as a browser reuses its connections: a
// dashboard page making a dozen queries to one peer pays for one or a few
// connections instead of a dial each.
const (
	dmsgHTTPMaxIdleConns        = 32
	dmsgHTTPMaxIdleConnsPerHost = 4
	dmsgHTTPIdleConnTimeout     = 60 * time.Second
)

// dmsgHTTPTransport returns the visor's shared HTTP transport for DmsgHTTP.
func (v *Visor) dmsgHTTPTransport() *http.Transport {
	v.dmsgHTTPOnce.Do(func() {
		v.dmsgHTTPTr = &http.Transport{
			DialContext:         v.dialDmsgHTTP,
			MaxIdleConns:        dmsgHTTPMaxIdleConns,
			MaxIdleConnsPerHost: dmsgHTTPMaxIdleConnsPerHost,
			IdleConnTimeout:     dmsgHTTPIdleConnTimeout,
		}
	})
	return v.dmsgHTTPTr
}

// dialDmsgHTTP opens a connection to the peer at addr ("<pk>:<port>"): over a
// skynet transport when the visor has one to the peer or to a relay that
// does, else a dmsg stream through the dmsg servers. Deployment services have
// no skynet transports to reach, so they go straight to dmsg: trying skynet
// first can only fail, and failing takes up to twelve seconds (a TPD query,
// then every candidate relay waiting out its handshake).
func (v *Visor) dialDmsgHTTP(ctx context.Context, _, addr string) (net.Conn, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	var pk cipher.PubKey
	if err := pk.Set(host); err != nil {
		return nil, fmt.Errorf("not a dmsg address %q: %w", addr, err)
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return nil, fmt.Errorf("invalid port in %q: %w", addr, err)
	}
	if !v.isDmsgServiceKey(pk) {
		if conn, err := v.dialDmsgOverSkynet(ctx, pk, uint16(port)); err == nil {
			return conn, nil
		}
	}
	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return v.dmsgC.DialStream(dctx, dmsg.Addr{PK: pk, Port: uint16(port)})
}

// dialDmsgOverSkynet reaches pk:port over the visor-relay rather than a
// dmsg-server session: a direct transport, else a 1-hop relay. It works
// because a visor mirrors :80 over both dmsg and its skynet forwarding server.
func (v *Visor) dialDmsgOverSkynet(ctx context.Context, pk cipher.PubKey, port uint16) (net.Conn, error) {
	if v.router == nil || v.tpM == nil || v.skynetFwdMux == nil {
		err := fmt.Errorf("skynet not up: %w", errNoRelay)
		v.dmsgSkynet.note(pk, err)
		return nil, err
	}
	dialer := &routerSkynetDialer{
		router:       v.router,
		localPK:      v.conf.PK,
		log:          v.log,
		tpM:          v.tpM,
		skynetMuxPtr: &v.skynetFwdMux,
	}
	dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	conn, err := dialer.dialDirectOrRelay(dctx, pk, port)
	v.dmsgSkynet.note(pk, err)
	if err != nil {
		return nil, err
	}
	v.log.WithField("remote", pk.String()).WithField("port", port).
		Debug("dmsg-over-skynet: connected over a skynet transport (no route, no dmsg-server)")
	return conn, nil
}

// dmsgWebSkynetDial is the resolving proxy's way onto the skynet transports
// for a .dmsg peer: the same dial DmsgHTTP uses, without the dmsg fallback,
// which the proxy makes itself. Deployment services have no skynet transports
// to reach, so they are refused at once rather than after a relay search.
func (v *Visor) dmsgWebSkynetDial(ctx context.Context, pk cipher.PubKey, port uint16) (net.Conn, error) {
	if v.isDmsgServiceKey(pk) {
		return nil, errNoRelay
	}
	return v.dialDmsgOverSkynet(ctx, pk, port)
}

// dmsgSkynetStats counts how the visor's dials to .dmsg peers went, for
// `visor state --select diag`: carried over skynet, or fallen back to dmsg.
type dmsgSkynetStats struct {
	skynet, fallback atomic.Uint64
	mu               sync.Mutex
	lastErr          string
	lastPK           string
}

func (s *dmsgSkynetStats) note(pk cipher.PubKey, err error) {
	if err == nil {
		s.skynet.Add(1)
		return
	}
	s.fallback.Add(1)
	s.mu.Lock()
	s.lastErr, s.lastPK = err.Error(), pk.String()
	s.mu.Unlock()
}

func (s *dmsgSkynetStats) snapshot() *visorapi.DiagDmsgOverSkynet {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &visorapi.DiagDmsgOverSkynet{
		Skynet:    s.skynet.Load(),
		Fallback:  s.fallback.Load(),
		LastError: s.lastErr,
		LastPK:    s.lastPK,
	}
}
