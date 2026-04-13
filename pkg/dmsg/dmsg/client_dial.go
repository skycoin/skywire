// Package dmsg pkg/dmsg/client_dial.go
package dmsg

import (
	"context"
	"math"
	"net"
	"sort"
	"time"

	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/netutil"
)

// Listen listens on a given dmsg port.
func (ce *Client) Listen(port uint16) (*Listener, error) {
	lis := newListener(ce.porter, Addr{PK: ce.pk, Port: port})
	ok, doneFn := ce.porter.Reserve(port, lis)
	if !ok {
		lis.close()
		return nil, ErrPortOccupied
	}
	lis.addCloseCallback(doneFn)
	return lis, nil
}

// Dial wraps DialStream to output net.Conn instead of *Stream.
func (ce *Client) Dial(ctx context.Context, addr Addr) (net.Conn, error) {
	return ce.DialStream(ctx, addr)
}

// DialStream dials to a remote client entity with the given address.
func (ce *Client) DialStream(ctx context.Context, addr Addr) (*Stream, error) {
	entry, discErr := ce.getClientEntryCached(ctx, addr.PK)
	if discErr != nil {
		// Discovery lookup failed. For direct clients or when the destination
		// doesn't register in discovery, try all connected servers as forwarders.
		// Only attempt if context has enough remaining time to avoid races.
		if ctx.Err() == nil && ce.hasEnoughTimeForFallback(ctx) {
			ce.log.WithError(discErr).Debug("Discovery lookup failed, trying connected servers")
			// Use a separate context with enough budget for multiple servers.
			// Each server attempt takes up to HandshakeTimeout (5s), so budget
			// for all connected sessions (typically 6) plus a small margin.
			// The old code used a single HandshakeTimeout which only allowed
			// trying the first (lowest latency) server — if the destination's
			// direct client was on a different server, it was never reached.
			fallbackCtx, fallbackCancel := context.WithTimeout(context.Background(), 6*HandshakeTimeout)
			defer fallbackCancel()
			stream, err := ce.dialViaConnectedServers(fallbackCtx, addr)
			if err == nil {
				return stream, nil
			}
		}
		return nil, discErr
	}

	// Phase 0: Try cached route first (server that last successfully reached this destination).
	if cachedSrvPK, ok := ce.getCachedRoute(addr.PK); ok {
		if dSes, ok := ce.clientSession(ce.porter, cachedSrvPK); ok {
			stream, err := dSes.DialStream(ctx, addr)
			if err != nil {
				ce.log.WithError(err).WithField("server", cachedSrvPK).
					Debug("DialStream failed via cached route, evicting")
				ce.evictCachedRoute(addr.PK)
			} else {
				return stream, nil
			}
		} else {
			// Session no longer exists, evict stale route.
			ce.evictCachedRoute(addr.PK)
		}
	}

	// Phase 1 and 2 try existing sessions to the client's already-
	// connected servers. Since PR #2301 auto-releases ephemeral ports
	// on terminal Read/Write errors, a failed dial frees its port
	// immediately — there is no longer a port-exhaustion reason to
	// cap these phases aggressively. We use a generous cap here so
	// small deployments (~6 dmsg servers) try every session before
	// giving up, but large networks still bound the total work.
	//
	// The prior cap of 2 caused persistent route-setup failures when
	// a destination's discovery entry was stale (listed delegated
	// servers it wasn't actually on): phase 1 would hit the listed
	// servers, fail, and phase 2's first two latency-sorted picks
	// would often miss the server the destination was actually on.
	const maxPerExistingPhase = 16
	// Phase 3 (establishing new sessions to delegated servers) is
	// more expensive since each attempt does a full noise handshake,
	// so it still uses the tighter cap.
	const maxPerNewPhase = 2

	// Phase 1: Iterate the target's delegated-server sessions
	// serially. Each attempt is bounded by the stream-level
	// HandshakeTimeout (5s) via SetDeadline, NOT a derived
	// per-attempt ctx — see sequentialPhaseDial's doc and the
	// DialStream watcher comment in client_session.go for why
	// deriving a shorter ctx races the handshake and orphans
	// streams in the destination's accept queue.
	//
	// Why sequential rather than parallel: a parallel race opens N
	// yamux streams on the destination's listener simultaneously, then
	// cancels N-1 losers. The destination's listener accepts the
	// loser streams into its accept queue before they can be
	// canceled, leaving orphan streams that subsequent AcceptStream
	// calls consume — breaking any caller that expects consecutive
	// dials to produce consecutive, usable streams (the exact failure
	// mode of TestMultiServerStreams).
	delegatedSessions := ce.sortedDelegatedSessions(entry.Client.DelegatedServers)
	if stream, ok := ce.sequentialPhaseDial(ctx, addr, delegatedSessions, maxPerExistingPhase); ok {
		return stream, nil
	}

	// Phase 2: Iterate other existing sessions (mesh path — already
	// connected, no new handshake). If servers are meshed, our server
	// forwards the request to the target's server. Sorted by latency.
	// Skips negative-cached pairs.
	meshSessions := ce.sortedMeshSessions(entry.Client.DelegatedServers)
	if stream, ok := ce.sequentialPhaseDial(ctx, addr, meshSessions, maxPerExistingPhase); ok {
		return stream, nil
	}

	// Phase 3: Last resort: establish new sessions to the target's delegated servers.
	for i, srvPK := range entry.Client.DelegatedServers {
		if i >= maxPerNewPhase {
			break
		}
		dSes, err := ce.EnsureAndObtainSession(ctx, srvPK)
		if err != nil {
			continue
		}
		stream, err := dSes.DialStream(ctx, addr)
		if err != nil {
			ce.log.WithError(err).WithField("server", srvPK).
				Debug("DialStream failed via new session, trying next server")
			continue
		}
		ce.setCachedRoute(addr.PK, srvPK)
		return stream, nil
	}

	return nil, ErrCannotConnectToDelegated
}

