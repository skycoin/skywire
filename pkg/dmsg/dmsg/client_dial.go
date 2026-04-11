// Package dmsg pkg/dmsg/client_dial.go
package dmsg

import (
	"context"
	"math"
	"net"
	"sort"
	"time"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/netutil"

	"github.com/skycoin/skywire/pkg/dmsg/disc"
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
			// Use a separate context for fallback to avoid racing with parent cancel.
			fallbackCtx, fallbackCancel := context.WithTimeout(context.Background(), HandshakeTimeout)
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

	// Phase 1: Race the target's delegated-server sessions in parallel.
	//
	// Prior behavior: sequential loop — each attempt could burn up to
	// HandshakeTimeout (5s) before moving on. With an outer ctx of ~10s
	// only 1-2 attempts fit, so phase 2/3 saw "ctx deadline exceeded"
	// as soon as one server was unresponsive.
	//
	// New behavior: fire all (non-negative-cached) delegated sessions
	// simultaneously with a tight 3s per-dial budget. First success
	// wins, cancel the rest. On a healthy dial the first session
	// returns in <500ms so the paralellism has negligible cost; on a
	// stale-entry dial every session times out in ~3s and phase 2
	// gets a real remaining budget instead of seeing ctx already done.
	delegatedSessions := ce.sortedDelegatedSessions(entry.Client.DelegatedServers)
	if stream, ok := ce.racePhaseDial(ctx, addr, delegatedSessions, maxPerExistingPhase); ok {
		return stream, nil
	}

	// Phase 2: Race other existing sessions (mesh path — already
	// connected, no new handshake). If servers are meshed, our server
	// forwards the request to the target's server. Sorted by latency.
	// Honors the same 3s per-dial budget as phase 1 and skips
	// negative-cached pairs.
	meshSessions := ce.sortedMeshSessions(entry.Client.DelegatedServers)
	if stream, ok := ce.racePhaseDial(ctx, addr, meshSessions, maxPerExistingPhase); ok {
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

// phaseDialTimeout is the per-attempt deadline for a single session
// DialStream inside racePhaseDial. It is intentionally shorter than
// HandshakeTimeout (5s) so that all delegated sessions can complete
// within a typical 10s outer route-setup context even when every one
// of them times out, leaving phase 2 a meaningful remaining budget.
const phaseDialTimeout = 3 * time.Second

// racePhaseDial races DialStream across all provided sessions in
// parallel, skipping any (dst, srv) pair that's in the negative route
// cache. First success wins and cancels the rest; on success we
// update routeCache and clear negative entries for the destination.
// On total failure we mark every attempted session as a negative
// route for dst. Returns (stream, true) on success, (nil, false)
// otherwise.
func (ce *Client) racePhaseDial(ctx context.Context, addr Addr, sessions []ClientSession, cap int) (*Stream, bool) {
	// Filter out negative-cached sessions up-front and cap the pool.
	candidates := make([]ClientSession, 0, len(sessions))
	for _, s := range sessions {
		if len(candidates) >= cap {
			break
		}
		if ce.isNegativeRoute(addr.PK, s.RemotePK()) {
			ce.log.WithField("server", s.RemotePK()).
				Debug("DialStream skipping session: negative cache hit")
			continue
		}
		candidates = append(candidates, s)
	}
	if len(candidates) == 0 {
		return nil, false
	}

	// Single candidate: no point in the goroutine machinery. Pass
	// the parent ctx directly — no derived timeout is needed because
	// session DialStream already bounds the handshake via its
	// internal HandshakeTimeout deadline, and deriving a short
	// WithTimeout + defer cancel here would immediately cancel the
	// ctx after DialStream returns, which previously raced the
	// session's ctx-cancel watcher goroutine (manifested as
	// "stream closed" errors in TestConcurrentStreams).
	if len(candidates) == 1 {
		stream, err := candidates[0].DialStream(ctx, addr)
		if err != nil {
			ce.log.WithError(err).WithField("server", candidates[0].RemotePK()).
				Debug("DialStream failed (single candidate)")
			ce.markNegativeRoute(addr.PK, candidates[0].RemotePK())
			return nil, false
		}
		ce.clearNegativeRoute(addr.PK)
		ce.setCachedRoute(addr.PK, candidates[0].RemotePK())
		return stream, true
	}

	raceCtx, cancelRace := context.WithTimeout(ctx, phaseDialTimeout)
	defer cancelRace()

	results := make(chan dialResult, len(candidates))
	for _, s := range candidates {
		go func(ses ClientSession) {
			stream, err := ses.DialStream(raceCtx, addr)
			results <- dialResult{stream: stream, srvPK: ses.RemotePK(), err: err}
		}(s)
	}

	// Collect results. First success wins — we cancel the rest and
	// drain in a background goroutine to close any late streams.
	var chosen *Stream
	var chosenSrv cipher.PubKey
	remaining := len(candidates)
	for remaining > 0 {
		select {
		case <-ctx.Done():
			// Parent context died — cancel the race and drain in
			// background, then return failure. Any stream that
			// comes back will be closed by the drain loop below.
			cancelRace()
			go drainAndCloseResults(results, remaining)
			return nil, false
		case res := <-results:
			remaining--
			if res.err != nil {
				ce.log.WithError(res.err).WithField("server", res.srvPK).
					Debug("DialStream failed in race")
				ce.markNegativeRoute(addr.PK, res.srvPK)
				continue
			}
			if chosen == nil {
				chosen = res.stream
				chosenSrv = res.srvPK
				// Cancel any still-running dials — their streams
				// will be closed by the drain loop.
				cancelRace()
			} else {
				// Late arrival after we already chose a winner.
				_ = res.stream.Close() //nolint:errcheck
			}
		}
	}

	if chosen == nil {
		return nil, false
	}
	ce.clearNegativeRoute(addr.PK)
	ce.setCachedRoute(addr.PK, chosenSrv)
	return chosen, true
}

// dialResult carries the outcome of a single DialStream attempt inside
// racePhaseDial. A named package-level type so drainAndCloseResults can
// accept the same channel element type.
type dialResult struct {
	stream *Stream
	srvPK  cipher.PubKey
	err    error
}

// drainAndCloseResults consumes n more results from ch and closes any
// successfully-returned streams. Used when racePhaseDial bails out
// early and needs to avoid leaking streams that the goroutines may
// still successfully open before they notice the cancellation.
func drainAndCloseResults(ch <-chan dialResult, n int) {
	for i := 0; i < n; i++ {
		res := <-ch
		if res.stream != nil {
			_ = res.stream.Close() //nolint:errcheck
		}
	}
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
func (ce *Client) dialViaConnectedServers(ctx context.Context, addr Addr) (*Stream, error) {
	sessions := ce.allClientSessions(ce.porter)
	sortSessionsByLatency(sessions)

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
