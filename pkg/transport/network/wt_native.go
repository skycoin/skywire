//go:build !(js && wasm)

// Package network pkg/transport/network/wt_native.go c2-net-transport
//
// Native WT-transport carrier: dial via webtransport-go (pinning the server's
// self-signed cert by SHA-256, the browser serverCertificateHashes model), and
// accept by running a WebTransport (HTTP/3 over QUIC) server fronted as a
// net.Listener so the shared acceptTransports loop can drain it. A browser visor
// has neither (it can't run a server, and webtransport-go pulls quic-go) — see
// wt_tinygo.go.
package network

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/webtransport-go"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/skyquic"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// dialResolvedWT resolves rPK's WebTransport endpoint + pinned cert hash from the
// address resolver (a native `tp add -t wt`, or autoconnect without a table
// entry) and dials it. When the peer shares our NAT / public IP or is ourselves,
// LAN addresses are tried FIRST so same-NAT WT doesn't depend on router
// hairpinning (mirrors resolvedClient.dialVisor — WT had no such fallback, so two
// visors behind one NAT could never WT to each other); then the public endpoint,
// v6 before v4 when both are advertised. The WT bind stored endpoint + SHA-256
// cert hash via BindWT, so /resolve/wt returns both.
func (c *wtClient) dialResolvedWT(ctx context.Context, rPK cipher.PubKey) (net.Conn, error) {
	ar, _ := c.ar.(addrresolver.APIClient)
	if ar == nil {
		return nil, ErrWTEntryNotFound
	}
	vd, err := ar.Resolve(ctx, string(types.WT), rPK)
	if err != nil {
		return nil, fmt.Errorf("wt: resolve PK %s: %w", rPK, err)
	}
	if vd.CertHash == "" {
		return nil, ErrWTEntryNotFound
	}
	dialAt := func(hostport string) (net.Conn, error) {
		url := "https://" + hostport + wtPath
		c.log.Debugf("Dialing WT %v @ %s", rPK, url)
		c.announceDial(types.WT, hostport)
		return wtDial(ctx, url, vd.CertHash, vd.CertHashNext)
	}

	// Same-NAT / self / local: try LAN addresses first to avoid NAT hairpinning.
	remotePublicIP := vd.RemoteAddr
	if host, _, e := net.SplitHostPort(remotePublicIP); e == nil {
		remotePublicIP = host
	}
	isSelf := rPK == c.lPK
	samePublicIP := false
	if lip := ar.LocalPublicIP(); lip != "" && remotePublicIP != "" {
		samePublicIP = lip == remotePublicIP
	}
	if vd.IsLocal || isSelf || samePublicIP {
		for _, host := range vd.Addresses {
			if !isSelf && (host == "127.0.0.1" || host == "::1") {
				continue
			}
			if host == remotePublicIP {
				continue // the public IP is the fallback below
			}
			if conn, derr := dialAt(net.JoinHostPort(host, vd.Port)); derr == nil {
				return conn, nil
			}
		}
	}

	// Public endpoint(s): prefer v6 when both are advertised.
	addrV6 := canonicalAddr(vd.RemoteAddrV6, vd.Port)
	addrV4 := canonicalAddr(vd.RemoteAddr, vd.Port)
	if addrV6 != "" {
		if conn, derr := dialAt(addrV6); derr == nil {
			return conn, nil
		}
		if addrV4 == "" {
			return nil, fmt.Errorf("wt: v6 endpoint %s unreachable", addrV6)
		}
	}
	if addrV4 != "" {
		return dialAt(addrV4)
	}
	return nil, fmt.Errorf("wt: no dialable endpoint for %s", rPK)
}

const (
	// wtReRegisterInterval keeps the WT AR entry alive (< the AR's 2-min TTL),
	// mirroring stcpr/quic.
	wtReRegisterInterval = 90 * time.Second
	wtBindRetryDelay     = 5 * time.Second
	wtBindMaxRetryDelay  = 60 * time.Second
)

