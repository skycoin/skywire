// Package dmsg pkg/dmsg/dmsg/client_relay.go
//
// A dmsg client as a RELAY (#4484 stage 3): a peer that cannot, or should not,
// hold its own dmsg server sessions attaches to this client over a skywire
// route (the skynet carrier, see CarrierSkynet) and this client carries the
// peer's stream requests over its own server sessions with the existing
// forwardRequest/bridgeStream path. No discovery registration, no TCP
// listener, no server peering: the relay is a server-role session on the
// shared EntityCommon, nothing more.
//
// What the relay sees is the signed StreamRequest envelope — enough to route
// it. What it cannot see is the stream: the Noise KK handshake inside the
// request runs between the attached peer and the destination, so the relay
// (and the server after it) only copy ciphertext. The server accepts the
// forwarded request because its signature verifies against the peer's own key
// (ServerConfig.AcceptRelayedRequests), and charges it to relay slots. The
// destination sees the attached peer's key, not the relay's.
package dmsg

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"time"

	"github.com/0magnet/yamux"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg/metrics"
)

// DefaultClientMaxRelayedStreams is the relay-slot cap a visor gives its dmsg
// client when it runs the relay acceptor: enough for a desk's worth of
// concurrent streams, small enough that an attached peer cannot amplify the
// visor into a public relay.
const DefaultClientMaxRelayedStreams = 256

// relayEntryLookupTimeout bounds the discovery lookup the relay makes to
// order its forward candidates by the destination's delegated servers. The
// lookup rides the relay's own sessions; a miss just falls back to
// latency order over every session.
const relayEntryLookupTimeout = 3 * time.Second

// ErrRelayPeerNotAllowed is returned by AcceptRelaySession when the allow
// callback refuses the handshaken peer.
var ErrRelayPeerNotAllowed = errors.New("dmsg: relay peer not allowed")

// SetSessionDialer installs (or, with nil, removes) the dialer used for
// carriers this client cannot dial natively. See Config.SessionDialer. Safe
// to call before Serve; takes effect for every subsequent session dial.
func (ce *Client) SetSessionDialer(d SessionDialer) {
	ce.convMx.Lock()
	ce.conf.SessionDialer = d
	ce.convMx.Unlock()
}

func (ce *Client) sessionDialer() SessionDialer {
	ce.convMx.RLock()
	defer ce.convMx.RUnlock()
	return ce.conf.SessionDialer
}

// AcceptRelaySession runs one attached peer's relay session over conn until
// the peer hangs up, ctx is canceled, or the client closes; it blocks for the
// session's lifetime, so callers run it in the accept loop's goroutine. The
// client answers the peer's Noise XK handshake as the server side, learns the
// peer's key from it, and asks allow (if non-nil) whether to keep the peer;
// then it serves the yamux session the way a dmsg server serves a client:
// each stream request is bridged to another peer attached here, or forwarded
// over one of this client's own server sessions, charged to
// Config.MaxRelayedStreams. Newest-session-wins per peer key, like servers.
//
// conn is closed on every return.
func (ce *Client) AcceptRelaySession(ctx context.Context, conn net.Conn, allow func(cipher.PubKey) bool) error {
	ss, err := makeServerSession(metrics.NewEmpty(), &ce.EntityCommon, conn)
	if err != nil {
		_ = conn.Close() //nolint:errcheck
		return err
	}
	pk := ss.RemotePK()
	log := ce.log.WithField("relay_peer", pk)
	if allow != nil && !allow(pk) {
		_ = conn.Close() //nolint:errcheck
		log.Debug("Refused relay session.")
		return ErrRelayPeerNotAllowed
	}
	ss.relayInbound = true

	ss.sm.mutx.Lock()
	ss.sm.yamux, err = yamux.Server(conn, YamuxConfig())
	if err != nil {
		ss.sm.mutx.Unlock()
		_ = conn.Close() //nolint:errcheck
		return err
	}
	ss.sm.addr = ss.sm.yamux.RemoteAddr()
	ss.sm.mutx.Unlock()

	ce.relaySessionsMx.Lock()
	if isClosed(ce.done) {
		ce.relaySessionsMx.Unlock()
		_ = ss.Close() //nolint:errcheck
		return errors.New("client closed")
	}
	old, hadOld := ce.relaySessions[pk]
	ce.relaySessions[pk] = ss.SessionCommon
	ce.relaySessionsMx.Unlock()
	if hadOld && old != ss.SessionCommon {
		_ = old.Close() //nolint:errcheck
	}

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = ss.Close() //nolint:errcheck
		case <-ce.done:
			_ = ss.Close() //nolint:errcheck
		case <-done:
		}
	}()

	log.Info("Started relay session.")
	ss.Serve()
	close(done)
	log.Info("Stopped relay session.")

	// Identity-checked: a fresh session for the same peer may already have
	// replaced this one.
	ce.relaySessionsMx.Lock()
	if ce.relaySessions[pk] == ss.SessionCommon {
		delete(ce.relaySessions, pk)
	}
	ce.relaySessionsMx.Unlock()
	return nil
}

