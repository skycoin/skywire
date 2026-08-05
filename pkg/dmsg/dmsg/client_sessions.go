// Package dmsg pkg/dmsg/dmsg/client_sessions.go c1-net-dmsg
package dmsg

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/hashicorp/yamux"
	"github.com/xtaci/smux"
	"golang.org/x/net/proxy"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
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
// pickCarrier chooses the dmsg carrier (network + dial address) for a server
// entry from an ordered carrier preference. The first listed carrier the server
// advertises wins. Empty list or no match falls back to the default: QUIC when
// the server advertises a QUIC endpoint (Protocol "quic" + AddressUDP), else the
// legacy TCP path. Unknown carrier names are skipped. Pure — unit-tested.
// hasCarrier reports whether c is in the client's ordered carrier preference.
func hasCarrier(carriers []string, c string) bool {
	for _, x := range carriers {
		if x == c {
			return true
		}
	}
	return false
}

// prefersWTOverWS reports whether the carrier preference lists WebTransport
// ahead of WebSocket — the browser-bootstrap case where a wss session should
// be dropped once a WT session exists. Native clients (no WT, or WS-less) → false.
func prefersWTOverWS(carriers []string) bool {
	wt, ws := -1, -1
	for i, c := range carriers {
		switch c {
		case CarrierWT:
			if wt < 0 {
				wt = i
			}
		case CarrierWS:
			if ws < 0 {
				ws = i
			}
		}
	}
	return wt >= 0 && ws >= 0 && wt < ws
}

// UpgradeBrowserSessions closes WebSocket (wss) sessions once at least one
// WebTransport session is live, so a browser that BOOTSTRAPPED over wss (the
// only carrier a browser can use before discovery is reachable) converges to
// WebTransport — dropping the redundant TLS-over-Noise of wss. It is safe to
// call repeatedly (e.g. on a timer): when a wss session is closed, the Serve
// loop re-dials to MinSessions, preferring WT (Carriers=[wt,ws]).
//
// Conservative by construction so it can never strand the client:
//   - a no-op unless the client prefers WT over WS (native clients skip it);
//   - only acts when a WT session already exists;
//   - never drops the session count below one.
//
// Returns the number of wss sessions closed.
func (ce *Client) UpgradeBrowserSessions() int {
	if !prefersWTOverWS(ce.conf.Carriers) {
		return 0
	}
	ce.sessionsMx.Lock()
	var hasWT bool
	var wsSessions []*SessionCommon
	for _, s := range ce.sessions {
		switch s.carrier {
		case CarrierWT:
			hasWT = true
		case CarrierWS:
			// Only converge wss → WT for servers that actually advertise WT. A wss
			// to a non-WT server has no WT to converge to — dropping it just re-dials
			// wss next tick (the churn when only a few of N servers advertise WT).
			if s.wtCapable {
				wsSessions = append(wsSessions, s)
			}
		}
	}
	remaining := len(ce.sessions)
	ce.sessionsMx.Unlock()

	if !hasWT {
		return 0
	}
	closed := 0
	for _, s := range wsSessions {
		if remaining <= 1 {
			break // never strand the client
		}
		ce.log.WithField("remote_pk", s.RemotePK()).Debug("Dropping wss session — WebTransport is up; converging off the redundant TLS layer.")
		_ = s.Close() //nolint:errcheck // serve loop unwinds → delSession → Serve re-dials WT-preferred
		remaining--
		closed++
	}
	return closed
}

func pickCarrier(carriers []string, entry *disc.Entry) (network, addr string) {
	for _, c := range carriers {
		switch c {
		case CarrierWT:
			if entry.Server.AddressWT != "" {
				return CarrierWT, entry.Server.AddressWT
			}
		case CarrierWS:
			if entry.Server.AddressWS != "" {
				return CarrierWS, entry.Server.AddressWS
			}
		case CarrierQUIC:
			if entry.Protocol == "quic" && entry.Server.AddressUDP != "" {
				return CarrierQUIC, entry.Server.AddressUDP
			}
		case CarrierTCP:
			return CarrierTCP, entry.Server.Address
		}
	}
	// No configured carrier matched the server's advertised endpoints. Fall back
	// to a default ONLY to a carrier this client can actually dial. Empty Carriers
	// means "native default" (the historical QUIC-then-TCP behavior); a wss-only
	// browser client (Carriers=[WT,WS]) can dial neither raw QUIC nor TCP, so it
	// gets no carrier ("") and the caller surfaces a clear unreachable error
	// instead of a futile `dial tcp`/`dial udp`.
	nativeDefault := len(carriers) == 0
	if entry.Protocol == "quic" && entry.Server.AddressUDP != "" && (nativeDefault || hasCarrier(carriers, CarrierQUIC)) {
		return CarrierQUIC, entry.Server.AddressUDP
	}
	if nativeDefault || hasCarrier(carriers, CarrierTCP) {
		return CarrierTCP, entry.Server.Address
	}
	return "", ""
}

// ProtocolLabel renders a human-readable label for the protocol a client used
// to reach a dmsg server, given the carrier it dialed and that carrier's
// endpoint address. Every dmsg carrier is Noise-encrypted end to end above the
// byte pipe; the label names the underlying transport, and for the WebSocket
// carrier it distinguishes plain ws:// from TLS-secured wss:// by the endpoint
// scheme. An empty carrier (an accepted, server-side session) renders as
// "accepted".
func ProtocolLabel(carrier, addr string) string {
	switch carrier {
	case CarrierTCP:
		return "tcp"
	case CarrierWS:
		if isSecureURL(addr) {
			return "wss"
		}
		return "ws"
	case CarrierWT:
		return "webtransport"
	case CarrierQUIC:
		return "quic"
	case "":
		return "accepted"
	default:
		return carrier
	}
}

