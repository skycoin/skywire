// Package dmsg pkg/dmsg/client_sessions.go
package dmsg

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/quic-go/quic-go"
	"github.com/xtaci/smux"
	"golang.org/x/net/proxy"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/skyquic"
)

// EnsureAndObtainSession attempts to obtain a session.
// If the session does not exist, we will attempt to establish one.
// It returns an error if the session does not exist AND cannot be established.
//
// sesMx is released across the discovery lookup (getServerEntry) because
// ce.dc may be a dmsgfirst-wrapped client whose primary path dials over
// DMSG itself — DialStream → phase 3 → EnsureAndObtainSession on the
// same goroutine, which would re-acquire sesMx and self-deadlock on a
// non-reentrant mutex. Holding the lock only during the cache check and
// the dialSession step still serializes concurrent dials to the same
// server, preserving the invariant the lock was protecting.
//
// We also resolve the server entry from the local entryCache first when
// possible. If we go to ce.dc here, the dmsgfirst wrapper's primary
// path is dmsghttp — which is itself a DialStream that lands back in
// EnsureAndObtainSession to set up its session, calls getServerEntry,
// and recurses into the same disc.Entry call until the goroutine stack
// blows up. The cache short-circuit removes that loop entirely for any
// server PK we already know about (configured servers are pre-seeded by
// dmsgc.New, so the common case never makes a network lookup; runtime-
// learned servers fall through to the slower disc-client path which
// only recurses if we genuinely don't have the entry yet — and even
// then, the recursion bottoms out as soon as one configured server's
// session is dialed and seeds itself).
func (ce *Client) EnsureAndObtainSession(ctx context.Context, srvPK cipher.PubKey) (ClientSession, error) {
	ce.sesMx.Lock()
	if dSes, ok := ce.clientSession(ce.porter, srvPK); ok {
		ce.sesMx.Unlock()
		return dSes, nil
	}
	ce.sesMx.Unlock()

	srvEntry, err := ce.resolveServerEntry(ctx, srvPK)
	if err != nil {
		return ClientSession{}, err
	}

	ce.sesMx.Lock()
	// Re-check after re-locking — another goroutine may have raced
	// us to establish a session for the same server PK while we
	// were doing the discovery lookup. Use it instead of dialing
	// a duplicate.
	if dSes, ok := ce.clientSession(ce.porter, srvPK); ok {
		ce.sesMx.Unlock()
		return dSes, nil
	}
	ce.sesMx.Unlock()
	// Dial WITHOUT holding sesMx. With single-client self-hosted discovery
	// (the client's own dmsg-HTTP transport carries discovery lookups),
	// dialSession's session-serve / entry-registration path can re-enter
	// EnsureAndObtainSession through this same client. Holding sesMx here
	// would re-lock this non-reentrant mutex on the same goroutine and
	// deadlock the whole dmsg client — a fatal, fleet-wide wedge.
	// dialSession's sessionsMx insertion is newest-session-wins, so a
	// racing duplicate dial is replaced (and closed), not leaked.
	return ce.dialSession(ctx, srvEntry)
}

// EnsureSession ensures the existence of a session.
// It returns an error if the session does not exist AND cannot be established.
func (ce *Client) EnsureSession(ctx context.Context, entry *disc.Entry) error {
	ce.sesMx.Lock()
	// If session with server of pk already exists, skip.
	if _, ok := ce.clientSession(ce.porter, entry.Static); ok {
		ce.sesMx.Unlock()
		ce.log.WithField("remote_pk", entry.Static).Debug("Session already exists...")
		return nil
	}
	ce.sesMx.Unlock()
	// Work on a shallow copy: callers can share one configured *disc.Entry
	// across concurrent EnsureSession calls (dmsg-disc's connectConfiguredServers
	// + updateServers both iterate the same preloaded server slice), so
	// mutating entry.Protocol in place is a data race against those readers.
	// The shallow copy shares the read-only Server pointer, which is fine.
	e := *entry
	e.Protocol = ce.conf.Protocol
	// Dial WITHOUT holding sesMx — same re-entrancy hazard as
	// EnsureAndObtainSession: the dial path can re-enter session
	// establishment via the self-hosted transport, which would
	// self-deadlock on the non-reentrant sesMx.
	_, err := ce.dialSession(ctx, &e)
	return err
}