// wtAcceptTimeout bounds how long a freshly-upgraded WebTransport session may
// take to open its first (yamux-carrying) bidirectional stream.
const wtAcceptTimeout = 30 * time.Second

// wtStreamConn adapts a WebTransport bidirectional stream + its session
// addresses into a net.Conn, so the stream flows through the shared transport
// path (Noise + yamux) like any accepted/dialed conn.
type wtStreamConn struct {
	*webtransport.Stream
	local, remote net.Addr
}

func (c wtStreamConn) LocalAddr() net.Addr  { return c.local }
func (c wtStreamConn) RemoteAddr() net.Addr { return c.remote }

// wtDial dials a peer visor's WebTransport endpoint at url, pinning its
// self-signed server cert to any of certHashHex (lowercase SHA-256 hex: the
// current hash and the next, so a rotation does not break it), opens one
// bidirectional stream, and adapts it to a net.Conn. Standard CA verification is
// disabled; the cert-hash pin is authoritative, exactly as the dmsg WT carrier
// (and a browser's serverCertificateHashes) does it.
func wtDial(ctx context.Context, url string, certHashHex ...string) (net.Conn, error) {
	pins, err := skyquic.PinnedHashes(certHashHex...)
	if err != nil {
		return nil, fmt.Errorf("wt: %w", err)
	}
	tlsConf := &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // pinned by cert-hash below, browser serverCertificateHashes model
		NextProtos:         []string{skyquic.WebTransportNextProto},
		// Disable session resumption: a resumed session would skip
		// VerifyPeerCertificate and thus the cert-hash pin (gosec G123).
		ClientSessionCache:     nil,
		SessionTicketsDisabled: true,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return fmt.Errorf("wt: server presented no certificate")
			}
			if !skyquic.MatchesPinned(rawCerts[0], pins) {
				return fmt.Errorf("wt: server cert hash mismatch")
			}
			return nil
		},
	}
	d := &webtransport.Transport{
		TLSClientConfig: tlsConf,
		QUICConfig: &quic.Config{
			// Receive windows: quic-go's 768 KB default connection window caps ONE
			// connection at 768 KiB/RTT = 5.1 MB/s at 150 ms; see pkg/skyquic.
			InitialStreamReceiveWindow:       skyquic.InitialStreamReceiveWindow,
			MaxStreamReceiveWindow:           skyquic.MaxStreamReceiveWindow,
			InitialConnectionReceiveWindow:   skyquic.InitialConnectionReceiveWindow,
			MaxConnectionReceiveWindow:       skyquic.MaxConnectionReceiveWindow,
			EnableDatagrams:                  true,
			EnableStreamResetPartialDelivery: true, // required by webtransport-go
		},
	}
	_, sess, err := d.Dial(ctx, url, nil)
	if err != nil {
		return nil, fmt.Errorf("wt: dial %q: %w", url, err)
	}
	str, err := sess.OpenStreamSync(ctx)
	if err != nil {
		sess.CloseWithError(0, "open stream") //nolint:errcheck,gosec
		return nil, fmt.Errorf("wt: open stream: %w", err)
	}
	return wtStreamConn{Stream: str, local: sess.LocalAddr(), remote: sess.RemoteAddr()}, nil
}

// Start implements Client: serve incoming WT transports.
func (c *wtClient) Start() error {
	if c.connListener != nil {
		return ErrAlreadyListening
	}
	go c.serve()
	return nil
}

func (c *wtClient) serve() {
	// Shared transport_port: register the "h3" ALPN on the unified QUIC mux so WT
	// rides the same forwardable UDP socket as squicr instead of its own port.
	if m, ok := c.sharedQUIC.(*sharedQUICMux); ok && m != nil {
		lis, err := c.serveShared(m)
		if err != nil {
			c.log.Errorf("WT shared serve failed: %v", err)
			return
		}
		c.acceptTransports(lis)
		return
	}

	lis, err := newWTListener(c.listenAddr)
	if err != nil {
		c.log.Errorf("Failed to start WT listener on %q: %v", c.listenAddr, err)
		return
	}
	c.advertise(lis.Addr(), lis.cert, "")
	c.acceptTransports(lis)
}

