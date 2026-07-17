//go:build !tinygo

// Package addrresolver pkg/transport/network/addrresolver/client_sudph.go c2-net-transport
//
// SUDPH (UDP hole-punching) is the only part of the AR client that pulls in
// github.com/AudriusButkevicius/pfilter (and, transitively, quic-go) and
// xtaci/kcp-go. A browser visor can neither open a UDP socket nor run SUDPH,
// so these methods — and the pfilter-typed BindSUDPH — are build-tagged out of
// the js/wasm build. The rest of the AR client (bind/resolve over HTTP-over-
// dmsg, used to look up stcpr/quic/wt peers) stays platform-neutral in
// client.go. Callers that need SUDPH type-assert the APIClient to SUDPHBinder.
package addrresolver

import (
	"encoding/json"
	"errors"
	"net"
	"time"

	"github.com/AudriusButkevicius/pfilter"
	"github.com/xtaci/kcp-go"

	"github.com/skycoin/skywire/pkg/netutil"
	"github.com/skycoin/skywire/pkg/transport/network/packetfilter"
)

// SUDPHBinder is APIClient plus the SUDPH bind entrypoint. The SUDPH transport
// (pkg/transport/network/sudph.go, itself !tinygo) type-asserts the resolver to
// this interface so the pfilter-typed method stays off the browser APIClient.
type SUDPHBinder interface {
	APIClient
	BindSUDPH(filter *pfilter.PacketFilter, handshake Handshake) (<-chan RemoteVisor, error)
}

func (c *httpClient) BindSUDPH(filter *pfilter.PacketFilter, hs Handshake) (<-chan RemoteVisor, error) {
	log := c.log.WithField("func", "httpClient.BindSUDPH")
	if !c.isReady() {
		log.Debug("BindSUDPH: Address resolver is not ready yet, waiting...")
		<-c.ready
		log.Debug("BindSUDPH: Address resolver became ready, binding")
	}

	// First connect must succeed for the caller to consider SUDPH initialized;
	// surface dial / handshake / register failure synchronously.
	arConn, localAddresses, err := c.connectSUDPH(filter, hs)
	if err != nil {
		return nil, err
	}

	c.sudphArConnMu.Lock()
	c.sudphArConn = arConn
	c.sudphArConnMu.Unlock()
	c.sudphLocalAddr = localAddresses

	// addrCh is the long-lived channel returned to the caller. It survives
	// individual arConn instances; reconnects do not close it. It is closed
	// exactly once, on shutdown, by serveSUDPHReconnect's defer.
	addrCh := make(chan RemoteVisor, addrChSize)

	// Single delBindSUDPH goroutine for the lifetime of the client. It
	// blocks until c.closed fires, then writes the unbind packet on
	// whichever arConn is current at that moment.
	c.delBindSudphWg.Add(1)
	go c.delBindSUDPHLoop()

	go c.serveSUDPHReconnect(filter, hs, arConn, addrCh)

	return addrCh, nil
}

