// Package dmsg pkg/dmsg/dmsg/server_inproc.go c1-net-dmsg
//
// In-process sessions. A visor that runs a dmsg server and also hosts
// services under their own keys has each service's dmsg client hold a session
// with that server like any other client, over QUIC or TCP on loopback. Every
// stream a remote visor opens to the service through this server then crosses
// the loopback session as well, so the host pays the carrier's crypto and
// syscalls twice. A server opted in with ServeInProcess is reached by clients
// in the same process over an in-memory pipe instead. The session is otherwise
// a normal one, with the same Noise handshake and stream mux.
package dmsg

import (
	"errors"
	"net"
	"sync"

	"github.com/0magnet/yamux"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
)

// CarrierInProcess names a session over an in-memory pipe to a server in the
// same process. It is never dialed by preference, only when the server is here.
const CarrierInProcess = "inproc"

var inProcServers struct {
	mu sync.RWMutex
	m  map[cipher.PubKey]*Server
}

// ServeInProcess lets dmsg clients in this process reach s over an in-memory
// pipe until s closes.
func (s *Server) ServeInProcess() {
	inProcServers.mu.Lock()
	if inProcServers.m == nil {
		inProcServers.m = make(map[cipher.PubKey]*Server)
	}
	inProcServers.m[s.pk] = s
	inProcServers.mu.Unlock()
	go func() {
		<-s.done
		inProcServers.mu.Lock()
		if inProcServers.m[s.pk] == s {
			delete(inProcServers.m, s.pk)
		}
		inProcServers.mu.Unlock()
	}()
}

func inProcessServer(pk cipher.PubKey) *Server {
	inProcServers.mu.RLock()
	defer inProcServers.mu.RUnlock()
	return inProcServers.m[pk]
}

var errInProcessClosed = errors.New("dmsg: in-process server is closed")

// acceptInProcess serves conn as a session, as Serve does an accepted socket.
func (s *Server) acceptInProcess(conn net.Conn) error {
	if isClosed(s.done) {
		return errInProcessClosed
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			if err := recover(); err != nil {
				s.log.Warnf("panic in in-process handleSession: %+v", err)
			}
		}()
		s.handleSession(conn)
	}()
	return nil
}

// dialSessionInProcess opens a session with srv, the server entry names, over
// an in-memory pipe.
func (ce *Client) dialSessionInProcess(srv *Server, entry *disc.Entry) (ClientSession, error) {
	clientEnd, serverEnd := net.Pipe()
	if err := srv.acceptInProcess(serverEnd); err != nil {
		clientEnd.Close() //nolint:errcheck,gosec
		serverEnd.Close() //nolint:errcheck,gosec
		return ClientSession{}, err
	}
	dSes, err := makeClientSession(&ce.EntityCommon, ce.porter, clientEnd, entry.Static)
	if err != nil {
		clientEnd.Close() //nolint:errcheck,gosec
		return ClientSession{}, err
	}
	if dSes.sm.yamux, err = yamux.Client(clientEnd, YamuxConfig()); err != nil {
		clientEnd.Close() //nolint:errcheck,gosec
		return ClientSession{}, err
	}
	return dSes, nil
}
