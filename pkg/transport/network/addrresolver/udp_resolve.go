package addrresolver

import (
	"context"
	"encoding/json"
	"net"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// A sudph lookup can ride the UDP control connection a visor already holds to
// the address resolver, instead of an HTTP request with its own handshake. The
// resolver answers and asks the peer to dial back, as GET /resolve/sudph does.
//
// Neither message is acted on by a side that predates it: a resolver ignores a
// control message that is not an address update (it has no port), and only a
// visor that asked receives a reply.

// UDPResolveRequest asks for a peer's sudph binding.
type UDPResolveRequest struct {
	Resolve string `json:"resolve"`
	ID      uint32 `json:"id"`
}

// UDPResolveReply answers a UDPResolveRequest with the same ID.
type UDPResolveReply struct {
	Resolved uint32     `json:"resolved"`
	Found    bool       `json:"found,omitempty"`
	Data     *VisorData `json:"data,omitempty"`
	Err      string     `json:"err,omitempty"`
}

// ParseUDPResolveRequest reports whether a control message is a lookup.
func ParseUDPResolveRequest(b []byte) (UDPResolveRequest, bool) {
	var req UDPResolveRequest
	if len(b) == 0 || b[0] != '{' || json.Unmarshal(b, &req) != nil || req.Resolve == "" {
		return UDPResolveRequest{}, false
	}
	return req, true
}

// parseUDPResolveReply reports whether a message from the resolver is a
// reply to a lookup, as opposed to a request to dial a peer.
func parseUDPResolveReply(b []byte) (UDPResolveReply, bool) {
	var probe struct {
		Resolved *uint32 `json:"resolved"`
	}
	if len(b) == 0 || b[0] != '{' || json.Unmarshal(b, &probe) != nil || probe.Resolved == nil {
		return UDPResolveReply{}, false
	}
	var reply UDPResolveReply
	if json.Unmarshal(b, &reply) != nil {
		return UDPResolveReply{}, false
	}
	return reply, true
}

const (
	// udpResolveProbeWait is how long the first lookup on a control
	// connection waits before taking the resolver for one that predates
	// lookups over UDP, which never answers.
	udpResolveProbeWait = 2 * time.Second
	// udpResolveWait bounds a lookup once the resolver has answered one.
	udpResolveWait = 5 * time.Second
)

const (
	udpResolveUnknown = iota
	udpResolveSupported
	udpResolveUnsupported
)

// udpResolver tracks lookups in flight on the current control connection.
type udpResolver struct {
	mu      sync.Mutex
	conn    net.Conn
	state   int
	nextID  uint32
	waiters map[uint32]chan UDPResolveReply
}

// resolveUDP looks pk up over the sudph control connection. handled is false
// when there is no connection, the resolver does not answer lookups over it,
// or the request could not be sent, and the caller then asks over HTTP.
func (c *httpClient) resolveUDP(ctx context.Context, pk cipher.PubKey) (data VisorData, err error, handled bool) {
	c.sudphArConnMu.Lock()
	conn := c.sudphArConn
	c.sudphArConnMu.Unlock()
	if conn == nil {
		return VisorData{}, nil, false
	}
	u := &c.udpRes
	u.mu.Lock()
	if u.conn != conn {
		u.conn, u.state, u.waiters = conn, udpResolveUnknown, map[uint32]chan UDPResolveReply{}
	}
	if u.state == udpResolveUnsupported {
		u.mu.Unlock()
		return VisorData{}, nil, false
	}
	u.nextID++
	id, probing := u.nextID, u.state == udpResolveUnknown
	ch := make(chan UDPResolveReply, 1)
	u.waiters[id] = ch
	u.mu.Unlock()
	forget := func() {
		u.mu.Lock()
		delete(u.waiters, id)
		u.mu.Unlock()
	}

	req, err := json.Marshal(UDPResolveRequest{Resolve: pk.Hex(), ID: id})
	if err != nil {
		forget()
		return VisorData{}, nil, false
	}
	if _, err := conn.Write(req); err != nil {
		forget()
		return VisorData{}, nil, false
	}
	wait := udpResolveWait
	if probing {
		wait = udpResolveProbeWait
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case reply := <-ch:
		if reply.Err != "" {
			return VisorData{}, nil, false
		}
		if !reply.Found || reply.Data == nil {
			return VisorData{}, ErrNoEntry, true
		}
		return *reply.Data, nil, true
	case <-timer.C:
		forget()
		if probing {
			u.mu.Lock()
			if u.conn == conn && u.state == udpResolveUnknown {
				u.state = udpResolveUnsupported
			}
			u.mu.Unlock()
		}
		return VisorData{}, nil, false
	case <-ctx.Done():
		forget()
		return VisorData{}, ctx.Err(), true
	}
}

// deliverUDPReply hands a reply read on conn to the lookup waiting for it.
func (c *httpClient) deliverUDPReply(conn net.Conn, reply UDPResolveReply) {
	u := &c.udpRes
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.conn != conn {
		return
	}
	u.state = udpResolveSupported
	if ch, ok := u.waiters[reply.Resolved]; ok {
		delete(u.waiters, reply.Resolved)
		ch <- reply
	}
}
