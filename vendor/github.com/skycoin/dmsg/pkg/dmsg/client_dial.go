// Package dmsg pkg/dmsg/client_dial.go
package dmsg

import (
	"context"
	"math"
	"net"
	"sort"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/netutil"

	"github.com/skycoin/dmsg/pkg/disc"
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
	entry, err := ce.getClientEntryCached(ctx, addr.PK)
	if err != nil {
		return nil, err
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

	// Phase 1: Try existing sessions to the target's delegated servers (direct path, cheapest).
	// Sort by latency so the lowest-latency server is tried first.
	delegatedSessions := ce.sortedDelegatedSessions(entry.Client.DelegatedServers)
	for _, dSes := range delegatedSessions {
		stream, err := dSes.DialStream(ctx, addr)
		if err != nil {
			ce.log.WithError(err).WithField("server", dSes.RemotePK()).
				Debug("DialStream failed via existing session, trying next server")
			continue
		}
		ce.setCachedRoute(addr.PK, dSes.RemotePK())
		return stream, nil
	}

	// Phase 2: Try all other existing sessions (mesh path — already connected, no new handshake).
	// If servers are meshed, our server forwards the request to the target's server.
	// Sorted by latency.
	meshSessions := ce.sortedMeshSessions(entry.Client.DelegatedServers)
	for _, ses := range meshSessions {
		stream, err := ses.DialStream(ctx, addr)
		if err != nil {
			ce.log.WithError(err).WithField("server", ses.RemotePK()).
				Debug("DialStream failed via mesh, trying next server")
			continue
		}
		ce.setCachedRoute(addr.PK, ses.RemotePK())
		return stream, nil
	}

	// Phase 3: Last resort: establish new sessions to the target's delegated servers.
	for _, srvPK := range entry.Client.DelegatedServers {
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