// It is expected that the session is created and served before the context cancels, otherwise an error will be returned.
// NOTE: This should not be called directly as it may lead to session duplicates.
// Only `ensureSession` or `EnsureAndObtainSession` should call this function.
func (ce *Client) dialSession(ctx context.Context, entry *disc.Entry) (cs ClientSession, err error) {
	ce.log.WithField("remote_pk", entry.Static).Debug("Dialing session...")

	// Pick transport: QUIC when the server advertises a QUIC endpoint +
	// Protocol "quic" (#2607 dmsg-over-QUIC), else the legacy TCP path.
	network := "tcp"
	dialAddr := entry.Server.Address
	if entry.Protocol == "quic" && entry.Server.AddressUDP != "" {
		network = "quic"
		dialAddr = entry.Server.AddressUDP
	}
	var dSes ClientSession

	// Trigger dial callback.
	if err := ce.conf.Callbacks.OnSessionDial(network, dialAddr); err != nil {
		return ClientSession{}, fmt.Errorf("session dial is rejected by callback: %w", err)
	}
	defer func() {
		if err != nil {
			// Trigger disconnect callback when dial fails.
			ce.conf.Callbacks.OnSessionDisconnect(network, dialAddr, err)
		}
	}()

	if network == "quic" {
		if dSes, err = ce.dialSessionQUIC(ctx, entry); err != nil {
			// QUIC dial failed (e.g. UDP blocked by a firewall). Fall back to
			// the server's TCP endpoint, which a QUIC-advertising server also
			// listens on (dual-listen). Only give up if there is no TCP address.
			if entry.Server.Address == "" {
				return ClientSession{}, err
			}
			ce.log.WithError(err).Debugf("QUIC dial to %s failed, falling back to TCP", entry.Static)
			network = "tcp"
			dialAddr = entry.Server.Address
		} else {
			ce.log.Infof("quic stream session initial for %s", dSes.RemotePK().String())
		}
	}
	if network == "tcp" {
		var conn net.Conn
		proxyAddr, ok := ctx.Value("socks5_proxy").(string)
		if ok && proxyAddr != "" {
			socksDialer, err := proxy.SOCKS5("tcp", proxyAddr, nil, proxy.Direct)
			if err != nil {
				return ClientSession{}, fmt.Errorf("failed to create SOCKS5 dialer: %w", err)
			}
			conn, err = socksDialer.Dial(network, dialAddr)
			if err != nil {
				return ClientSession{}, fmt.Errorf("failed to dial through SOCKS5 proxy: %w", err)
			}
		} else {
			conn, err = net.DialTimeout(network, dialAddr, DialTimeout)
			if err != nil {
				return ClientSession{}, fmt.Errorf("failed to dial: %w", err)
			}
		}
		// TCP_NODELAY on the visor→dmsg-server TCP socket. The dmsg
		// session carries all dmsg streams (skypty traffic, dmsg-HTTP,
		// visor-RPC over dmsg, skychat, etc.). Without this, Nagle's
		// algorithm batches small writes — per-keystroke pty bytes
		// from the hypervisor UI's skypty hit 40–200ms of delay each,
		// felt as lag on every key press. dmsg streams demux many
		// logical flows onto one TCP conn; the demux-side throughput
		// gains of Nagle aren't material for skywire's mix, and the
		// interactive-latency loss is severe.
		if tcpConn, ok := conn.(*net.TCPConn); ok {
			_ = tcpConn.SetNoDelay(true) //nolint:errcheck
		}

		if dSes, err = makeClientSession(&ce.EntityCommon, ce.porter, conn, entry.Static); err != nil {
			conn.Close() //nolint:errcheck,gosec
			return ClientSession{}, err
		}
		if entry.Protocol == "smux" {
			dSes.sm.smux, err = smux.Client(conn, SmuxConfig())
			if err != nil {
				conn.Close() //nolint:errcheck,gosec
				return ClientSession{}, err
			}
			ce.log.Infof("smux stream session initial for %s", dSes.RemotePK().String())
		} else {
			dSes.sm.yamux, err = yamux.Client(conn, YamuxConfig())
			if err != nil {
				conn.Close() //nolint:errcheck,gosec
				return ClientSession{}, err
			}
			ce.log.Infof("yamux stream session initial for %s", dSes.RemotePK().String())
		}
	}

	// Atomically: refuse-if-closed, replace-stale, store, and wg.Add(1)
	// under sessionsMx — pairs with Close which sets ce.closed under the
	// same lock before wg.Wait. The Add must happen-before any subsequent
	// Wait or sync.WaitGroup races (TestEnv race: Add at 159 vs Wait at
	// client.go:510). The setSessionCallback is invoked outside the lock
	// to match the previous setSession semantics.
	//
	// Newest-session-wins: if a session for this server PK already exists
	// it is almost certainly a stale half-dead session (the link died
	// without a clean close, so its serve loop is still parked in
	// AcceptStream and never freed the map slot). The old code REJECTED the
	// reconnect and kept the corpse — the visor stayed "connected" to a
	// server that could no longer carry an inbound bridged stream, so other
	// visors dialing through it hung for HandshakeTimeout and multihop route
	// setup starved on "dmsg error 202". Replace the predecessor and close
	// it; its serve goroutine then unwinds and calls delSession, which is
	// identity-checked so it cannot evict this live replacement.
	ce.sessionsMx.Lock()
	if ce.closed {
		ce.sessionsMx.Unlock()
		_ = dSes.Close() //nolint:errcheck
		return ClientSession{}, errors.New("client closed")
	}
	old, hadOld := ce.sessions[dSes.RemotePK()]
	ce.sessions[dSes.RemotePK()] = dSes.SessionCommon
	ce.wg.Add(1)
	setSessCb := ce.setSessionCallback
	ce.sessionsMx.Unlock()

	if hadOld && old != dSes.SessionCommon {
		// Close the stale predecessor outside the lock (Close does IO).
		_ = old.Close() //nolint:errcheck
	}

	if setSessCb != nil {
		if err := setSessCb(ctx); err != nil {
			ce.log.
				WithField("func", "Client.dialSession").
				WithError(err).
				Warn("setSessionCallback returned non-nil error.")
		}
	}

	go func() {
		defer ce.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				ce.log.Warnf("recovered panic in session serve goroutine: %v", r)
			}
		}()
		ce.log.WithField("remote_pk", dSes.RemotePK()).Debug("Serving session.")
		err := dSes.serve()
		// Hold sesMx across the done-check AND the errCh send so it is atomic
		// with Close()'s sesMx-guarded close(ce.errCh): either we send before
		// errCh is closed, or we observe done closed and skip the send.
		// Checking done unlocked (the prior code) raced Close -> send on a
		// closed channel -> recovered panic + skipped delSession/disconnect.
		ce.sesMx.Lock()
		if isClosed(ce.done) {
			ce.sesMx.Unlock()
		} else {
			select {
			case ce.errCh <- fmt.Errorf("failed to serve dialed session to %s: %v", dSes.RemotePK(), err):
			default:
			}
			ce.sesMx.Unlock()
			// Identity-checked: a newer reconnect may have already
			// replaced this session in the map (newest-session-wins);
			// deleting by PK alone would evict that live successor.
			ce.delSession(ctx, dSes.RemotePK(), dSes.SessionCommon)
		}

		// Trigger disconnect callback.
		ce.conf.Callbacks.OnSessionDisconnect(network, dialAddr, err)
	}()

	return dSes, nil
}