// getClientEntryCached returns a client entry, using the entry cache when possible.
func (ce *Client) getClientEntryCached(ctx context.Context, clientPK cipher.PubKey) (*disc.Entry, error) {
	if entry, ok := ce.getCachedEntry(clientPK); ok {
		return entry, nil
	}
	entry, err := getClientEntry(ctx, ce.dc, clientPK)
	if err != nil {
		return nil, err
	}
	ce.setCachedEntry(clientPK, entry)
	return entry, nil
}

// sequentialPhaseDial iterates sessions in order. The parent ctx is
// passed straight through to each session.DialStream — the session
// bounds the handshake itself via HandshakeTimeout (5s, set via
// SetDeadline on the underlying stream). Do NOT derive a shorter
// per-attempt context here: the session's DialStream watcher fires
// ctx.Done() mid-handshake, and even though the watcher/defer
// mutex converts that into a clean (nil, err) return, the handshake
// REQUEST has already reached the destination and been introduced
// into its listener accept queue. Closing the dial-side stream
// propagates FIN through the server bridge to the destination,
// leaving a dead stream in the destination's accept buffer that
// subsequent AcceptStream calls dequeue — manifesting as EOF on
// read for unrelated streams. This is the exact failure mode of
// TestConcurrentStreams under -race; see the commit that added
// this comment for the full bisect.
//
// On success it updates the route cache. Returns (stream, true) on
// success, (nil, false) if every attempt failed or the pool was empty.
//
// Sequential rather than parallel because parallel racing opens N
// yamux streams on the destination simultaneously and leaves N-1
// orphan streams in the destination's accept queue after cancellation
// — same failure mode but from a different angle (the exact failure
// mode of TestMultiServerStreams).
func (ce *Client) sequentialPhaseDial(ctx context.Context, addr Addr, sessions []ClientSession, cap int) (*Stream, bool) {
	tried := 0
	for _, s := range sessions {
		if tried >= cap {
			break
		}
		if ctx.Err() != nil {
			return nil, false
		}
		tried++

		stream, err := s.DialStream(ctx, addr)
		if err != nil {
			ce.log.WithError(err).WithField("server", s.RemotePK()).
				Debug("DialStream failed, trying next session")
			continue
		}
		ce.setCachedRoute(addr.PK, s.RemotePK())
		return stream, true
	}
	return nil, false
}

