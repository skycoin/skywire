// Package skysocks internal/skysocks/server.go
package skysocks

import (
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"

	"github.com/armon/go-socks5"
	"github.com/hashicorp/yamux"
	ipc "github.com/james-barrow/golang-ipc"

	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
)

// Server implements multiplexing proxy server using yamux.
type Server struct {
	appCl     *app.Client
	sMu       sync.Mutex
	socks     *socks5.Server
	listener  net.Listener
	closed    uint32
	whitelist map[cipher.PubKey]struct{}
	useWL     bool
}

// NewServer constructs a new Server.
func NewServer(whitelist []cipher.PubKey, appCl *app.Client) (*Server, error) {
	s, err := socks5.New(&socks5.Config{})
	if err != nil {
		return nil, fmt.Errorf("socks5: %w", err)
	}

	wlMap := make(map[cipher.PubKey]struct{})
	for _, pk := range whitelist {
		wlMap[pk] = struct{}{}
	}

	server := &Server{
		appCl:     appCl,
		socks:     s,
		whitelist: wlMap,
		useWL:     len(whitelist) > 0,
	}

	return server, nil
}

// Serve accept connections from listener and serves socks5 proxy for
// the incoming connections.
func (s *Server) Serve(l net.Listener) error {
	s.sMu.Lock()
	s.listener = l
	s.sMu.Unlock()

	if s.appCl != nil {
		s.setAppStatus(appserver.AppDetailedStatusRunning)
	}

	for {
		if s.isClosed() {
			return nil
		}

		conn, err := l.Accept()
		if err != nil {
			if s.isClosed() {
				if s.appCl != nil {
					s.appCl.Log().Debugf("Failed to accept skysocks connection, but server is closed: %v", err)
				}
				return nil
			}

			if s.appCl != nil {
				s.appCl.Log().Errorf("Failed to accept skysocks connection: %v", err)
			}

			return fmt.Errorf("accept: %w", err)
		}

		if s.appCl != nil {
			s.appCl.Log().Debug("Accepted new skysocks connection")
		}

		// Check whitelist if enabled
		if s.useWL {
			remotePK, err := s.getRemotePK(conn)
			if err != nil {
				if s.appCl != nil {
					s.appCl.Log().WithError(err).Warn("Failed to get remote PK, rejecting connection")
				}
				_ = conn.Close() //nolint:errcheck
				continue
			}

			if _, allowed := s.whitelist[remotePK]; !allowed {
				if s.appCl != nil {
					s.appCl.Log().WithField("remote_pk", remotePK.Hex()).Warn("Connection rejected: not in whitelist")
				}
				_ = conn.Close() //nolint:errcheck
				continue
			}

			if s.appCl != nil {
				s.appCl.Log().WithField("remote_pk", remotePK.Hex()).Debug("Whitelisted connection accepted")
			}
		}

		sessionCfg := yamux.DefaultConfig()
		sessionCfg.EnableKeepAlive = false
		session, err := yamux.Server(conn, sessionCfg)
		if err != nil {
			return fmt.Errorf("yamux server failure: %w", err)
		}

		go func() {
			if err := s.socks.Serve(session); err != nil {
				if s.appCl != nil {
					s.appCl.Log().Errorf("Failed to start SOCKS5 server: %v", err)
				}
			}
		}()
	}
}

// getRemotePK extracts the remote public key from the connection
func (s *Server) getRemotePK(conn net.Conn) (cipher.PubKey, error) {
	wrappedConn, err := appnet.WrapConn(conn)
	if err != nil {
		return cipher.PubKey{}, fmt.Errorf("failed to wrap connection: %w", err)
	}

	rAddr, ok := wrappedConn.RemoteAddr().(appnet.Addr)
	if !ok {
		return cipher.PubKey{}, fmt.Errorf("failed to get remote address")
	}

	return rAddr.PubKey, nil
}

// ListenIPC starts named-pipe based connection server for windows or unix socket in Linux/Mac
func (s *Server) ListenIPC(client *ipc.Client) {
	if s.appCl == nil {
		return
	}
	listenIPC(client, skyenv.SkysocksName, s.appCl.Log(), func() {
		client.Close()
		if err := s.Close(); err != nil {
			s.appCl.Log().Errorf("Error closing skysocks server: %v", err)
			os.Exit(1)
		}
	})
}

func (s *Server) setAppStatus(status appserver.AppDetailedStatus) {
	if err := s.appCl.SetDetailedStatus(string(status)); err != nil {
		s.appCl.Log().Errorf("Failed to set status %v: %v", status, err)
	}
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

// IsPublic returns true if whitelist is not enabled (server is public)
func (s *Server) IsPublic() bool {
	return !s.useWL
}
