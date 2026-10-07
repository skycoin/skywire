// Package dmsg pkg/dmsg/dmsg/relay_stats.go c2-dmsg-core
package dmsg

import "sync/atomic"

// relayCounters count the streams an entity bridged between two sessions.
type relayCounters struct {
	streams atomic.Uint64
	active  atomic.Int64
	up      atomic.Uint64 // bytes from the side that asked for the stream
	down    atomic.Uint64 // bytes from the side it was asked of
}

// ServerStats is what a dmsg server is carrying.
type ServerStats struct {
	// ClientSessions are the clients connected to this server.
	ClientSessions int
	// PeerSessions are this server's links to other servers.
	PeerSessions int
	// ActiveStreams are streams being relayed now.
	ActiveStreams int64
	// StreamsRelayed counts every stream relayed since the server started.
	StreamsRelayed uint64
	// BytesUp is what relayed streams carried from the side that opened them,
	// BytesDown what they carried back.
	BytesUp, BytesDown uint64
}

// Stats returns what the server is carrying now and has relayed so far.
func (s *Server) Stats() ServerStats {
	st := ServerStats{
		ActiveStreams:  s.relay.active.Load(),
		StreamsRelayed: s.relay.streams.Load(),
		BytesUp:        s.relay.up.Load(),
		BytesDown:      s.relay.down.Load(),
	}
	s.sessionsMx.Lock()
	for _, ses := range s.sessions {
		if !ses.isPeer {
			st.ClientSessions++
		}
	}
	s.sessionsMx.Unlock()
	s.peerSessionsMx.Lock()
	st.PeerSessions = len(s.peerSessions)
	s.peerSessionsMx.Unlock()
	return st
}