// sortedDelegatedSessions returns existing sessions to the given delegated servers,
// sorted by ascending latency (lowest ping first).
func (ce *Client) sortedDelegatedSessions(delegatedServers []cipher.PubKey) []ClientSession {
	var sessions []ClientSession
	for _, srvPK := range delegatedServers {
		if dSes, ok := ce.clientSession(ce.porter, srvPK); ok {
			sessions = append(sessions, dSes)
		}
	}
	sortSessionsByLatency(sessions)
	return sessions
}

// sortedMeshSessions returns all sessions NOT in the delegated list,
// sorted by ascending latency.
func (ce *Client) sortedMeshSessions(delegatedServers []cipher.PubKey) []ClientSession {
	var sessions []ClientSession
	for _, ses := range ce.allClientSessions(ce.porter) {
		if hasPK(delegatedServers, ses.RemotePK()) {
			continue
		}
		sessions = append(sessions, ses)
	}
	sortSessionsByLatency(sessions)
	return sessions
}

// sortSessionsByLatency sorts sessions by last measured ping latency (ascending).
// Sessions with no measurement (0) are sorted last.
func sortSessionsByLatency(sessions []ClientSession) {
	sort.Slice(sessions, func(i, j int) bool {
		pi := sessions[i].LastPing()
		pj := sessions[j].LastPing()
		// Treat 0 (unmeasured) as maximum latency.
		if pi == 0 {
			pi = math.MaxInt64
		}
		if pj == 0 {
			pj = math.MaxInt64
		}
		return pi < pj
	})
}

// hasEnoughTimeForFallback checks if the context has at least 10 seconds remaining.
// Short-lived contexts (e.g. test timeouts) skip the fallback to avoid races.
func (ce *Client) hasEnoughTimeForFallback(ctx context.Context) bool {
	deadline, ok := ctx.Deadline()
	if !ok {
		return true // no deadline = unlimited time
	}
	return time.Until(deadline) > 10*time.Second
}

// dialViaConnectedServers tries all connected server sessions as forwarders.
// Used when the destination PK is not registered in discovery (e.g. direct clients).
// If the destination PK matches a connected server, that server is tried FIRST —
// its direct client is connected to itself, so forwarding is guaranteed to work
// if the direct client is running.
func (ce *Client) dialViaConnectedServers(ctx context.Context, addr Addr) (*Stream, error) {
	sessions := ce.allClientSessions(ce.porter)
	sortSessionsByLatency(sessions)

	// Prioritize the session to the destination PK itself — if the visor
	// is connected to the server whose services it wants to reach, that
	// server can forward to its own direct client immediately.
	for i, ses := range sessions {
		if ses.RemotePK() == addr.PK && i > 0 {
			sessions[0], sessions[i] = sessions[i], sessions[0]
			break
		}
	}

	for _, ses := range sessions {
		if ctx.Err() != nil {
			return nil, ErrCannotConnectToDelegated
		}
		// Use a per-attempt timeout derived from the parent context.
		// This prevents races when the parent context cancels mid-handshake.
		dialCtx, dialCancel := context.WithTimeout(ctx, HandshakeTimeout)
		stream, err := ses.DialStream(dialCtx, addr)
		dialCancel()
		if err != nil {
			ce.log.WithError(err).WithField("server", ses.RemotePK()).
				Debug("DialStream failed via connected server, trying next")
			continue
		}
		ce.setCachedRoute(addr.PK, ses.RemotePK())
		return stream, nil
	}

	return nil, ErrCannotConnectToDelegated
}