// advertise publishes the WT endpoint at addr with cert's hash, keeps that
// hash current as the certificate rotates, and registers both with the
// address resolver so peers can discover the endpoint and pin the
// self-signed cert (WT has no CA). Like stcpr/quic, the entry is kept alive
// with a periodic re-register; a rotation re-registers at once, since a peer
// pinning the old hash cannot connect until it learns the new one.
func (c *wtClient) advertise(addr net.Addr, cert *skyquic.RotatingWebTransportCert, where string) {
	url := fmt.Sprintf("https://%s%s", addr.String(), wtPath)
	rotated := make(chan struct{}, 1)
	cert.OnRotate(func(h, next [32]byte) {
		c.setAdvertised(url, hex.EncodeToString(h[:]))
		c.log.Infof("WT certificate rotated (cert %s, next %s)", hex.EncodeToString(h[:]), hex.EncodeToString(next[:]))
		select {
		case rotated <- struct{}{}:
		default:
		}
	})
	h := cert.Hash()
	c.setAdvertised(url, hex.EncodeToString(h[:]))
	c.log.Infof("Serving WT transport at %s%s (cert %s)", url, where, hex.EncodeToString(h[:]))

	if ar, _ := c.ar.(addrresolver.APIClient); ar != nil {
		if _, port, err := net.SplitHostPort(addr.String()); err == nil {
			go c.registerWT(ar, port, cert, rotated)
		} else {
			c.log.WithError(err).Warn("WT: cannot extract port for AR registration")
		}
	}
}

// registerWT binds the WT UDP port + cert hash to the address resolver, retrying
// with backoff, then re-registers periodically to keep the entry alive, and
// whenever the certificate rotates.
func (c *wtClient) registerWT(ar addrresolver.APIClient, port string, cert *skyquic.RotatingWebTransportCert, rotated <-chan struct{}) {
	bind := func() error {
		h, next := cert.Hash(), cert.NextHash()
		return ar.BindWT(context.Background(), port, hex.EncodeToString(h[:]), hex.EncodeToString(next[:]))
	}
	delay := wtBindRetryDelay
	for {
		if err := bind(); err == nil {
			c.log.Debugf("Successfully bound WT to AR (port %s)", port)
			break
		} else {
			c.log.WithError(err).Warnf("Failed to bind WT, retrying in %v", delay)
		}
		if c.isClosed() {
			return
		}
		time.Sleep(delay)
		if delay *= 2; delay > wtBindMaxRetryDelay {
			delay = wtBindMaxRetryDelay
		}
	}
	ticker := time.NewTicker(wtReRegisterInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
		case <-rotated:
		}
		if err := bind(); err != nil {
			c.log.WithError(err).Warn("Failed to re-register WT")
		}
	}
}

// serveShared registers WT's "h3" ALPN handler on the shared QUIC multiplexer so
// WebTransport rides the unified transport_port socket (alongside squicr) instead
// of a dedicated UDP port — making WT reachable through the operator's single
// port-forward. It returns a chan-backed listener fed by accepted WT streams; the
// advertised/registered AR endpoint uses the master socket's port (transport_port).
func (c *wtClient) serveShared(m *sharedQUICMux) (net.Listener, error) {
	wt, err := sharedWebTransport(m)
	if err != nil {
		return nil, err
	}
	addr := m.localAddr()
	lis := newChanListener(addr)
	wt.mux.HandleFunc(wtPath, func(w http.ResponseWriter, r *http.Request) {
		sess, err := wt.srv.Upgrade(w, r)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), wtAcceptTimeout)
		defer cancel()
		str, err := sess.AcceptStream(ctx)
		if err != nil {
			sess.CloseWithError(0, "no stream") //nolint:errcheck,gosec
			return
		}
		lis.push(wtStreamConn{Stream: str, local: sess.LocalAddr(), remote: sess.RemoteAddr()})
	})

	c.advertise(addr, wt.cert, " on shared transport_port")
	return lis, nil
}