// isSecureURL reports whether addr is a TLS-secured websocket / https endpoint
// (wss:// or https://), used to distinguish wss from plain ws.
func isSecureURL(addr string) bool {
	a := strings.ToLower(strings.TrimSpace(addr))
	return strings.HasPrefix(a, "wss://") || strings.HasPrefix(a, "https://")
}

func (ce *Client) dialSession(ctx context.Context, entry *disc.Entry) (cs ClientSession, err error) {
	ce.log.WithField("remote_pk", entry.Static).Debug("Dialing session...")

	// Pick the carrier from the client's ordered Carriers preference: the first
	// listed carrier the server advertises wins; empty/no-match falls back to the
	// default (QUIC when advertised, else TCP). The chosen carrier is dialed below
	// with a TCP fallback on failure.
	network, dialAddr := pickCarrier(ce.conf.Carriers, entry)
	if network == "" {
		// The server advertises no endpoint this client can dial (e.g. a browser,
		// which can only do wss/WebTransport, reaching a server whose entry carries
		// no AddressWS). Surface a clear error so the rendezvous tries another of
		// the peer's delegated servers rather than a futile raw dial.
		return ClientSession{}, fmt.Errorf("dmsg: no carrier for server %s dialable by this client (carriers=%v)", entry.Static, ce.conf.Carriers)
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

	if network == "wt" {
		if dSes, err = ce.dialSessionWT(ctx, entry); err != nil {
			// WT dial failed. Prefer a WS fallback when this client allows WS and
			// the server advertises it — a browser (which prefers WT) has no TCP
			// fallback, so without this a single broken WT listener would strand it
			// even though the server also offers WS. Else fall back to TCP (native
			// tooling on a network that permits it). The ws/tcp blocks below run
			// when network is reassigned here.
			switch {
			case hasCarrier(ce.conf.Carriers, CarrierWS) && entry.Server.AddressWS != "":
				ce.log.WithError(err).Debugf("WT dial to %s failed, falling back to WS", entry.Static)
				network = CarrierWS
			case entry.Server.Address != "" && hasCarrier(ce.conf.Carriers, CarrierTCP):
				ce.log.WithError(err).Debugf("WT dial to %s failed, falling back to TCP", entry.Static)
				network = "tcp"
				dialAddr = entry.Server.Address
			default:
				return ClientSession{}, err
			}
		} else {
			ce.log.Infof("wt stream session initial for %s", dSes.RemotePK().String())
		}
	}
	if network == "ws" {
		if dSes, err = ce.dialSessionWS(ctx, entry); err != nil {
			// WS dial failed. Fall back to the server's TCP endpoint only when this
			// client is actually configured to dial TCP. Gating on Address=="" was
			// wrong: it assumed the js/wasm build never has a TCP address, but
			// DISCOVERY-resolved server entries DO carry one — so a browser
			// (Carriers=[WT,WS], no TCP) would fall back to a raw `dial tcp` it can
			// never satisfy ("connection refused"), instead of surfacing the WS
			// error so the rendezvous moves on to another of the peer's servers.
			if entry.Server.Address == "" || !hasCarrier(ce.conf.Carriers, CarrierTCP) {
				return ClientSession{}, err
			}
			ce.log.WithError(err).Debugf("WS dial to %s failed, falling back to TCP", entry.Static)
			network = "tcp"
			dialAddr = entry.Server.Address
		} else {
			ce.log.Infof("ws stream session initial for %s", dSes.RemotePK().String())
		}
	}
	if network == "quic" {
		if dSes, err = ce.dialSessionQUIC(ctx, entry); err != nil {
			// QUIC dial failed (e.g. UDP blocked by a firewall). Fall back to
			// the server's TCP endpoint, which a QUIC-advertising server also
			// listens on (dual-listen). Only if this client can actually dial TCP
			// (a browser can't) and the server has a TCP address.
			if entry.Server.Address == "" || !hasCarrier(ce.conf.Carriers, CarrierTCP) {
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
			setTCPNoDelay(tcpConn)
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
	// Record the carrier actually used (after any WT→WS / *→TCP fallback above)
	// so Client.UpgradeBrowserSessions can later converge wss → WebTransport.
	// carrierAddr is the endpoint that carrier dialed, so callers can tell the
	// exact protocol used to reach this server (and ws:// from wss://).
	dSes.carrier = network
	dSes.carrierAddr = dialAddr
	// Whether this server can be upgraded wss → WT. Only these should have their
	// bootstrap wss dropped by UpgradeBrowserSessions; a non-WT server's wss just
	// re-dials wss on the next tick, churning the session (the 8/9-servers case).
	dSes.wtCapable = entry.Server.AddressWT != ""

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

// dialSessionQUIC (the QUIC client dial) lives in quic_native.go (//go:build
// !tinygo); the TinyGo build has a stub in quic_stub.go that errors so the dial
// falls back to TCP/WS.

// Session obtains an established session.
func (ce *Client) Session(pk cipher.PubKey) (ClientSession, bool) {
	return ce.clientSession(ce.porter, pk)
}