// LookupIP dails to dmsg servers for public IP of the client.
func (ce *Client) LookupIP(ctx context.Context, servers []cipher.PubKey) (myIP net.IP, err error) {

	cancellabelCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	if servers == nil {
		entries, err := ce.discoverServers(cancellabelCtx, true)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			servers = append(servers, entry.Static)
		}
	}

	// Range client's delegated servers.
	// See if we are already connected to a delegated server.
	for _, srvPK := range servers {
		if dSes, ok := ce.clientSession(ce.porter, srvPK); ok {
			ip, err := dSes.LookupIP(Addr{PK: dSes.RemotePK(), Port: 1})
			if err != nil {
				ce.log.WithError(err).WithField("server_pk", srvPK).Warn("Failed to dial server for IP.")
				continue
			}

			// If the client is test client then ignore Public IP check
			if ce.conf.ClientType == "test" {
				return ip, nil
			}

			// Check if the IP is public, if not try other servers
			if !netutil.IsPublicIP(ip) {
				ce.log.WithField("server_pk", srvPK).WithField("ip", ip.String()).Warn("Received non-public IP address from dmsg server, trying other servers.")
				continue
			}
			return ip, nil
		}
	}

	// Range client's delegated servers.
	// Attempt to connect to a delegated server.
	// And Close it after getting the IP.
	for _, srvPK := range servers {
		dSes, err := ce.EnsureAndObtainSession(ctx, srvPK)
		if err != nil {
			continue
		}
		ip, err := dSes.LookupIP(Addr{PK: dSes.RemotePK(), Port: 1})
		if err != nil {
			ce.log.WithError(err).WithField("server_pk", srvPK).Warn("Failed to dial server for IP.")
			continue
		}
		err = dSes.Close()
		if err != nil {
			ce.log.WithError(err).WithField("server_pk", srvPK).Warn("Failed to close session")
		}

		// If the client is test client then ignore Public IP check
		if ce.conf.ClientType == "test" {
			return ip, nil
		}

		// Check if the IP is public, if not try other servers
		if !netutil.IsPublicIP(ip) {
			ce.log.WithField("server_pk", srvPK).WithField("ip", ip.String()).Warn("Received non-public IP address from dmsg server, trying other servers.")
			continue
		}
		return ip, nil
	}

	return nil, ErrCannotConnectToDelegated
}

// AllStreams returns all the streams of the current client.
func (ce *Client) AllStreams() (out []*Stream) {
	fn := func(port uint16, pv netutil.PorterValue) (next bool) { //nolint
		if str, ok := pv.Value.(*Stream); ok {
			out = append(out, str)
			return true
		}

		for _, v := range pv.Children {
			if str, ok := v.(*Stream); ok {
				out = append(out, str)
			}
		}
		return true
	}

	ce.porter.RangePortValuesAndChildren(fn)
	return out
}

// AllEntries returns all the entries registered in discovery
func (ce *Client) AllEntries(ctx context.Context) (entries []string, err error) {
	err = netutil.NewDefaultRetrier(ce.log).Do(ctx, func() error {
		entries, err = ce.dc.AllEntries(ctx)
		return err
	})
	return entries, err
}

// AllVisorEntries returns all the entries registered in discovery that are visor
func (ce *Client) AllVisorEntries(ctx context.Context) (entries []string, err error) {
	err = netutil.NewDefaultRetrier(ce.log).Do(ctx, func() error {
		entries, err = ce.dc.AllEntries(ctx)
		return err
	})
	return entries, err
}

// ConnectedServersPK return keys of all connected dmsg servers
func (ce *Client) ConnectedServersPK() []string {
	sessions := ce.allClientSessions(ce.porter)
	addrs := make([]string, len(sessions))
	for i, s := range sessions {
		addrs[i] = s.RemotePK().String()
	}
	return addrs
}

// ConnectionsSummary associates connected clients, and the servers that connect such clients.
// Key: Client PK, Value: Slice of Server PKs
type ConnectionsSummary map[cipher.PubKey][]cipher.PubKey

// ConnectionsSummary returns a summary of all connected clients, and the associated servers that connect them.
func (ce *Client) ConnectionsSummary() ConnectionsSummary {
	streams := ce.AllStreams()
	out := make(ConnectionsSummary, len(streams))

	for _, s := range streams {
		cPK := s.RawRemoteAddr().PK
		sPK := s.ServerPK()

		sPKs, ok := out[cPK]
		if ok && hasPK(sPKs, sPK) {
			continue
		}
		out[cPK] = append(sPKs, sPK)
	}

	return out
}