// connectSUDPH establishes a fresh KCP+handshake session with the AR's UDP
// listener and posts the initial register payload. Called once on initial
// BindSUDPH and again on every reconnect attempt.
//
// A fresh per-connection packet-filter conn (c.sudphConn) is built on every
// call. kcp client sockets close their underlying conn on Close (see
// xtaci/kcp-go sess.go: a session with no listener closes s.conn), so the
// prior c.sudphConn is already dead by reconnect time and reusing it fails
// every attempt with "use of closed network connection". The local UDP port
// is the SHARED listener's — filter.NewConn does not allocate a new port — so
// it stays stable across rebuilds anyway, both for AR's record of our public
// address and for remote visors that received our (PK, port) tuple via Resolve.
func (c *httpClient) connectSUDPH(filter *pfilter.PacketFilter, hs Handshake) (net.Conn, LocalAddresses, error) {
	if c.remoteUDPAddr == "" {
		// dmsg-only AR with no udp_address in /health. Surface a clear
		// reason so the visor log isn't a confusing UDP-resolve error.
		return nil, LocalAddresses{}, errors.New("AR has no UDP address (dmsg-only AR did not advertise udp_address in /health); SUDPH unavailable")
	}
	rAddr, err := net.ResolveUDPAddr("udp", c.remoteUDPAddr)
	if err != nil {
		return nil, LocalAddresses{}, err
	}

	// Drop any prior (kcp-closed) conn and build a fresh one. The defensive
	// Close is a no-op when kcp already closed it and guards against leaking a
	// filter conn on the rare path where it is still open.
	if c.sudphConn != nil {
		_ = c.sudphConn.Close() //nolint:errcheck,gosec
	}
	c.sudphConn = filter.NewConn(sudphPriority, packetfilter.NewAddressFilter(rAddr, c.mLog))

	_, localPort, err := net.SplitHostPort(c.sudphConn.LocalAddr().String())
	if err != nil {
		return nil, LocalAddresses{}, err
	}

	kcpConn, err := kcp.NewConn(c.remoteUDPAddr, nil, 0, 0, c.sudphConn)
	if err != nil {
		return nil, LocalAddresses{}, err
	}
	arConn, err := hs(kcpConn)
	if err != nil {
		kcpConn.Close() //nolint:errcheck,gosec
		return nil, LocalAddresses{}, err
	}

	addresses, err := netutil.LocalAddresses()
	if err != nil {
		arConn.Close() //nolint:errcheck,gosec
		return nil, LocalAddresses{}, err
	}

	localAddresses := LocalAddresses{
		Addresses:  addresses,
		Port:       localPort,
		PublicIP:   c.LocalPublicIP(),
		PublicIPv6: c.localPublicIPv6Raw(),
	}

	laData, err := json.Marshal(localAddresses)
	if err != nil {
		arConn.Close() //nolint:errcheck,gosec
		return nil, LocalAddresses{}, err
	}

	if _, err := arConn.Write(laData); err != nil {
		arConn.Close() //nolint:errcheck,gosec
		return nil, LocalAddresses{}, err
	}

	return arConn, localAddresses, nil
}

// serveSUDPHReconnect runs the per-connection loops (heartbeat / re-register /
// read-and-forward) over arConn and reconnects with backoff when the
// connection dies. The supplied addrCh is held across reconnects so callers
// see a single channel that survives transient AR outages, network blips,
// or KCP session resets. Without this loop, any of those events would
// silently exit the per-connection goroutines, leaving the visor unreachable
// via SUDPH until it was restarted.
func (c *httpClient) serveSUDPHReconnect(filter *pfilter.PacketFilter, hs Handshake, arConn net.Conn, addrCh chan<- RemoteVisor) {
	defer close(addrCh)

	backoff := sudphReconnectInitialBackoff
	for {
		c.serveSUDPHConn(arConn, addrCh)

		if c.isClosed() {
			return
		}

		c.log.Warn("SUDPH connection to address-resolver lost, reconnecting...")
		var newConn net.Conn
		var newLocalAddrs LocalAddresses
		for {
			select {
			case <-c.closed:
				return
			case <-time.After(backoff):
			}
			var err error
			newConn, newLocalAddrs, err = c.connectSUDPH(filter, hs)
			if err == nil {
				break
			}
			c.log.WithError(err).Warn("SUDPH reconnect failed, will retry")
			backoff *= 2
			if backoff > sudphReconnectMaxBackoff {
				backoff = sudphReconnectMaxBackoff
			}
		}
		backoff = sudphReconnectInitialBackoff
		arConn = newConn
		c.sudphArConnMu.Lock()
		c.sudphArConn = arConn
		c.sudphArConnMu.Unlock()
		c.sudphLocalAddr = newLocalAddrs
		c.log.Info("SUDPH reconnected to address-resolver")
	}
}
