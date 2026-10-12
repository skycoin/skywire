// Package hostbridge pkg/hostbridge/server.go c3-vis-wasm
//
// Package hostbridge lends a browser visor the host's sockets. The tab dials
// the bridge over one WebSocket, both ends run yamux on it, and each stream
// asks the bridge to dial, listen on TCP, or bind UDP on the tab's behalf.
//
// The bridge keeps no keys or state of its own, so the same small binary
// serves any visor that knows its token.
package hostbridge

import (
	"bufio"
	"errors"
	"io"
	"net"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/0magnet/yamux"
)

// DefaultAddr is where the bridge listens for the tab.
const DefaultAddr = "127.0.0.1:3438"

// DefaultOrigin is the browser visor's page.
const DefaultOrigin = "https://skywire.dev"

// Config is the bridge's access policy.
type Config struct {
	// Token is the secret the tab puts in the WebSocket path.
	Token string
	// Origins are the pages allowed to connect. "*" allows any.
	Origins []string
	Logf    func(format string, args ...any)
}

// Server is the host end of the bridge.
type Server struct {
	cfg Config
	ids atomic.Uint64
}

// NewServer returns a bridge with the given policy. An empty token is refused
// because any page could then use the host's sockets.
func NewServer(cfg Config) (*Server, error) {
	if cfg.Token == "" {
		return nil, errors.New("hostbridge: empty token")
	}
	return &Server{cfg: cfg}, nil
}

func (s *Server) logf(format string, args ...any) {
	if s.cfg.Logf != nil {
		s.cfg.Logf(format, args...)
	}
}

// Serve accepts tab connections on l until it fails.
func (s *Server) Serve(l net.Listener) error {
	for {
		c, err := l.Accept()
		if err != nil {
			return err
		}
		go s.handle(c)
	}
}

func (s *Server) handle(c net.Conn) {
	defer c.Close() //nolint:errcheck
	ws, err := s.upgrade(c)
	if err != nil {
		s.logf("refused %s: %v", c.RemoteAddr(), err)
		return
	}
	sess, err := yamux.Server(ws, muxConfig())
	if err != nil {
		return
	}
	defer sess.Close() //nolint:errcheck
	s.logf("tab connected from %s", c.RemoteAddr())
	for {
		st, err := sess.Accept()
		if err != nil {
			break
		}
		go s.serveStream(sess, st)
	}
	s.logf("tab disconnected from %s", c.RemoteAddr())
}

// muxConfig turns off keepalive, since a busy tab can stall its event loop
// for longer than the default write timeout. See pkg/wasmrpc.
func muxConfig() *yamux.Config {
	c := yamux.DefaultConfig()
	c.EnableKeepAlive = false
	c.ConnectionWriteTimeout = 5 * time.Minute
	c.LogOutput = io.Discard
	return c
}

func (s *Server) serveStream(sess *yamux.Session, st net.Conn) {
	defer st.Close() //nolint:errcheck
	br := bufio.NewReader(st)
	op, f, err := readMsg(br, 2)
	if err != nil {
		return
	}
	network, addr := f[0], f[1]
	if !validNetwork(op, network) {
		_ = writeReply(st, errors.New("hostbridge: bad request"), "", "") //nolint:errcheck
		return
	}
	switch op {
	case opDial:
		c, err := net.DialTimeout(network, addr, 20*time.Second)
		if err != nil {
			_ = writeReply(st, err, "", "") //nolint:errcheck
			return
		}
		if writeReply(st, nil, c.LocalAddr().String(), c.RemoteAddr().String()) != nil {
			_ = c.Close() //nolint:errcheck
			return
		}
		pipe(st, br, c)
	case opListen:
		l, err := net.Listen(network, addr)
		if err != nil {
			_ = writeReply(st, err, "", "") //nolint:errcheck
			return
		}
		defer l.Close() //nolint:errcheck
		id := strconv.FormatUint(s.ids.Add(1), 10)
		if writeReply(st, nil, l.Addr().String(), id) != nil {
			return
		}
		s.logf("listening on %s %s", network, l.Addr())
		go func() {
			_, _ = io.Copy(io.Discard, br) //nolint:errcheck
			_ = l.Close()                  //nolint:errcheck
		}()
		for {
			c, err := l.Accept()
			if err != nil {
				s.logf("stopped listening on %s %s", network, l.Addr())
				return
			}
			go s.forward(sess, id, c)
		}
	case opUDP:
		pc, err := net.ListenPacket(network, addr)
		if err != nil {
			_ = writeReply(st, err, "", "") //nolint:errcheck
			return
		}
		defer pc.Close() //nolint:errcheck
		if writeReply(st, nil, pc.LocalAddr().String(), "") != nil {
			return
		}
		s.logf("bound %s %s", network, pc.LocalAddr())
		go func() {
			defer st.Close() //nolint:errcheck
			buf := make([]byte, maxDatagram)
			for {
				n, a, err := pc.ReadFrom(buf)
				if err != nil {
					return
				}
				ap, ok := udpAddrPort(a)
				if !ok {
					continue
				}
				if writeDatagram(st, ap, buf[:n]) != nil {
					return
				}
			}
		}()
		buf := make([]byte, maxDatagram)
		for {
			n, ap, err := readDatagram(br, buf)
			if err != nil {
				return
			}
			_, _ = pc.WriteTo(buf[:n], net.UDPAddrFromAddrPort(ap)) //nolint:errcheck
		}
	}
}

// forward hands one inbound connection to the tab on a new stream.
func (s *Server) forward(sess *yamux.Session, id string, c net.Conn) {
	st, err := sess.Open()
	if err != nil {
		_ = c.Close() //nolint:errcheck
		return
	}
	defer st.Close() //nolint:errcheck
	if writeMsg(st, opAccepted, id, c.RemoteAddr().String()) != nil {
		_ = c.Close() //nolint:errcheck
		return
	}
	pipe(st, st, c)
}

// pipe copies both ways and passes each half-close on, so a request whose
// sender closes its write side still gets its reply.
func pipe(st net.Conn, sr io.Reader, c net.Conn) {
	defer c.Close() //nolint:errcheck
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(c, sr) //nolint:errcheck
		if tc, ok := c.(*net.TCPConn); ok {
			_ = tc.CloseWrite() //nolint:errcheck
		}
		close(done)
	}()
	_, err := io.Copy(st, c)
	_ = st.Close() //nolint:errcheck
	if err == nil {
		<-done
	}
}
