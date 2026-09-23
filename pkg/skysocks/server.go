// Package skysocks pkg/skysocks/server.go c4-app-proxy
package skysocks

import (
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"

	ipc "github.com/0magnet/golang-ipc"
	"github.com/0magnet/yamux"
	"github.com/armon/go-socks5"
	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
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
	// Give go-socks5 an explicit logrus-backed logger so it does not fall back
	// to its default log.New(os.Stdout, …) and leak lines to stdout. Cap at
	// Debug: a SOCKS proxy failing a *client-requested* dial is a routine,
	// client-driven condition, not a proxy error.
	s, err := socks5.New(&socks5.Config{
		Logger: logging.NewStdLoggerLevel(logging.MustGetLogger("skysocks"), logrus.DebugLevel),
	})
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
		sessionCfg.MaxStreamWindowSize = muxStreamWindowBytes
		// Same raised write valve as the client session (see muxConnWriteTimeout):
		// at yamux's 10s default the server reset saturated DOWNLOADS the same way
		// the client reset saturated uploads.
		sessionCfg.ConnectionWriteTimeout = muxConnWriteTimeout
		session, err := yamux.Server(conn, sessionCfg)
		if err != nil {
			return fmt.Errorf("yamux server failure: %w", err)
		}

		go s.serveSession(session)
	}
}

// serveSession accepts the session's streams and gives each one to the handler
// its first byte names.
//
// This replaces socks.Serve, which would hand every stream to the SOCKS5
// server. Almost all of them still go there; the exception is a datagram relay
// (udp.go), which is not a SOCKS5 session at all. One byte is enough to tell
// them apart and is the most that may be read: a client is entitled to send
// only its three-byte greeting and wait, so a peek any longer than a single
// byte would deadlock the connections that behave that way.
func (s *Server) serveSession(session net.Listener) {
	for {
		stream, err := session.Accept()
		if err != nil {
			if !s.isClosed() && s.appCl != nil {
				s.appCl.Log().Debugf("skysocks session ended: %v", err)
			}
			return
		}
		go s.serveStream(stream)
	}
}

// serveStream dispatches one stream on its first byte.
func (s *Server) serveStream(stream net.Conn) {
	var first [1]byte
	if _, err := io.ReadFull(stream, first[:]); err != nil {
		stream.Close() //nolint:errcheck,gosec // nothing was ever served on it
		return
	}

	if first[0] == udpMagic[0] {
		defer stream.Close() //nolint:errcheck,gosec // the relay owns the stream
		if err := readUDPRelayPreamble(stream); err != nil {
			if s.appCl != nil {
				s.appCl.Log().Debugf("skysocks: rejected a UDP relay stream: %v", err)
			}
			return
		}
		serveUDPRelay(stream, s.appCl)
		return
	}

	// An ordinary SOCKS5 session. The byte already read is put back in
	// front of the stream so the SOCKS5 server sees the greeting whole.
	if err := s.socks.ServeConn(&prefixConn{Conn: stream, prefix: first[:]}); err != nil && s.appCl != nil {
		s.appCl.Log().Debugf("skysocks: stream ended: %v", err)
	}
}

// prefixConn is a net.Conn whose first reads return prefix before the
// underlying connection's own bytes.
type prefixConn struct {
	net.Conn
	prefix []byte
}

func (p *prefixConn) Read(b []byte) (int, error) {
	if len(p.prefix) > 0 {
		n := copy(b, p.prefix)
		p.prefix = p.prefix[n:]
		return n, nil
	}
	return p.Conn.Read(b)
}

// CloseWrite forwards the origin's end of data to the client as a FIN.
//
// go-socks5 signals it by calling CloseWrite on the destination — but only if
// the destination has the method, and a yamux stream does not, so without this
// the signal was dropped on the floor. The stream then stayed open until the
// client closed it, and a client waiting for exactly that EOF never did: an
// FTP data connection, an HTTP/1.0 response delimited by close, or anything
// piped through nc read the last byte and then hung forever.
//
// yamux's Close is the half close this needs: from an established stream it
// sends the FIN and moves to streamLocalClose, leaving this side free to keep
// reading whatever the client is still sending. A conn that has a CloseWrite
// of its own — a TCP conn, in a test — gets that instead, since for those
// Close would take the other direction down with it.
func (p *prefixConn) CloseWrite() error {
	if cw, ok := p.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return p.Conn.Close()
}

// getRemotePK extracts the remote public key from the connection
func (s *Server) getRemotePK(conn net.Conn) (cipher.PubKey, error) {
	// Try direct type assertion first (app framework connections already use appnet.Addr)
	if rAddr, ok := conn.RemoteAddr().(appnet.Addr); ok {
		return rAddr.PubKey, nil
	}

	// Fall back to ConvertAddr for other connection types (dmsg, routing)
	addr, err := appnet.ConvertAddr(conn.RemoteAddr())
	if err != nil {
		return cipher.PubKey{}, fmt.Errorf("failed to get remote address: %w", err)
	}

	return addr.PubKey, nil
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
