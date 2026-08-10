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
	"sync"
	"sync/atomic"
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
	// maxRelayFanout bounds the blind-try fallback: when the transport graph
	// can't confirm which of our peers reaches the destination, we try this
	// many of our own direct peers as relays in parallel. Each relay drops the
	// SYN if it can't actually reach the dst, so wrong guesses fail their own
	// handshake harmlessly; the bound caps the fan-out (amplification).
	maxRelayFanout = 8
)

var errNoRelay = errors.New("skynet: no usable 1-hop relay to destination")

// dialViaRelay reaches remote:port through a shared relay. Candidates are tried
// in PARALLEL — first successful handshake wins — so a bounded blind-try (used
// when the transport graph can't confirm which peer reaches the dst) stays
// fast: wrong guesses just fail their own handshake without delaying the
// winner. Returns errNoRelay if none succeed; the caller then falls back to a
// multihop route.
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

	winner := make(chan net.Conn, 1)
	var claimed atomic.Bool
	var wg sync.WaitGroup
	for _, r := range relays {
		wg.Add(1)
		go func(relay cipher.PubKey) {
			defer wg.Done()
			stream, err := mux.DialThroughRelay(relay, remote, "")
			if err != nil {
				d.log.WithField("relay", relay.String()).WithField("remote", remote.String()).
					WithError(err).Debug("Skynet relay: DialThroughRelay failed")
				return
			}
			conn := &vstreamConn{VStream: stream}
			if herr := performHandshakeWithTimeout(conn, port, relayHandshakeTimeout); herr != nil {
				conn.Close() //nolint:errcheck,gosec
				d.log.WithField("relay", relay.String()).WithField("remote", remote.String()).
					WithError(herr).Debug("Skynet relay: handshake over relayed stream failed")
				return
			}
			if claimed.CompareAndSwap(false, true) {
				d.log.WithField("remote", remote.String()).
					WithField("relay", relay.String()).
					WithField("port", port).
					Debug("Skynet: reached destination via 1-hop relay (no route)")
				winner <- conn
			} else {
				conn.Close() //nolint:errcheck,gosec // another relay already won
			}
		}(r)
	}
	go func() { wg.Wait(); close(winner) }()
	if conn, ok := <-winner; ok {
		return conn, nil
	}
	return nil, errNoRelay
}

// discoverRelayCandidates returns up to maxRelayFanout peers to try as 1-hop
// relays: peers we have a direct non-dmsg transport to. Transport-graph-
// CONFIRMED relays (peers the destination also has a transport to) are ordered
// first, then our other direct peers are appended as blind fallbacks.
func (d *routerSkynetDialer) discoverRelayCandidates(ctx context.Context, remote cipher.PubKey) []cipher.PubKey {
	// Our own directly-connected non-dmsg peers — the only possible 1-hop
	// relays. Computed first so we can both intersect with the transport graph
	// and, if that's unavailable, blind-try them.
	myPeers := d.directNonDmsgPeers(remote)
	if len(myPeers) == 0 {
		return nil
	}

	// Prefer relays the transport graph CONFIRMS reach the destination.
	if dc := d.tpM.Conf.DiscoveryClient; dc != nil {
		qctx, cancel := context.WithTimeout(ctx, relayDiscoveryTimeout)
		entries, err := dc.GetTransportsByEdge(qctx, remote)
		cancel()
		if err != nil {
			d.log.WithField("remote", remote.String()).WithError(err).Debug("Skynet relay: transport-graph query failed")
		} else {
			remotePeers := make(map[cipher.PubKey]struct{}, len(entries))
			for _, e := range entries {
				if e.Type == "dmsg" {
					continue
				}
				if peer := e.RemoteEdge(remote); peer != remote && peer != d.localPK {
					remotePeers[peer] = struct{}{}
				}
			}
			// Order candidates: transport-graph-CONFIRMED relays first, then our
			// other direct peers as blind fallbacks. dialViaRelay dials the whole
			// set in parallel (first success wins), so if the confirmed relays
			// can't actually bridge — an older peer without relay-forwarding
			// support, or a stale transport-graph edge — a working peer in the
			// same batch still completes the dial. Bounded by maxRelayFanout.
			ordered := make([]cipher.PubKey, 0, len(myPeers))
			seen := make(map[cipher.PubKey]struct{}, len(myPeers))
			var nConfirmed int
			for _, p := range myPeers {
				if _, ok := remotePeers[p]; ok {
					ordered = append(ordered, p)
					seen[p] = struct{}{}
					nConfirmed++
				}
			}
			for _, p := range myPeers {
				if len(ordered) >= maxRelayFanout {
					break
				}
				if _, dup := seen[p]; !dup {
					ordered = append(ordered, p)
					seen[p] = struct{}{}
				}
			}
			if len(ordered) > maxRelayFanout {
				ordered = ordered[:maxRelayFanout]
			}
			if len(ordered) > 0 {
				d.log.WithField("remote", remote.String()).
					WithField("confirmed", nConfirmed).
					WithField("candidates", len(ordered)).
					Debug("Skynet relay: candidate set (confirmed relays first, blind fallbacks appended)")
				return ordered
			}
		}
	}

	// Transport graph unavailable, stale, or empty for this destination (e.g.
	// TPD backlog, or a freshly-restarted dst whose edges haven't propagated).
	// Blind-try our own direct peers (bounded): each relay validates it can
	// actually reach the dst before bridging, and dialViaRelay dials them in
	// parallel, so wrong guesses just fail their own handshake without delaying
	// a relay that works. This keeps the relay usable without a hard, always-
	// fresh dependency on TPD.
	if len(myPeers) > maxRelayFanout {
		myPeers = myPeers[:maxRelayFanout]
	}
	d.log.WithField("remote", remote.String()).WithField("candidates", len(myPeers)).
		Debug("Skynet relay: transport graph gave no confirmed relay; blind-trying direct peers")
	return myPeers
}

// directNonDmsgPeers returns our currently-open, non-dmsg direct transport
// peers (deduped), excluding self and exclude. These are the only peers that
// can serve as a 1-hop relay.
func (d *routerSkynetDialer) directNonDmsgPeers(exclude cipher.PubKey) []cipher.PubKey {
	seen := make(map[cipher.PubKey]struct{})
	var peers []cipher.PubKey
	d.tpM.WalkTransports(func(tp *transport.ManagedTransport) bool {
		r := tp.Remote()
		if tp.IsClosed() || tp.Type() == "dmsg" || r == d.localPK || r == exclude {
			return true
		}
		if _, dup := seen[r]; dup {
			return true
		}
		seen[r] = struct{}{}
		peers = append(peers, r)
		return true
	})
	return peers
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
