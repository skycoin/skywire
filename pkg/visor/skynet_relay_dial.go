// Package visor pkg/visor/skynet_relay_dial.go c3-vis-core
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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/proxystatus"
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

// relayDialError is errNoRelay with what each attempt answered, kept for
// `visor state --select diag` (dmsg.over_skynet.failures).
type relayDialError struct {
	dst        cipher.PubKey
	direct     string
	candidates int
	confirmed  int
	tries      []relayTry
	skipped    bool // the relay tier was not tried (relaySkipTTL)
}

// e2eRefused reports whether every relay was tried and each stream ended in
// its end-to-end Noise handshake. A destination older than #5707 answers a
// relayed stream that way, so no relay can reach it until it updates.
func (e *relayDialError) e2eRefused() bool {
	if e.skipped || len(e.tries) == 0 || len(e.tries) < e.candidates {
		return false
	}
	for _, t := range e.tries {
		if !strings.Contains(t.err, "noise handshake") {
			return false
		}
	}
	return true
}

type relayTry struct {
	relay     cipher.PubKey
	confirmed bool
	stage     string
	err       string
}

func (e *relayDialError) Error() string {
	if e.skipped {
		return errNoRelay.Error() + " (relays skipped: they failed to reach it recently)"
	}
	return errNoRelay.Error()
}
func (e *relayDialError) Unwrap() error { return errNoRelay }

// dialViaRelay reaches remote:port through a shared relay. Candidates are tried
// in PARALLEL — first successful handshake wins — so a bounded blind-try (used
// when the transport graph can't confirm which peer reaches the dst) stays
// fast: wrong guesses just fail their own handshake without delaying the
// winner. Returns a *relayDialError (errNoRelay) if none succeed; the caller
// then falls back to a multihop route.
func (d *routerSkynetDialer) dialViaRelay(ctx context.Context, remote cipher.PubKey, port uint16) (net.Conn, error) {
	var mux *transport.VStreamMux
	if d.skynetMuxPtr != nil {
		mux = *d.skynetMuxPtr
	}
	fail := &relayDialError{dst: remote}
	if mux == nil {
		return nil, fail
	}
	relays, nConfirmed := d.discoverRelayCandidates(ctx, remote)
	fail.candidates, fail.confirmed = len(relays), nConfirmed
	if len(relays) == 0 {
		return nil, fail
	}

	winner := make(chan net.Conn, 1)
	var claimed atomic.Bool
	var wg sync.WaitGroup
	var triesMu sync.Mutex
	noteTry := func(relay cipher.PubKey, i int, stage string, err error) {
		triesMu.Lock()
		fail.tries = append(fail.tries, relayTry{relay: relay, confirmed: i < nConfirmed, stage: stage, err: err.Error()})
		triesMu.Unlock()
	}
	for i, r := range relays {
		wg.Add(1)
		go func(i int, relay cipher.PubKey) {
			defer wg.Done()
			stream, err := mux.DialThroughRelay(relay, remote, "")
			if err != nil {
				noteTry(relay, i, "relay", err)
				d.log.WithField("relay", relay.String()).WithField("remote", remote.String()).
					WithError(err).Debug("Skynet relay: DialThroughRelay failed")
				return
			}
			conn := &vstreamConn{VStream: stream}
			if herr := d.handshake(conn, remote, port); herr != nil {
				conn.Close() //nolint:errcheck,gosec
				noteTry(relay, i, "handshake", herr)
				d.log.WithField("relay", relay.String()).WithField("remote", remote.String()).
					WithError(herr).Debug("Skynet relay: handshake over relayed stream failed")
				return
			}
			if claimed.CompareAndSwap(false, true) {
				d.log.WithField("remote", remote.String()).
					WithField("relay", relay.String()).
					WithField("port", port).
					Debug("Skynet: reached destination via 1-hop relay (no route)")
				proxystatus.NotePath(ctx, "relay via "+relay.String()+" on "+d.transportNote(stream.TransportID()))
				winner <- conn
			} else {
				conn.Close() //nolint:errcheck,gosec // another relay already won
			}
		}(i, r)
	}
	go func() { wg.Wait(); close(winner) }()
	if conn, ok := <-winner; ok {
		return conn, nil
	}
	return nil, fail
}

// discoverRelayCandidates returns up to maxRelayFanout peers to try as 1-hop
// relays: peers we have a direct non-dmsg transport to. Transport-graph-
// CONFIRMED relays (peers the destination also has a transport to) are ordered
// first, then our other direct peers are appended as blind fallbacks. The
// count returned is how many of the leading candidates are confirmed.
func (d *routerSkynetDialer) discoverRelayCandidates(ctx context.Context, remote cipher.PubKey) ([]cipher.PubKey, int) {
	// Our own directly-connected non-dmsg peers — the only possible 1-hop
	// relays. Computed first so we can both intersect with the transport graph
	// and, if that's unavailable, blind-try them.
	myPeers := d.directNonDmsgPeers(remote)
	if len(myPeers) == 0 {
		return nil, 0
	}

	// Prefer relays the transport graph CONFIRMS reach the destination.
	if dc := d.tpM.Conf.DiscoveryClient; dc != nil {
		remotePeers, err := relayGraph.peers(ctx, dc, d.localPK, remote)
		if err != nil {
			d.log.WithField("remote", remote.String()).WithError(err).Debug("Skynet relay: transport-graph query failed")
		} else {
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
			nConfirmed = min(nConfirmed, len(ordered))
			if len(ordered) > 0 {
				d.log.WithField("remote", remote.String()).
					WithField("confirmed", nConfirmed).
					WithField("candidates", len(ordered)).
					Debug("Skynet relay: candidate set (confirmed relays first, blind fallbacks appended)")
				return ordered, nConfirmed
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
	return myPeers, 0
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
	var direct string
	if mux != nil {
		if stream, err := mux.Dial(remote, ""); err == nil {
			conn := &vstreamConn{VStream: stream}
			herr := d.handshake(conn, remote, port)
			if herr == nil {
				d.log.WithField("remote", remote.String()).Debug("dmsg-over-skynet: direct transport")
				proxystatus.NotePath(ctx, d.transportNote(stream.TransportID()))
				return conn, nil
			}
			conn.Close() //nolint:errcheck,gosec
			direct = "handshake: " + herr.Error()
		} else {
			direct = err.Error()
		}
	}
	if d.noRelay {
		return nil, &relayDialError{dst: remote, direct: direct, skipped: true}
	}
	conn, err := d.dialViaRelay(ctx, remote, port)
	var rerr *relayDialError
	if errors.As(err, &rerr) {
		rerr.direct = direct
	}
	return conn, err
}

// handshake runs the skynet handshake with host on a fresh stream: the plain
// one, or for d.service the one that names it and checks host's proof.
func (d *routerSkynetDialer) handshake(conn net.Conn, host cipher.PubKey, port uint16) error {
	if d.service.Null() {
		return performHandshakeWithTimeout(conn, port, relayHandshakeTimeout)
	}
	done := make(chan error, 1)
	go func() { done <- skynetweb.PerformServiceHandshake(conn, port, d.service, host) }()
	select {
	case err := <-done:
		return err
	case <-time.After(relayHandshakeTimeout):
		conn.Close() //nolint:errcheck,gosec
		return errors.New("skynet service handshake timed out")
	}
}