// dialSessionQUIC dials a dmsg server's QUIC endpoint and builds a QUIC-backed
// client session (#2607 dmsg-over-QUIC). The PK-bound QUIC TLS (pkg/skyquic,
// option A) authenticates the server's skywire PK and encrypts the hop; the
// session runs over native QUIC streams with no Noise handshake. EnableDatagrams
// so the session can later expose an unreliable datagram channel.
func (ce *Client) dialSessionQUIC(ctx context.Context, entry *disc.Entry) (ClientSession, error) {
	udpAddr, err := net.ResolveUDPAddr("udp", entry.Server.AddressUDP)
	if err != nil {
		return ClientSession{}, fmt.Errorf("quic: resolve %q: %w", entry.Server.AddressUDP, err)
	}
	udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return ClientSession{}, fmt.Errorf("quic: bind dial socket: %w", err)
	}
	cert, err := skyquic.NewCertificate(ce.pk, ce.sk)
	if err != nil {
		udpConn.Close() //nolint:errcheck,gosec
		return ClientSession{}, fmt.Errorf("quic: identity cert: %w", err)
	}
	rPK := entry.Static
	tlsConf := skyquic.TLSConfig(cert, &rPK, nil) // pin the server PK
	qc, err := quic.Dial(ctx, udpConn, udpAddr, tlsConf, &quic.Config{
		EnableDatagrams: true,
		KeepAlivePeriod: 25 * time.Second,
		MaxIdleTimeout:  60 * time.Second,
	})
	if err != nil {
		udpConn.Close() //nolint:errcheck,gosec
		return ClientSession{}, fmt.Errorf("quic: dial %s: %w", entry.Server.AddressUDP, err)
	}
	return makeClientSessionQUIC(&ce.EntityCommon, ce.porter, qc, rPK), nil
}

// Session obtains an established session.
func (ce *Client) Session(pk cipher.PubKey) (ClientSession, bool) {
	return ce.clientSession(ce.porter, pk)
}
