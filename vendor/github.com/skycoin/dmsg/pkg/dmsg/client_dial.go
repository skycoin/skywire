// Package dmsg pkg/dmsg/client_dial.go
package dmsg

import (
	"context"
	"net"

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
	entry, err := getClientEntry(ctx, ce.dc, addr.PK)
	if err != nil {
		return nil, err
	}

	// Range client's delegated servers.
	// Try existing sessions first, falling back to next server on failure.
	for _, srvPK := range entry.Client.DelegatedServers {
		if dSes, ok := ce.clientSession(ce.porter, srvPK); ok {
			stream, err := dSes.DialStream(addr)
			if err != nil {
				ce.log.WithError(err).WithField("server", srvPK).
					Debug("DialStream failed via existing session, trying next server")
				continue
			}
			return stream, nil
		}
	}

	// Range client's delegated servers.
	// Attempt to connect to a delegated server.
	for _, srvPK := range entry.Client.DelegatedServers {
		dSes, err := ce.EnsureAndObtainSession(ctx, srvPK)
		if err != nil {
			continue
		}
		stream, err := dSes.DialStream(addr)
		if err != nil {
			ce.log.WithError(err).WithField("server", srvPK).
				Debug("DialStream failed via new session, trying next server")
			continue
		}
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