// sharedWT is the HTTP/3 WebTransport server on a shared QUIC socket. Each
// user mounts its own path on mux.
type sharedWT struct {
	mux  *http.ServeMux
	srv  *webtransport.Server
	cert *skyquic.RotatingWebTransportCert
}

// close stops the certificate rotation, when the socket closes.
func (w *sharedWT) close() { w.cert.Close() }

// sharedWebTransport returns m's WebTransport server, building it and
// registering the "h3" ALPN on first use. The mux's listener performs the TLS
// handshake (presenting this cert for "h3" via GetConfigForClient); each
// accepted h3 conn is then served as one WebTransport connection.
func sharedWebTransport(m *sharedQUICMux) (*sharedWT, error) {
	m.wtOnce.Do(func() {
		cert, err := skyquic.NewRotatingWebTransportCert()
		if err != nil {
			m.wtErr = fmt.Errorf("wt: generate cert: %w", err)
			return
		}
		srvMux := http.NewServeMux()
		h3 := &http3.Server{
			TLSConfig:       cert.TLSConfig(),
			Handler:         srvMux,
			EnableDatagrams: true,
			QUICConfig: &quic.Config{
				// Receive windows: quic-go's 768 KB default connection window caps ONE
				// connection at 768 KiB/RTT = 5.1 MB/s at 150 ms; see pkg/skyquic.
				InitialStreamReceiveWindow:       skyquic.InitialStreamReceiveWindow,
				MaxStreamReceiveWindow:           skyquic.MaxStreamReceiveWindow,
				InitialConnectionReceiveWindow:   skyquic.InitialConnectionReceiveWindow,
				MaxConnectionReceiveWindow:       skyquic.MaxConnectionReceiveWindow,
				EnableDatagrams:                  true,
				EnableStreamResetPartialDelivery: true,
			},
		}
		webtransport.ConfigureHTTP3Server(h3)
		wtSrv := &webtransport.Server{
			H3:          h3,
			CheckOrigin: func(*http.Request) bool { return true }, // PK auth is in Noise, not origin
		}
		handle := func(conn *quic.Conn) {
			if err := wtSrv.ServeQUICConn(conn); err != nil {
				m.log.Debugf("WT ServeQUICConn ended: %v", err)
			}
		}
		if err := m.register(wtALPN, cert.TLSConfig(), handle); err != nil {
			cert.Close()
			m.wtErr = fmt.Errorf("wt: register on shared mux: %w", err)
			return
		}
		m.wt = &sharedWT{mux: srvMux, srv: wtSrv, cert: cert}
	})
	if m.wtErr != nil {
		return nil, m.wtErr
	}
	return m.wt.(*sharedWT), nil
}

// wtListener fronts a WebTransport (HTTP/3) server as a net.Listener: the first
// bidirectional stream of each accepted WebTransport session is delivered as a
// net.Conn from Accept(). The server presents a fresh self-signed cert; its
// SHA-256 is what dialing peers pin. The certificate rotates before the
// browser-enforced 14-day limit; see skyquic.RotatingWebTransportCert.
type wtListener struct {
	addr  net.Addr
	cert  *skyquic.RotatingWebTransportCert
	conns chan net.Conn
	done  chan struct{}
	wtSrv *webtransport.Server
	once  sync.Once
}

