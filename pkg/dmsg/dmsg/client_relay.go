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
	for _, s := range ordered {
		if s.carrier == CarrierSkynet {
			continue
		}
		out = append(out, s.SessionCommon)
	}
	return out
}
