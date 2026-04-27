// Package skynet internal/skynet/server.go
package skynet

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
)

// Server handles incoming skynet connections and forwards them to local ports
type Server struct {
	log        logrus.FieldLogger
	ports      map[int]struct{}
	whitelist  map[cipher.PubKey]struct{}
	useWL      bool
	mu         sync.RWMutex
	listener   net.Listener
	closeCh    chan struct{}
	closeOnce  sync.Once
	activeConn sync.WaitGroup
}

// NewServer creates a new skynet server
func NewServer(log logrus.FieldLogger) *Server {
	return &Server{
		log:       log,
		ports:     make(map[int]struct{}),
		whitelist: make(map[cipher.PubKey]struct{}),
		closeCh:   make(chan struct{}),
	}
}

// Serve starts the server on the given listener
func (s *Server) Serve(lis net.Listener) error {
	s.mu.Lock()
	s.listener = lis
	s.mu.Unlock()

	s.log.Info("Skynet server started")

	for {
		conn, err := lis.Accept()
		if err != nil {
			select {
			case <-s.closeCh:
				return nil
			default:
				if !errors.Is(err, net.ErrClosed) {
					s.log.WithError(err).Error("Failed to accept connection")
				}
				return err
			}
		}

		s.activeConn.Add(1)
		go func(c net.Conn) {
			defer s.activeConn.Done()
			s.handleConn(c)
		}(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer func() { _ = conn.Close() }() //nolint:errcheck,gosec

	// Wrap the connection
	wrappedConn, ok := conn.(*appnet.WrappedConn)
	if !ok {
		s.log.Error("Connection is not a WrappedConn")
		return
	}

	rAddr, ok := wrappedConn.RemoteAddr().(appnet.Addr)
	if !ok {
		s.log.Error("Failed to get remote address")
		return
	}

	remotePK := rAddr.PubKey
	s.log.WithField("remote_pk", remotePK.Hex()).Debug("Incoming connection")

	// Check whitelist
	if s.useWL {
		s.mu.RLock()
		_, allowed := s.whitelist[remotePK]
		s.mu.RUnlock()

		if !allowed {
			s.log.WithField("remote_pk", remotePK.Hex()).Warn("Connection rejected: not in whitelist")
			s.sendError(wrappedConn, fmt.Errorf("not authorized"))
			return
		}
	}

	// Read client message with size limit
	buf := make([]byte, 32*1024)
	n, err := wrappedConn.Read(buf)
	if err != nil {
		s.log.WithError(err).Error("Failed to read client message")
		return
	}

	if n > int(MaxRequestSize) {
		s.log.Error("Client message exceeds maximum size")
		s.sendError(wrappedConn, fmt.Errorf("request too large"))
		return
	}

	var msg clientMsg
	if err := json.Unmarshal(buf[:n], &msg); err != nil {
		s.log.WithError(err).Error("Failed to unmarshal client message")
		s.sendError(wrappedConn, err)
		return
	}

	s.log.WithField("port", msg.Port).Debug("Received forward request")

	// Check if port is allowed
	s.mu.RLock()
	_, portAllowed := s.ports[msg.Port]
	s.mu.RUnlock()

	if !portAllowed {
		s.log.WithField("port", msg.Port).Warn("Port not in allowed list")
		s.sendError(wrappedConn, fmt.Errorf("port %d not exposed", msg.Port))
		return
	}

	// Send success reply
	s.sendError(wrappedConn, nil)

	// Forward traffic
	s.forwardRawTCP(wrappedConn, fmt.Sprintf("127.0.0.1:%d", msg.Port))
}

func (s *Server) sendError(conn net.Conn, sendErr error) {
	var reply serverReply
	if sendErr != nil {
		errStr := sendErr.Error()
		reply.Error = &errStr
	}

	data, err := json.Marshal(reply)
	if err != nil {
		s.log.WithError(err).Error("Failed to marshal reply")
		return
	}

	if _, err := conn.Write(data); err != nil {
		s.log.WithError(err).Error("Failed to send reply")
	}
}

func (s *Server) forwardRawTCP(remoteConn net.Conn, localAddr string) {
	localConn, err := net.Dial("tcp", localAddr)
	if err != nil {
		s.log.WithError(err).Error("Failed to dial local server")
		return
	}

	done := make(chan struct{}, 2)

	// remote -> local
	go func() {
		defer func() { done <- struct{}{} }()
		_, err := io.Copy(localConn, remoteConn)
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
			s.log.WithError(err).Debug("remote->local copy ended")
		}
	}()

	// local -> remote
	go func() {
		defer func() { done <- struct{}{} }()
		_, err := io.Copy(remoteConn, localConn)
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
			s.log.WithError(err).Debug("local->remote copy ended")
		}
	}()

	// Wait for one direction to finish, then close both to unblock the other
	<-done
	_ = localConn.Close()  //nolint:errcheck,gosec
	_ = remoteConn.Close() //nolint:errcheck,gosec
	<-done

	s.log.Debug("Raw TCP forwarding completed")
}

// AddPort adds a port to the allowed list
func (s *Server) AddPort(port int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ports[port] = struct{}{}
}

// RemovePort removes a port from the allowed list
func (s *Server) RemovePort(port int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.ports, port)
}

// AddToWhitelist adds a public key to the whitelist
func (s *Server) AddToWhitelist(pk cipher.PubKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.whitelist[pk] = struct{}{}
	s.useWL = true
}

// RemoveFromWhitelist removes a public key from the whitelist
func (s *Server) RemoveFromWhitelist(pk cipher.PubKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.whitelist, pk)
	if len(s.whitelist) == 0 {
		s.useWL = false
	}
}

// Ports returns the list of exposed ports
func (s *Server) Ports() []int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ports := make([]int, 0, len(s.ports))
	for p := range s.ports {
		ports = append(ports, p)
	}
	return ports
}

// Whitelist returns the list of whitelisted public keys
func (s *Server) Whitelist() []cipher.PubKey {
	s.mu.RLock()
	defer s.mu.RUnlock()
	pks := make([]cipher.PubKey, 0, len(s.whitelist))
	for pk := range s.whitelist {
		pks = append(pks, pk)
	}
	return pks
}

// Close stops the server
func (s *Server) Close() error {
	var err error
	s.closeOnce.Do(func() {
		close(s.closeCh)

		s.mu.Lock()
		if s.listener != nil {
			if cErr := s.listener.Close(); cErr != nil {
				err = cErr
			}
		}
		s.mu.Unlock()

		s.activeConn.Wait()
	})
	return err
}