// RelaySessions lists the peers currently attached to this client as a relay.
func (ce *Client) RelaySessions() []cipher.PubKey {
	ce.relaySessionsMx.Lock()
	defer ce.relaySessionsMx.Unlock()
	out := make([]cipher.PubKey, 0, len(ce.relaySessions))
	for pk := range ce.relaySessions {
		out = append(out, pk)
	}
	return out
}

// RelayedStreams reports how many relayed streams this client is carrying.
func (ce *Client) RelayedStreams() int {
	return int(atomic.LoadInt64(&ce.relayedStreams))
}

func (ce *Client) relaySession(pk cipher.PubKey) (*SessionCommon, bool) {
	ce.relaySessionsMx.Lock()
	defer ce.relaySessionsMx.Unlock()
	ses, ok := ce.relaySessions[pk]
	return ses, ok
}

func (ce *Client) closeRelaySessions() {
	ce.relaySessionsMx.Lock()
	sessions := ce.relaySessions
	ce.relaySessions = make(map[cipher.PubKey]*SessionCommon)
	ce.relaySessionsMx.Unlock()
	for _, ses := range sessions {
		_ = ses.Close() //nolint:errcheck
	}
}

// relayForwardSessions orders this client's server sessions for forwarding a
// relayed request to dst: sessions to dst's delegated servers first (by
// latency), then every other session (by latency) — the same order DialStream
// uses for its own dials. Sessions that are themselves relay attachments
// (skynet carrier) are skipped: a relay does not chain through a relay.
func (ce *Client) relayForwardSessions(dst cipher.PubKey) []*SessionCommon {
	var delegated []cipher.PubKey
	ctx, cancel := context.WithTimeout(context.Background(), relayEntryLookupTimeout)
	defer cancel()
	if entry, err := ce.getClientEntryCached(ctx, dst); err == nil && entry != nil && entry.Client != nil {
		delegated = entry.Client.DelegatedServers
	}
	ordered := append(ce.sortedDelegatedSessions(delegated), ce.sortedMeshSessions(delegated)...)
	out := make([]*SessionCommon, 0, len(ordered))
	// The server that last reached dst — from this client's own dials or an
	// earlier forward — goes first. The deployment services have no client
	// entry, so without it every relayed request walked the mesh in latency
	// order and burned a handshake timeout on each server that did not hold
	// the service.
	if cached, ok := ce.getCachedRoute(dst); ok && !ce.relayForwardFailed(dst, cached) {
		if ses, ok := ce.session(cached); ok && ses.carrier != CarrierSkynet {
			out = append(out, ses)
		}
	}
	// Peers that recently failed for dst go last: during the #4703 rollout a
	// server without it lets a relayed request hit the handshake timeout.
	var failed []*SessionCommon
	for _, s := range ordered {
		if s.carrier == CarrierSkynet || (len(out) > 0 && s.SessionCommon == out[0]) {
			continue
		}
		if ce.relayForwardFailed(dst, s.RemotePK()) {
			failed = append(failed, s.SessionCommon)
			continue
		}
		out = append(out, s.SessionCommon)
	}
	return append(out, failed...)
}

// relayFailureBackoff is how long a nominated relay that refused or failed a
// session dial is left alone before the serve loop tries it again. It is the
// capability negotiation for relays: a peer without a relay acceptor, or one
// that will not admit us, simply fails the dial, and we stop asking for a while.
const relayFailureBackoff = 5 * time.Minute

// relayProbation is how long a fresh relay session must survive before its
// loss counts as an ordinary disconnect rather than a refusal: an acceptor
// that will not admit us can only say so after the Noise handshake has
// proven our key, by closing the conn.
const relayProbation = 10 * time.Second

// errRelayNominated is the sentinel the serve loop is woken with when new
// relay candidates arrive while it is parked waiting for a session to fail.
var errRelayNominated = errors.New("dmsg: relay candidates changed")

