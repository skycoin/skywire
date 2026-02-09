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
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
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

// clientMsg is the message sent by the client to request port forwarding
type clientMsg struct {
	Port   int  `json:"port"`
	RawTCP bool `json:"raw_tcp,omitempty"`
}

// serverReply is the response sent to the client
type serverReply struct {
	Error *string `json:"error,omitempty"`
}

// NewServer creates a new skyforward server
func NewServer(log logrus.FieldLogger, ports []int, allowedPKs []cipher.PubKey) *Server {
	portMap := make(map[int]struct{})
	for _, p := range ports {
		portMap[p] = struct{}{}
	}

	wlMap := make(map[cipher.PubKey]struct{})
	for _, pk := range allowedPKs {
		wlMap[pk] = struct{}{}
	}

	return &Server{
		log:       log,
		ports:     portMap,
		whitelist: wlMap,
		useWL:     len(allowedPKs) > 0,
		closeCh:   make(chan struct{}),
	}
}

// Serve starts accepting connections on the listener
func (s *Server) Serve(l net.Listener) error {
	s.listener = l
	s.log.Info("Skyforward server started")

	for {
		conn, err := l.Accept()
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

// Close stops the server
func (s *Server) Close() error {
	var err error
	s.closeOnce.Do(func() {
		close(s.closeCh)
		if s.listener != nil {
			err = s.listener.Close()
		}
		s.activeConn.Wait()
	})
	return err
}

func (s *Server) handleConn(conn net.Conn) {
	defer func() {
		if err := conn.Close(); err != nil {
			s.log.WithError(err).Debug("Error closing connection")
		}
	}()

	// Get remote public key for whitelist check
	wrappedConn, err := appnet.WrapConn(conn)
	if err != nil {
		s.log.WithError(err).Error("Failed to wrap connection")
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

	// Read client message
	buf := make([]byte, 32*1024)
	n, err := wrappedConn.Read(buf)
	if err != nil {
		s.log.WithError(err).Error("Failed to read client message")
		return
	}

	var msg clientMsg
	if err := json.Unmarshal(buf[:n], &msg); err != nil {
		s.log.WithError(err).Error("Failed to unmarshal client message")
		s.sendError(wrappedConn, err)
		return
	}

	s.log.WithFields(logrus.Fields{
		"port":    msg.Port,
		"raw_tcp": msg.RawTCP,
	}).Debug("Received forward request")

	// Check if port is allowed
	s.mu.RLock()
	_, portAllowed := s.ports[msg.Port]
	s.mu.RUnlock()

	if !portAllowed {
		s.log.WithField("port", msg.Port).Warn("Port not in allowed list")
		s.sendError(wrappedConn, fmt.Errorf("port %d not exposed", msg.Port))
		return
	}

	// Check if local port is available
	localAddr := fmt.Sprintf("127.0.0.1:%d", msg.Port)
	testConn, err := net.Dial("tcp", localAddr)
	if err != nil {
		s.log.WithError(err).WithField("port", msg.Port).Warn("Local port not available")
		s.sendError(wrappedConn, fmt.Errorf("local port %d not available", msg.Port))
		return
	}
	_ = testConn.Close() //nolint:errcheck

	// Send success reply
	s.sendError(wrappedConn, nil)

	// Forward traffic
	if msg.RawTCP {
		s.forwardRawTCP(wrappedConn, localAddr)
	} else {
		s.forwardHTTP(wrappedConn, localAddr)
	}
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
		_, err := io.Copy(localConn, remoteConn)
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
			s.log.WithError(err).Debug("remote->local copy ended")
		}
		done <- struct{}{}
	}()

	// local -> remote
	go func() {
		_, err := io.Copy(remoteConn, localConn)
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
			s.log.WithError(err).Debug("local->remote copy ended")
		}
		done <- struct{}{}
	}()

	// Wait for one direction to finish
	<-done

	// Close both connections
	_ = localConn.Close() //nolint:errcheck
	// remoteConn will be closed by the caller

	<-done
	s.log.Debug("Raw TCP forwarding completed")
}

func (s *Server) forwardHTTP(remoteConn net.Conn, localAddr string) {
	// For HTTP mode, we do a simpler request-response forwarding
	// This matches the original behavior in pkg/visor/init.go
	for {
		select {
		case <-s.closeCh:
			return
		default:
		}

		buf := make([]byte, 32*1024)
		n, err := remoteConn.Read(buf)
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				s.log.WithError(err).Debug("Failed to read from remote")
			}
			return
		}

		// Connect to local server and forward the data
		localConn, err := net.Dial("tcp", localAddr)
		if err != nil {
			s.log.WithError(err).Error("Failed to dial local server")
			return
		}

		// Send data to local
		if _, err := localConn.Write(buf[:n]); err != nil {
			s.log.WithError(err).Error("Failed to write to local")
			_ = localConn.Close() //nolint:errcheck
			return
		}

		// Read response from local
		respBuf := make([]byte, 64*1024)
		total := 0
		for {
			localConn.SetReadDeadline(timeoutAfter(5)) //nolint:errcheck,gosec
			rn, err := localConn.Read(respBuf[total:])
			if err != nil {
				if !errors.Is(err, io.EOF) && !isTimeout(err) {
					s.log.WithError(err).Debug("Failed to read from local")
				}
				break
			}
			total += rn
			if total >= len(respBuf) {
				break
			}
		}
		_ = localConn.Close() //nolint:errcheck

		if total > 0 {
			if _, err := remoteConn.Write(respBuf[:total]); err != nil {
				s.log.WithError(err).Error("Failed to write response to remote")
				return
			}
		}
	}
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
