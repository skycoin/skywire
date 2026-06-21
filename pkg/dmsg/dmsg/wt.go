//go:build !tinygo

// Package dmsg pkg/dmsg/dmsg/wt.go: dmsg-over-WebTransport.
//
// Build tag: WebTransport pulls in quic-go + webtransport-go, neither of which
// compiles under TinyGo (quic-go needs crypto/tls.QUICEncryptionLevel). It is a
// server-side + browser transport, so a TinyGo (IoT) dmsg client doesn't need
// it; the //go:build !tinygo tag keeps it out of the TinyGo graph. See
// docs/design/tinygo-dmsg-client.md.
//
// WebTransport (HTTP/3 over QUIC) is the one browser-reachable transport that
// does NOT need a CA-issued TLS certificate: the browser's WebTransport
// constructor accepts a self-signed cert via `serverCertificateHashes` (a pinned
// SHA-256). So a dmsg server can present a short-lived self-signed ECDSA cert
// (skyquic.NewWebTransportCertificate), publish its hash in discovery, and a
// browser connects to it by bare IP — no Caddy, no domain, no Let's Encrypt.
//
// Unlike dmsg-over-QUIC (which authenticates BOTH peers via mutual PK-bound
// TLS), a browser WebTransport client can't present a client certificate. So WT
// authenticates only the SERVER (cert-hash pinning); the client→server skywire
// PK is authenticated in the dmsg Noise handshake, exactly like the TCP/WS
// paths. Accordingly a WT session here is used as a single bidirectional stream
// carrying the EXISTING Noise+yamux session — the WS code path, over a different
// transport. (WT's native stream multiplexing is intentionally not used; yamux
// multiplexes inside the one WT stream, keeping one session implementation.)
package dmsg

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

	"github.com/hashicorp/yamux"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/webtransport-go"

	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/skyquic"
)

// wtPath is the HTTP/3 path the WebTransport endpoint is served on. The
// advertised Server.AddressWT URL includes it (e.g. "https://host:port/dmsg").
const wtPath = "/dmsg"

// wtStreamConn adapts a WebTransport bidirectional stream + its session
// addresses into a net.Conn, so the stream flows through the shared
// handleSession path (Noise + yamux) like any accepted TCP/WS conn.
type wtStreamConn struct {
	*webtransport.Stream
	local, remote net.Addr
}

func (c wtStreamConn) LocalAddr() net.Addr  { return c.local }
func (c wtStreamConn) RemoteAddr() net.Addr { return c.remote }