// SetRelayPeers nominates the peers this client should hold a dmsg session
// through as RELAYS, at the given dmsg port, replacing any earlier nomination.
// The visor calls it as its transports change: a nominee is a peer it has a
// live skywire transport to and a reason to trust with its dmsg traffic (a
// hypervisor, a pinned persistent peer). The serve loop dials nominees FIRST
// and treats "no relay session while nominees exist" as one session short, so
// a client already at MinSessions still attaches; a nominee that refuses is
// backed off for relayFailureBackoff. Stream dials prefer a relay session
// (see DialStream). Passing an empty set withdraws every nomination; existing
// relay sessions are left to close on their own.
// It reports whether the nomination changed.
func (ce *Client) SetRelayPeers(pks []cipher.PubKey, port uint16) bool {
	ce.relayMx.Lock()
	changed := len(pks) != len(ce.relayPeers) || port != ce.relayPort
	next := make(map[cipher.PubKey]struct{}, len(pks))
	for _, pk := range pks {
		if pk.Null() || pk == ce.pk {
			continue
		}
		next[pk] = struct{}{}
		if _, had := ce.relayPeers[pk]; !had {
			changed = true
		}
	}
	ce.relayPeers = next
	ce.relayPort = port
	if changed {
		ce.relayGen++
	}
	ce.relayMx.Unlock()
	// An unchanged set still wakes the loop while a nominee is pending: a
	// dialable nominee with no session, i.e. one whose failure backoff has
	// lapsed. Nothing else re-runs the pass when a backoff expires — the loop
	// is parked on errCh — so the visor's periodic re-nomination is the retry.
	if !changed && !ce.relayPending() {
		return false
	}
	if !changed {
		ce.relayMx.Lock()
		ce.relayGen++
		ce.relayMx.Unlock()
	}
	// Wake a serve loop parked on errCh so it re-evaluates the candidate list.
	ce.sesMx.Lock()
	if !isClosed(ce.done) {
		select {
		case ce.errCh <- errRelayNominated:
		default:
		}
	}
	ce.sesMx.Unlock()
	return changed
}

// relayPending reports whether a nominee is dialable right now (not backed
// off) while no relay session exists.
func (ce *Client) relayPending() bool {
	return len(ce.relayEntries()) > 0 && !ce.hasRelaySession()
}

// relayGeneration is the nomination change counter (see SetRelayPeers).
func (ce *Client) relayGeneration() uint64 {
	ce.relayMx.Lock()
	defer ce.relayMx.Unlock()
	return ce.relayGen
}

// RelayPeers returns the current relay nominees.
func (ce *Client) RelayPeers() []cipher.PubKey {
	ce.relayMx.Lock()
	defer ce.relayMx.Unlock()
	out := make([]cipher.PubKey, 0, len(ce.relayPeers))
	for pk := range ce.relayPeers {
		out = append(out, pk)
	}
	return out
}

// relayEntries returns server entries for the nominated relays this client has
// no session with and is not backing off, at their skynet address.
func (ce *Client) relayEntries() []*disc.Entry {
	ce.relayMx.Lock()
	defer ce.relayMx.Unlock()
	if len(ce.relayPeers) == 0 {
		return nil
	}
	now := time.Now()
	out := make([]*disc.Entry, 0, len(ce.relayPeers))
	for pk := range ce.relayPeers {
		if _, ok := ce.session(pk); ok {
			continue
		}
		if until, ok := ce.relayFailAt[pk]; ok && now.Before(until) {
			continue
		}
		out = append(out, &disc.Entry{
			Static: pk,
			Server: &disc.Server{Address: SkynetAddr(pk, ce.relayPort), AvailableSessions: 1},
		})
	}
	return out
}

// isRelayPeer reports whether pk is a current relay nominee.
func (ce *Client) isRelayPeer(pk cipher.PubKey) bool {
	ce.relayMx.Lock()
	defer ce.relayMx.Unlock()
	_, ok := ce.relayPeers[pk]
	return ok
}

// relayTimeoutRetry is the backoff after a nominee dial that merely timed out:
// at boot the skynet dial races the router's own cold start (setup node,
// route finder), so a timeout says nothing about the nominee. Refusals and
// other errors get relayFailureBackoff.
const relayTimeoutRetry = 30 * time.Second

