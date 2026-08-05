//go:build !tinygo && !(js && wasm)

// Package dmsg pkg/dmsg/dmsg/serve_unified_quic_wt.go c1-net-dmsg
//
// ServeUnifiedQUIC serves BOTH dmsg-over-QUIC and dmsg-over-WebTransport on ONE
// UDP socket, ALPN-demuxed on a shared quic.Transport. This is what makes
// WebTransport DEFAULT-ON for a dmsg server at zero extra port cost — reachable
// wherever dmsg-over-QUIC already is (same UDP port) — instead of requiring a
// separately-bound WTAddress. It mirrors how WS cmuxes the TCP port and how the
// visor's WT shares its unified transport_port QUIC socket.
//
// A quic.Transport permits exactly ONE listener (transport.go: t.server != nil →
// errListenerAlreadySet), so dmsg-QUIC and WT CANNOT each call Listen*/ListenEarly
// on it — that was the original bug (the WT ListenEarly always failed with
// "already listening", so no server ever advertised WT). Instead we take a SINGLE
// ListenEarly whose tls.Config.GetConfigForClient returns the dmsg identity cert
// (skywire ALPN, mutual-TLS PK binding) or the WebTransport cert ("h3", no client
// cert — browsers present none) depending on the ClientHello's offered ALPNs; the
// accept loop then dispatches each connection by its negotiated ALPN.
package dmsg

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/webtransport-go"

	"github.com/skycoin/skywire/pkg/skyquic"
)

// ServeUnifiedQUIC serves dmsg-over-QUIC and (when advertisedWTURL is non-empty)
// dmsg-over-WebTransport on the same udpConn via ONE ALPN-demuxing listener:
// skywire's QUIC ALPN → native dmsg QUIC sessions (handleQUICConn); "h3" →
// WebTransport (handleWTSession). WT setup is best-effort — a cert/build failure
// logs and the server keeps serving QUIC. advertisedWTURL empty makes this behave
// exactly like ServeQUIC (QUIC only).
func (s *Server) ServeUnifiedQUIC(udpConn net.PacketConn, advertisedUDPAddr, advertisedWTURL string) error {
	identCert, err := skyquic.NewCertificate(s.pk, s.sk)
	if err != nil {
		return fmt.Errorf("dmsg-quic: identity cert: %w", err)
	}
	tr := &quic.Transport{Conn: udpConn}
	defer tr.Close() //nolint:errcheck,gosec

	identTLS := skyquic.TLSConfig(identCert, nil, nil)
	quicConf := &quic.Config{
		EnableDatagrams: true,
		MaxIdleTimeout:  60 * time.Second,
		KeepAlivePeriod: 25 * time.Second,
	}

	// Try to co-host WebTransport on the SAME listener, ALPN-demuxed. Because a
	// quic.Transport allows only one listener, dmsg-QUIC and WT share a single
	// ListenEarly: GetConfigForClient picks the identity cert (skywire ALPN) or the
	// WT cert ("h3") per handshake. Best-effort: on WT setup failure, serve QUIC only.
	tlsConf := identTLS
	var wtSrv *webtransport.Server
	var wtHash [32]byte
	if advertisedWTURL != "" {
		srv, wtTLS, hash, werr := s.buildWTServer()
		if werr != nil {
			s.log.WithError(werr).Warn("dmsg-wt: setup failed; serving QUIC only")
		} else {
			wtSrv, wtHash = srv, hash
			tlsConf = &tls.Config{ //nolint:gosec // per-ALPN sub-configs set their own MinVersion/verification
				// Union of ALPNs so the listener accepts both; GetConfigForClient
				// supplies the actual per-handshake cert + verification.
				NextProtos: []string{skyquic.NextProto, skyquic.WebTransportNextProto},
				GetConfigForClient: func(h *tls.ClientHelloInfo) (*tls.Config, error) {
					for _, p := range h.SupportedProtos {
						if p == skyquic.WebTransportNextProto { // "h3" → WebTransport
							return wtTLS, nil
						}
					}
					return identTLS, nil // skywire ALPN → native dmsg-QUIC (mutual TLS)
				},
			}
			// WebTransport needs datagrams + partial stream-reset delivery; both are
			// harmless for native dmsg-QUIC sharing the same listener config.
			quicConf.EnableStreamResetPartialDelivery = true
		}
	}

	// Regular Listen (not ListenEarly): Accept returns fully-handshaken connections,
	// so NegotiatedProtocol is set for the ALPN dispatch below and dmsg-QUIC's
	// mutual-TLS handshake completes normally. WebTransport rides 1-RTT here (no
	// 0-RTT) via ServeQUICConn, which is fine for a dmsg server.
	lis, err := tr.Listen(tlsConf, quicConf)
	if err != nil {
		return fmt.Errorf("dmsg-quic: listen: %w", err)
	}
	s.setAdvertisedUDPAddr(advertisedUDPAddr)
	s.log.WithField("addr_udp", advertisedUDPAddr).Info("Serving dmsg over QUIC.")
	if wtSrv != nil {
		s.setAdvertisedWT(advertisedWTURL, wtHash)
		s.log.WithField("addr_wt", advertisedWTURL).Info("Serving dmsg over WebTransport (shared UDP socket).")
		go func() {
			<-s.done
			_ = wtSrv.Close() //nolint:errcheck
		}()
	}

	for {
		qc, aerr := lis.Accept(context.Background())
		if aerr != nil {
			if isClosed(s.done) {
				return nil
			}
			return fmt.Errorf("dmsg-quic: accept: %w", aerr)
		}
		// Dispatch by the ALPN the connection negotiated.
		if wtSrv != nil && qc.ConnectionState().TLS.NegotiatedProtocol == skyquic.WebTransportNextProto {
			go func(c *quic.Conn) { _ = wtSrv.ServeQUICConn(c) }(qc) //nolint:errcheck
			continue
		}
		go s.handleQUICConn(qc)
	}
}

// buildWTServer constructs the WebTransport (HTTP/3) server, its TLS config and
// the serverCertificateHashes hash used to advertise it. It does NOT listen — the
// caller feeds it "h3"-ALPN connections demuxed off the shared QUIC listener via
// ServeQUICConn. Returns an error only on cert generation failure.
func (s *Server) buildWTServer() (*webtransport.Server, *tls.Config, [32]byte, error) {
	wtCert, wtHash, err := skyquic.NewWebTransportCertificate()
	if err != nil {
		return nil, nil, [32]byte{}, fmt.Errorf("wt cert: %w", err)
	}
	wtTLS := skyquic.WebTransportTLSConfig(wtCert)
	mux := http.NewServeMux()
	h3 := &http3.Server{
		TLSConfig:       wtTLS,
		Handler:         mux,
		EnableDatagrams: true,
		QUICConfig:      &quic.Config{EnableDatagrams: true, EnableStreamResetPartialDelivery: true},
	}
	webtransport.ConfigureHTTP3Server(h3)
	wtSrv := &webtransport.Server{
		H3:          h3,
		CheckOrigin: func(*http.Request) bool { return true }, // PK auth is in Noise, not origin
	}
	mux.HandleFunc(wtPath, func(w http.ResponseWriter, r *http.Request) {
		sess, uerr := wtSrv.Upgrade(w, r)
		if uerr != nil {
			s.log.WithError(uerr).Debug("dmsg-wt: upgrade failed")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		s.handleWTSession(sess)
	})
	return wtSrv, wtTLS, wtHash, nil
}