// ServeWebTransport serves dmsg over WebTransport on the given UDP socket and
// advertises advertisedWTURL + certHash in discovery (Server.AddressWT +
// CertHashWT). It runs alongside TCP/QUIC/WS Serve. cert is the short-lived
// self-signed ECDSA cert (skyquic.NewWebTransportCertificate) and certHash is
// its SHA-256 — the value a browser pins in its WebTransport
// serverCertificateHashes constructor. Blocks until the listener errors or the
// server closes.
//
// Cert rotation (WebTransport certs are valid <=14 days, browser-enforced) is
// the caller's job: generate a fresh cert + hash and re-call ServeWebTransport
// on a new socket before expiry, so the new hash is re-advertised. Keeping the
// cert in the caller (rather than generating it here) lets the caller persist
// and rotate it deterministically.
func (s *Server) ServeWebTransport(udpConn net.PacketConn, advertisedWTURL string, cert tls.Certificate, certHash [32]byte) error {
	mux := http.NewServeMux()
	h3 := &http3.Server{
		TLSConfig:       skyquic.WebTransportTLSConfig(cert),
		Handler:         mux,
		EnableDatagrams: true, // required: WebTransport runs on HTTP/3 datagrams
		QUICConfig: &quic.Config{
			EnableDatagrams:                  true,
			EnableStreamResetPartialDelivery: true, // required by webtransport-go
		},
	}
	// Advertise the WebTransport SETTINGS (SETTINGS_WT_ENABLED etc.) in the H3
	// SETTINGS frame so clients accept the CONNECT; without this the client
	// rejects with "server didn't enable WebTransport".
	webtransport.ConfigureHTTP3Server(h3)
	wtSrv := &webtransport.Server{
		H3:          h3,
		CheckOrigin: func(*http.Request) bool { return true }, // PK auth is in Noise, not origin
	}
	mux.HandleFunc(wtPath, func(w http.ResponseWriter, r *http.Request) {
		sess, err := wtSrv.Upgrade(w, r)
		if err != nil {
			s.log.WithError(err).Debug("dmsg-wt: upgrade failed")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		s.handleWTSession(sess)
	})

	s.advertisedWTAddr = advertisedWTURL
	s.advertisedWTCertHash = certHash
	go func() {
		<-s.done
		wtSrv.Close() //nolint:errcheck,gosec
	}()
	s.log.WithField("addr_wt", advertisedWTURL).Info("Serving dmsg over WebTransport.")
	serr := wtSrv.Serve(udpConn)
	if isClosed(s.done) {
		return nil
	}
	return serr
}

// handleWTSession accepts the client's first bidirectional stream and serves it
// as a normal dmsg server session (Noise + yamux over the WT stream).
func (s *Server) handleWTSession(sess *webtransport.Session) {
	ctx, cancel := context.WithTimeout(context.Background(), HandshakeTimeout)
	defer cancel()
	str, err := sess.AcceptStream(ctx)
	if err != nil {
		s.log.WithError(err).Debug("dmsg-wt: accept stream failed")
		sess.CloseWithError(0, "no stream") //nolint:errcheck,gosec
		return
	}
	conn := wtStreamConn{Stream: str, local: sess.LocalAddr(), remote: sess.RemoteAddr()}
	if s.SessionCount() >= s.maxSessions {
		s.log.WithField("max_sessions", s.maxSessions).
			Debug("dmsg-wt: max sessions reached, still accepting for delegated listeners.")
	}
	s.handleSession(conn)
}

// dialSessionWT dials a dmsg server's WebTransport endpoint (Server.AddressWT)
// and builds a yamux+Noise client session over a single bidirectional WT
// stream — the WebTransport analog of dialSessionWS. The server cert is
// self-signed with NO CA, so it is verified by pinning Server.CertHashWT (the
// SHA-256 of the cert DER, lowercase hex) exactly as a browser would via
// serverCertificateHashes; standard CA verification is disabled. This native
// path is primarily for tests and non-browser WT clients — the production
// browser client dials WT directly in JS over the same wire protocol.
func (ce *Client) dialSessionWT(ctx context.Context, entry *disc.Entry) (ClientSession, error) {
	wantHash, err := hex.DecodeString(entry.Server.CertHashWT)
	if err != nil || len(wantHash) != sha256.Size {
		return ClientSession{}, fmt.Errorf("wt: invalid cert hash %q", entry.Server.CertHashWT)
	}
	tlsConf := &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // pinned by cert-hash below, browser serverCertificateHashes model
		NextProtos:         []string{skyquic.WebTransportNextProto},
		// Disable session resumption: a resumed session would skip
		// VerifyPeerCertificate and thus the cert-hash pin (gosec G123). A fresh
		// full handshake per dial is cheap here and keeps the pin authoritative.
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
	_, sess, err := d.Dial(ctx, entry.Server.AddressWT, nil)
	if err != nil {
		return ClientSession{}, fmt.Errorf("wt: dial %q: %w", entry.Server.AddressWT, err)
	}
	str, err := sess.OpenStreamSync(ctx)
	if err != nil {
		sess.CloseWithError(0, "open stream") //nolint:errcheck,gosec
		return ClientSession{}, fmt.Errorf("wt: open stream: %w", err)
	}
	conn := wtStreamConn{Stream: str, local: sess.LocalAddr(), remote: sess.RemoteAddr()}

	dSes, err := makeClientSession(&ce.EntityCommon, ce.porter, conn, entry.Static)
	if err != nil {
		sess.CloseWithError(0, "session") //nolint:errcheck,gosec
		return ClientSession{}, err
	}
	dSes.sm.yamux, err = yamux.Client(conn, YamuxConfig())
	if err != nil {
		sess.CloseWithError(0, "yamux") //nolint:errcheck,gosec
		return ClientSession{}, err
	}
	ce.log.Infof("wt stream session initial for %s", dSes.RemotePK().String())
	return dSes, nil
}
