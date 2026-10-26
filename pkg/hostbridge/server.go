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
	"strings"
	"sync"
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
	// PairFile keeps the visor the bridge paired with, so a restart serves
	// only that visor. Empty keeps the pairing in memory.
	PairFile string
	Logf     func(format string, args ...any)
}

// Server is the host end of the bridge.
type Server struct {
	cfg    Config
	ids    atomic.Uint64
	mu     sync.Mutex
	paired Pairing
	// tuns are the devices this bridge opened, the only ones opNet sets up.
	tuns map[string]bool
}

// NewServer returns a bridge with the given policy. An empty token is refused
// because any page could then use the host's sockets.
func NewServer(cfg Config) (*Server, error) {
	if cfg.Token == "" {
		return nil, errors.New("hostbridge: empty token")
	}
	paired, err := loadPairing(cfg.PairFile)
	if err != nil {
		return nil, err
	}
	return &Server{cfg: cfg, paired: paired}, nil
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
	pk, err := s.serverHello(sess)
	if errors.Is(err, io.EOF) || errors.Is(err, yamux.ErrSessionShutdown) {
		// A browser opens and closes one to ask about local network access.
		s.logf("%s closed before its hello", c.RemoteAddr())
		return
	}
	if err != nil {
		s.logf("refused %s: %v", c.RemoteAddr(), err)
		// Closing the session at once can drop the refusal before it is sent.
		// The tab hangs up once it has read it.
		select {
		case <-sess.CloseChan():
		case <-time.After(helloTimeout):
		}
		return
	}
	s.logf("visor %s connected from %s", pk, c.RemoteAddr())
	for {
		st, err := sess.Accept()
		if err != nil {
			break
		}
		go s.serveStream(sess, st)
	}
	s.logf("visor %s disconnected from %s", pk, c.RemoteAddr())
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
	op, f, err := readMsg(br)
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
	case opTUN:
		s.serveTUN(st, br)
	case opNet:
		res, err := s.netOp(network, addr)
		_ = writeReply(st, err, res, "") //nolint:errcheck
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

// serveTUN opens a TUN device and moves packets between it and the stream
// until either ends. Closing the device deletes it and every route through
// it, so the host's routing falls back when the tab goes away.
func (s *Server) serveTUN(st net.Conn, br *bufio.Reader) {
	t, err := openTUN()
	if err != nil {
		_ = writeReply(st, err, "", "") //nolint:errcheck
		return
	}
	s.mu.Lock()
	if s.tuns == nil {
		s.tuns = map[string]bool{}
	}
	s.tuns[t.Name()] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.tuns, t.Name())
		s.mu.Unlock()
	}()
	if writeReply(st, nil, t.Name(), "") != nil {
		t.release()
		return
	}
	s.logf("opened %s", t.Name())
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer st.Close() //nolint:errcheck
		buf := make([]byte, maxDatagram)
		for {
			n, err := t.Read(buf)
			if err != nil {
				return
			}
			if writePacket(st, buf[:n]) != nil {
				return
			}
		}
	}()
	buf := make([]byte, maxDatagram)
	for {
		n, err := readPacket(br, buf)
		if err != nil {
			break
		}
		_, _ = t.Write(buf[:n]) //nolint:errcheck
	}
	_ = t.Close() //nolint:errcheck
	<-done
	t.release()
	s.logf("closed %s", t.Name())
}

// netOp runs one opNet request. args are space separated.
func (s *Server) netOp(cmd, args string) (string, error) {
	f := strings.Fields(args)
	switch {
	case cmd == "gateway":
		return defaultGateway()
	case cmd == "resolve" && len(f) == 1:
		return resolve4(f[0])
	case cmd == "setup" && len(f) == 3:
		s.mu.Lock()
		ours := s.tuns[f[0]]
		s.mu.Unlock()
		if !ours {
			return "", errors.New("hostbridge: " + f[0] + " is not a device this bridge opened")
		}
		mtu, err := strconv.Atoi(f[2])
		if err != nil {
			return "", err
		}
		return "", tunSetup(f[0], f[1], mtu)
	case cmd == "route" && len(f) == 3:
		s.logf("route %s %s via %s", f[0], f[1], f[2])
		return "", route(f[0], f[1], f[2])
	}
	return "", errors.New("hostbridge: bad request")
}

// resolve4 looks a name up on the host, as the browser beside it does, and
// returns its IPv4 addresses.
func resolve4(name string) (string, error) {
	ips, err := net.LookupIP(name)
	if err != nil {
		return "", err
	}
	var v4 []string
	for _, ip := range ips {
		if ip.To4() != nil {
			v4 = append(v4, ip.String())
		}
	}
	if len(v4) == 0 {
		return "", errors.New("hostbridge: no IPv4 address for " + name)
	}
	return strings.Join(v4, " "), nil
}
