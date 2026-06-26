//go:build !tinygo

// Package network pkg/transport/network/wt_native.go
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
	"crypto/hmac"
	"crypto/sha256"
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
	"github.com/skycoin/skywire/pkg/skyquic"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// resolveWTViaAR resolves a peer's WebTransport endpoint + pinned cert hash from
// the address resolver when there is no explicit table entry (a native
// `tp add -t wt`). The WT bind stored both (BindWT), so /resolve/wt returns the
// UDP endpoint and the SHA-256 cert hash; the URL is https://host:port/skywire.
// ok=false when there is no AR, no WT record, or no cert hash.
func (c *wtClient) resolveWTViaAR(ctx context.Context, rPK cipher.PubKey) (url, certHash string, ok bool) {
	ar, _ := c.ar.(addrresolver.APIClient)
	if ar == nil {
		return "", "", false
	}
	vd, err := ar.Resolve(ctx, string(types.WT), rPK)
	if err != nil || vd.CertHash == "" {
		return "", "", false
	}
	addr := canonicalAddr(vd.RemoteAddr, vd.Port)
	if addr == "" {
		return "", "", false
	}
	return "https://" + addr + wtPath, vd.CertHash, true
}

// wtPath is the HTTP/3 path the WebTransport endpoint is served on; the
// advertised URL includes it (e.g. "https://host:port/skywire").
const wtPath = "/skywire"

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
// self-signed server cert by certHashHex (lowercase SHA-256 hex), opens one
// bidirectional stream, and adapts it to a net.Conn. Standard CA verification is
// disabled; the cert-hash pin is authoritative, exactly as the dmsg WT carrier
// (and a browser's serverCertificateHashes) does it.
func wtDial(ctx context.Context, url, certHashHex string) (net.Conn, error) {
	wantHash, err := hex.DecodeString(certHashHex)
	if err != nil || len(wantHash) != sha256.Size {
		return nil, fmt.Errorf("wt: invalid cert hash %q", certHashHex)
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
			got := sha256.Sum256(rawCerts[0])
			if !hmac.Equal(got[:], wantHash) {
				return fmt.Errorf("wt: server cert hash mismatch")
			}
			return nil
		},
	}
	d := &webtransport.Dialer{
		TLSClientConfig: tlsConf,
		QUICConfig: &quic.Config{
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
	lis, err := newWTListener(c.listenAddr)
	if err != nil {
		c.log.Errorf("Failed to start WT listener on %q: %v", c.listenAddr, err)
		return
	}
	c.advertisedURL = fmt.Sprintf("https://%s%s", lis.Addr().String(), wtPath)
	c.advertisedCertHash = hex.EncodeToString(lis.certHash[:])
	c.log.Infof("Serving WT transport at %s (cert %s)", c.advertisedURL, c.advertisedCertHash)

	// Register the WT endpoint + cert hash with the address resolver so peers can
	// discover it and pin the self-signed cert (WT has no CA). Like stcpr/quic,
	// keep the entry alive with a periodic re-register.
	if ar, _ := c.ar.(addrresolver.APIClient); ar != nil {
		if _, port, err := net.SplitHostPort(lis.Addr().String()); err == nil {
			go c.registerWT(ar, port, c.advertisedCertHash)
		} else {
			c.log.WithError(err).Warn("WT: cannot extract port for AR registration")
		}
	}

	c.acceptTransports(lis)
}

// registerWT binds the WT UDP port + cert hash to the address resolver, retrying
// with backoff, then re-registers periodically to keep the entry alive.
func (c *wtClient) registerWT(ar addrresolver.APIClient, port, certHash string) {
	delay := wtBindRetryDelay
	for {
		if err := ar.BindWT(context.Background(), port, certHash); err == nil {
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
			if err := ar.BindWT(context.Background(), port, certHash); err != nil {
				c.log.WithError(err).Warn("Failed to re-register WT")
			}
		}
	}
}

// wtListener fronts a WebTransport (HTTP/3) server as a net.Listener: the first
// bidirectional stream of each accepted WebTransport session is delivered as a
// net.Conn from Accept(). The server presents a fresh self-signed cert; its
// SHA-256 (certHash) is what dialing peers pin. Cert rotation (WebTransport certs
// are browser-capped at <=14 days) is a wiring concern for a longer-lived
// listener; this listener holds one cert for its lifetime.
type wtListener struct {
	addr     net.Addr
	certHash [32]byte
	conns    chan net.Conn
	done     chan struct{}
	wtSrv    *webtransport.Server
	once     sync.Once
}

func newWTListener(addr string) (*wtListener, error) {
	udpConn, err := net.ListenPacket("udp", addr)
	if err != nil {
		return nil, err
	}
	cert, certHash, err := skyquic.NewWebTransportCertificate()
	if err != nil {
		udpConn.Close() //nolint:errcheck,gosec
		return nil, fmt.Errorf("wt: generate cert: %w", err)
	}

	l := &wtListener{
		addr:     udpConn.LocalAddr(),
		certHash: certHash,
		conns:    make(chan net.Conn),
		done:     make(chan struct{}),
	}

	mux := http.NewServeMux()
	h3 := &http3.Server{
		TLSConfig:       skyquic.WebTransportTLSConfig(cert),
		Handler:         mux,
		EnableDatagrams: true,
		QUICConfig: &quic.Config{
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
		_ = l.wtSrv.Close() //nolint:errcheck
	})
	return nil
}

// Addr implements net.Listener.
func (l *wtListener) Addr() net.Addr { return l.addr }