// relayBackoffFor picks the backoff a failed nominee dial earns.
func relayBackoffFor(err error) time.Duration {
	var nerr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &nerr) && nerr.Timeout()) {
		return relayTimeoutRetry
	}
	return relayFailureBackoff
}

// noteRelayFailure backs a nominee off for d after a failed session dial.
func (ce *Client) noteRelayFailure(pk cipher.PubKey, d time.Duration) {
	ce.relayMx.Lock()
	if ce.relayFailAt == nil {
		ce.relayFailAt = make(map[cipher.PubKey]time.Time)
	}
	ce.relayFailAt[pk] = time.Now().Add(d)
	ce.relayMx.Unlock()
}

// hasRelaySession reports whether any current session rides the skynet carrier.
func (ce *Client) hasRelaySession() bool {
	ce.sessionsMx.Lock()
	defer ce.sessionsMx.Unlock()
	for _, ses := range ce.sessions {
		if ses.carrier == CarrierSkynet {
			return true
		}
	}
	return false
}

// sessionsSatisfied is the serve loop's "enough sessions" test: MinSessions
// met, and — when relays are nominated and dialable — at least one relay
// session held. Only meaningful when MinSessions != 0.
func (ce *Client) sessionsSatisfied() bool {
	if ce.SessionCount() < ce.conf.MinSessions {
		return false
	}
	if len(ce.relayEntries()) > 0 && !ce.hasRelaySession() {
		return false
	}
	return true
}

// sortedRelaySessions returns the sessions riding the skynet carrier, by
// latency. DialStream tries these before any server session: a relay is the
// path the visor chose for this client's dmsg traffic.
func (ce *Client) sortedRelaySessions() []ClientSession {
	var out []ClientSession
	for _, ses := range ce.allClientSessions(ce.porter) {
		if ses.carrier == CarrierSkynet {
			out = append(out, ses)
		}
	}
	sortSessionsByLatency(out)
	return out
}

// relayBackedOff reports whether nominee pk is inside its failure backoff.
func (ce *Client) relayBackedOff(pk cipher.PubKey) bool {
	ce.relayMx.Lock()
	defer ce.relayMx.Unlock()
	until, ok := ce.relayFailAt[pk]
	return ok && time.Now().Before(until)
}

// relayForwardFailureTTL is how long a (destination, peer) pair that failed a
// relayed forward is demoted to the end of the relay's forwarding order.
const relayForwardFailureTTL = 2 * time.Minute

// relayDialSkipTTL is how long a dialer leaves the relay out of the ladder for
// a destination the relay just failed to reach, so the server phases are not
// delayed by a relay attempt that will fail the same way.
const relayDialSkipTTL = time.Minute

type dstPeer struct{ dst, peer cipher.PubKey }

// noteRelayForwardFailure records that peer could not carry a relayed request
// to dst (relay side), and evicts a cached route that pointed at it.
func (ce *Client) noteRelayForwardFailure(dst, peer cipher.PubKey) {
	ce.relayMx.Lock()
	if ce.relayForwardBad == nil {
		ce.relayForwardBad = make(map[dstPeer]time.Time)
	}
	ce.relayForwardBad[dstPeer{dst, peer}] = time.Now().Add(relayForwardFailureTTL)
	ce.relayMx.Unlock()
	if cached, ok := ce.getCachedRoute(dst); ok && cached == peer {
		ce.evictCachedRoute(dst)
	}
}

// relayForwardFailed reports whether peer recently failed to carry a relayed
// request to dst.
func (ce *Client) relayForwardFailed(dst, peer cipher.PubKey) bool {
	ce.relayMx.Lock()
	defer ce.relayMx.Unlock()
	until, ok := ce.relayForwardBad[dstPeer{dst, peer}]
	return ok && time.Now().Before(until)
}

// noteRelayDialFailure records (dialer side) that the relay could not reach dst.
func (ce *Client) noteRelayDialFailure(dst cipher.PubKey) {
	ce.relayMx.Lock()
	if ce.relayDialSkip == nil {
		ce.relayDialSkip = make(map[cipher.PubKey]time.Time)
	}
	ce.relayDialSkip[dst] = time.Now().Add(relayDialSkipTTL)
	ce.relayMx.Unlock()
}

// relayDialSkipped reports whether dials to dst currently bypass the relay.
func (ce *Client) relayDialSkipped(dst cipher.PubKey) bool {
	ce.relayMx.Lock()
	defer ce.relayMx.Unlock()
	until, ok := ce.relayDialSkip[dst]
	return ok && time.Now().Before(until)
}