func newWTListener(addr string) (*wtListener, error) {
	udpConn, err := net.ListenPacket("udp", addr)
	if err != nil {
		return nil, err
	}
	cert, err := skyquic.NewRotatingWebTransportCert()
	if err != nil {
		udpConn.Close() //nolint:errcheck,gosec
		return nil, fmt.Errorf("wt: generate cert: %w", err)
	}

	l := &wtListener{
		addr:  udpConn.LocalAddr(),
		cert:  cert,
		conns: make(chan net.Conn),
		done:  make(chan struct{}),
	}

	mux := http.NewServeMux()
	h3 := &http3.Server{
		TLSConfig:       cert.TLSConfig(),
		Handler:         mux,
		EnableDatagrams: true,
		QUICConfig: &quic.Config{
			// Receive windows: quic-go's 768 KB default connection window caps ONE
			// connection at 768 KiB/RTT = 5.1 MB/s at 150 ms; see pkg/skyquic.
			InitialStreamReceiveWindow:       skyquic.InitialStreamReceiveWindow,
			MaxStreamReceiveWindow:           skyquic.MaxStreamReceiveWindow,
			InitialConnectionReceiveWindow:   skyquic.InitialConnectionReceiveWindow,
			MaxConnectionReceiveWindow:       skyquic.MaxConnectionReceiveWindow,
			EnableDatagrams:                  true,
			EnableStreamResetPartialDelivery: true,
		},
	}
	webtransport.ConfigureHTTP3Server(h3)
	l.wtSrv = &webtransport.Server{
		H3:          h3,
		CheckOrigin: func(*http.Request) bool { return true }, // PK auth is in Noise, not origin
	}
	mux.HandleFunc(wtPath, l.handle)
	go l.wtSrv.Serve(udpConn) //nolint:errcheck
	return l, nil
}

func (l *wtListener) handle(w http.ResponseWriter, r *http.Request) {
	sess, err := l.wtSrv.Upgrade(w, r)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), wtAcceptTimeout)
	defer cancel()
	str, err := sess.AcceptStream(ctx)
	if err != nil {
		sess.CloseWithError(0, "no stream") //nolint:errcheck,gosec
		return
	}
	conn := wtStreamConn{Stream: str, local: sess.LocalAddr(), remote: sess.RemoteAddr()}
	select {
	case l.conns <- conn:
	case <-l.done:
		sess.CloseWithError(0, "closed") //nolint:errcheck,gosec
	}
}

// Accept implements net.Listener.
func (l *wtListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

// Close implements net.Listener.
func (l *wtListener) Close() error {
	l.once.Do(func() {
		close(l.done)
		l.cert.Close()
		_ = l.wtSrv.Close() //nolint:errcheck
	})
	return nil
}

// Addr implements net.Listener.
func (l *wtListener) Addr() net.Addr { return l.addr }

// dmsgWTServer is the part of a dmsg server (pkg/dmsg/dmsg.Server) that serves
// WebTransport sessions accepted here.
type dmsgWTServer interface {
	ServeWTSession(*webtransport.Session)
}

// SetDmsgWTServer serves a folded dmsg server's WebTransport front on the
// shared QUIC socket, at dmsg.WTPath beside the visor's own WT transport — the
// way a browser reaches a dmsg server without a TLS certificate from a CA.
// srv is typed `any`, as SetDmsgQUICServer's is. advertise is called with the
// socket's local address and the current and next certificate hashes a
// client pins, now and again whenever the certificate rotates. Reports false
// when unified UDP is not enabled, so there is nothing to serve on.
func (f *ClientFactory) SetDmsgWTServer(srv any, advertise func(addr net.Addr, certHash, nextCertHash [32]byte)) (bool, error) {
	m, ok := f.sharedQUIC.(*sharedQUICMux)
	if !ok || m == nil {
		return false, nil
	}
	s, ok := srv.(dmsgWTServer)
	if !ok {
		return false, fmt.Errorf("dmsg wt: %T does not serve WebTransport sessions", srv)
	}
	wt, err := sharedWebTransport(m)
	if err != nil {
		return false, err
	}
	wt.mux.HandleFunc(dmsg.WTPath, func(w http.ResponseWriter, r *http.Request) {
		sess, err := wt.srv.Upgrade(w, r)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		s.ServeWTSession(sess)
	})
	addr := m.localAddr()
	wt.cert.OnRotate(func(h, next [32]byte) { advertise(addr, h, next) })
	advertise(addr, wt.cert.Hash(), wt.cert.NextHash())
	return true, nil
}
