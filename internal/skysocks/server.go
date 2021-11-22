package skysocks

import (
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"

	"github.com/armon/go-socks5"
	ipc "github.com/james-barrow/golang-ipc"
	"github.com/sirupsen/logrus"
	"github.com/skycoin/yamux"

	"github.com/skycoin/skywire/pkg/skyenv"
)

// Server implements multiplexing proxy server using yamux.
type Server struct {
	sMu      sync.Mutex
	socks    *socks5.Server
	listener net.Listener
	log      logrus.FieldLogger
	closed   uint32
}

// NewServer constructs a new Server.
func NewServer(passcode string, l logrus.FieldLogger) (*Server, error) {
	var credentials socks5.CredentialStore
	if passcode != "" {
		credentials = passcodeCredentials(passcode)
	}

	s, err := socks5.New(&socks5.Config{Credentials: credentials})
	if err != nil {
		return nil, fmt.Errorf("socks5: %w", err)
	}

	return &Server{socks: s, log: l}, nil
}

// Serve accept connections from listener and serves socks5 proxy for
// the incoming connections.
func (s *Server) Serve(l net.Listener) error {
	s.sMu.Lock()
	s.listener = l
	s.sMu.Unlock()

	for {
		if s.isClosed() {
			return nil
		}

		conn, err := l.Accept()
		if err != nil {
			if s.isClosed() {
				s.log.WithError(err).Debugln("Failed to accept skysocks connection, but server is closed")
				return nil
			}

			s.log.WithError(err).Debugln("Failed to accept skysocks connection")

			return fmt.Errorf("accept: %w", err)
		}

		s.log.Infoln("Accepted new skysocks connection")

		sessionCfg := yamux.DefaultConfig()
		sessionCfg.EnableKeepAlive = false
		session, err := yamux.Server(conn, sessionCfg)
		if err != nil {
			return fmt.Errorf("yamux server failure: %w", err)
		}

		go func() {
			if err := s.socks.Serve(session); err != nil {
				s.log.Error("Failed to start SOCKS5 server:", err)
			}
		}()
	}
}

// ListenIPC starts named-pipe based connection server for windows or unix socket in Linux/Mac
func (s *Server) ListenIPC(client *ipc.Client) {
	listenIPC(client, skyenv.SkychatName, func() {
		client.Close()
		if err := s.Close(); err != nil {
			fmt.Println("Error closing skysocks server: ", err.Error())
			os.Exit(1)
		}
	})
}

// Close implement io.Closer.
func (s *Server) Close() error {
	if s == nil {
		return nil
	}

	s.close()

	defer s.sMu.Unlock()
	s.sMu.Lock()
	return s.listener.Close()
}

func (s *Server) close() {
	atomic.StoreUint32(&s.closed, 1)
}

func (s *Server) isClosed() bool {
	return atomic.LoadUint32(&s.closed) != 0
}

type passcodeCredentials string

func (s passcodeCredentials) Valid(user, password string) bool {
	if len(s) == 0 {
		return true
	}

	return user == string(s) || password == string(s)
}
