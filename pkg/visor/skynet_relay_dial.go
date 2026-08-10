// Package visor pkg/visor/skynet_relay_dial.go c2-vis-net
//
// Relay tier for the SKYNET dialer (DialSkynet), i.e. `.skynet` hosts only.
// When there's no direct transport to a destination, reach it through a 1-hop
// relay — a peer we share a direct (non-dmsg) transport with that also has one
// to the destination — using VStreamMux.DialThroughRelay (route ID 0,
// PK-addressed forwarding, no route-finder, no setup-node). It slots between
// the direct-transport fast path and the existing multihop-route (RF) fallback:
// direct → relay → route.
//
// This does NOT touch the `.dmsg` path: dmsg is a relay layer and is never
// route-finder-routed. Making dmsg-addressed / deployment targets reachable via
// the visor-relay is a separate, relay-only integration (no RF fallback).
package visor

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skynetweb"
	"github.com/skycoin/skywire/pkg/transport"
)

const (
	// relayDiscoveryTimeout bounds the TPD query that finds a shared relay.
	relayDiscoveryTimeout = 4 * time.Second
	// relayHandshakeTimeout bounds the skynet handshake over a relayed
	// stream: if the relay can't reach the destination (stale transport
	// graph), no ready byte ever arrives, so we must not block forever.
	relayHandshakeTimeout = 8 * time.Second
	// maxRelayCandidates caps how many shared relays we try before falling
	// through to a multihop route.
	maxRelayCandidates = 3
)

var errNoRelay = errors.New("skynet: no usable 1-hop relay to destination")

// dialViaRelay reaches remote:port through a shared relay. Returns errNoRelay
// when no relay is found or none complete the handshake in time; the caller
// then falls back to a multihop route.
func (d *routerSkynetDialer) dialViaRelay(ctx context.Context, remote cipher.PubKey, port uint16) (net.Conn, error) {
	var mux *transport.VStreamMux
	if d.skynetMuxPtr != nil {
		mux = *d.skynetMuxPtr
	}
	if mux == nil {
		return nil, errNoRelay
	}
	relays := d.discoverRelayCandidates(ctx, remote)
	if len(relays) == 0 {
		return nil, errNoRelay
	}
	for _, r := range relays {
		stream, err := mux.DialThroughRelay(r, remote, "")
		if err != nil {
			d.log.WithField("relay", r.String()).WithError(err).Debug("Skynet relay: dial failed, trying next")
			continue
		}
		conn := &vstreamConn{VStream: stream}
		if err := performHandshakeWithTimeout(conn, port, relayHandshakeTimeout); err != nil {
			conn.Close() //nolint:errcheck,gosec
			d.log.WithField("relay", r.String()).WithError(err).Debug("Skynet relay: handshake failed, trying next")
			continue
		}
		d.log.WithField("remote", remote.String()).
			WithField("relay", r.String()).
			WithField("port", port).
			Debug("Skynet: reached destination via 1-hop relay (no route)")
		return conn, nil
	}
	return nil, errNoRelay
}

// discoverRelayCandidates returns up to maxRelayCandidates peers that (a) we
// have a direct non-dmsg transport to and (b) the destination also has one to,
// per the transport graph — i.e. peers that can relay a stream to remote in a
// single hop.
func (d *routerSkynetDialer) discoverRelayCandidates(ctx context.Context, remote cipher.PubKey) []cipher.PubKey {
	dc := d.tpM.Conf.DiscoveryClient
	if dc == nil {
		return nil
	}
	qctx, cancel := context.WithTimeout(ctx, relayDiscoveryTimeout)
	defer cancel()
	entries, err := dc.GetTransportsByEdge(qctx, remote)
	if err != nil {
		d.log.WithField("remote", remote.String()).WithError(err).Debug("Skynet relay: transport-graph query failed")
		return nil
	}
	// Peers the destination is directly (non-dmsg) connected to.
	remotePeers := make(map[cipher.PubKey]struct{}, len(entries))
	for _, e := range entries {
		if e.Type == "dmsg" {
			continue
		}
		peer := e.RemoteEdge(remote)
		if peer == remote || peer == d.localPK {
			continue
		}
		remotePeers[peer] = struct{}{}
	}
	if len(remotePeers) == 0 {
		return nil
	}
	// Intersect with our own directly-connected non-dmsg peers.
	seen := make(map[cipher.PubKey]struct{})
	var relays []cipher.PubKey
	d.tpM.WalkTransports(func(tp *transport.ManagedTransport) bool {
		r := tp.Remote()
		if tp.IsClosed() || tp.Type() == "dmsg" {
			return true
		}
		if _, ok := remotePeers[r]; !ok {
			return true
		}
		if _, dup := seen[r]; dup {
			return true
		}
		seen[r] = struct{}{}
		relays = append(relays, r)
		return len(relays) < maxRelayCandidates
	})
	return relays
}

// performHandshakeWithTimeout runs the skynet handshake with a hard deadline.
// VStream has no deadline support, so a relayed stream whose destination never
// responds would otherwise block forever; on timeout the caller closes the
// conn, which unblocks the handshake goroutine.
func performHandshakeWithTimeout(conn net.Conn, port uint16, timeout time.Duration) error {
	done := make(chan error, 1)
	go func() { done <- skynetweb.PerformHandshake(conn, port) }()
	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		return errors.New("skynet relay: handshake timed out")
	}
}

// dialDirectOrRelay reaches remote:port over skynet WITHOUT a route: a direct
// VStream when a non-dmsg transport exists, else a 1-hop relay (route-0,
// PK-addressed forwarding). Returns a handshaken conn, or an error if neither
// works. This is the "dmsg over skynet transports" primitive — used by the
// .dmsg reach path to serve a peer's :80 over the visor-relay, with
// dmsg-servers as the only fallback (never a route-finder route).
func (d *routerSkynetDialer) dialDirectOrRelay(ctx context.Context, remote cipher.PubKey, port uint16) (net.Conn, error) {
	var mux *transport.VStreamMux
	if d.skynetMuxPtr != nil {
		mux = *d.skynetMuxPtr
	}
	if mux != nil {
		if stream, err := mux.Dial(remote, ""); err == nil {
			conn := &vstreamConn{VStream: stream}
			if herr := performHandshakeWithTimeout(conn, port, relayHandshakeTimeout); herr == nil {
				d.log.WithField("remote", remote.String()).Debug("dmsg-over-skynet: direct transport")
				return conn, nil
			}
			conn.Close() //nolint:errcheck,gosec
		}
	}
	return d.dialViaRelay(ctx, remote, port)
}
