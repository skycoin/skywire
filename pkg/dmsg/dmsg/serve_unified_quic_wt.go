//go:build !tinygo && !(js && wasm)

// Package dmsg pkg/dmsg/dmsg/serve_unified_quic_wt.go c1-net-dmsg
//
// ServeUnifiedQUIC serves BOTH dmsg-over-QUIC and dmsg-over-WebTransport on ONE
// UDP socket, ALPN-demuxed on a shared quic.Transport. This is what makes
// WebTransport DEFAULT-ON for a dmsg server at zero extra port cost — reachable
// wherever dmsg-over-QUIC already is (same UDP port) — instead of requiring a
// separately-bound WTAddress. It mirrors how WS cmuxes the TCP port and how the
// visor's WT shares its unified transport_port QUIC socket.
package dmsg

import (
	"context"
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
// dmsg-over-WebTransport on the same udpConn. The two are distinguished by ALPN
// on a shared quic.Transport: skywire's QUIC ALPN → native dmsg QUIC sessions
// (handleQUICConn); "h3" → WebTransport (handleWTSession). A WT cert-gen or
// listen failure is non-fatal — the server keeps serving QUIC. advertisedWTURL
// empty makes this behave exactly like ServeQUIC (QUIC only).
func (s *Server) ServeUnifiedQUIC(udpConn net.PacketConn, advertisedUDPAddr, advertisedWTURL string) error {
	identCert, err := skyquic.NewCertificate(s.pk, s.sk)
	if err != nil {
		return fmt.Errorf("dmsg-quic: identity cert: %w", err)
	}
	tr := &quic.Transport{Conn: udpConn}
	defer tr.Close() //nolint:errcheck,gosec

	quicLis, err := tr.Listen(skyquic.TLSConfig(identCert, nil, nil), &quic.Config{
		EnableDatagrams: true,
		MaxIdleTimeout:  60 * time.Second,
		KeepAlivePeriod: 25 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("dmsg-quic: listen: %w", err)
	}
	s.setAdvertisedUDPAddr(advertisedUDPAddr)
	s.log.WithField("addr_udp", advertisedUDPAddr).Info("Serving dmsg over QUIC.")

	if advertisedWTURL != "" {
		s.serveWTOnTransport(tr, advertisedWTURL)
	}

	for {
		qc, aerr := quicLis.Accept(context.Background())
		if aerr != nil {
			if isClosed(s.done) {
				return nil
			}
			return fmt.Errorf("dmsg-quic: accept: %w", aerr)
		}
		go s.handleQUICConn(qc)
	}
}

// serveWTOnTransport registers a WebTransport ("h3") listener on the already-open
// shared transport tr and serves it in the background. Best-effort: a cert or
// listen error is logged and the caller keeps serving QUIC.
func (s *Server) serveWTOnTransport(tr *quic.Transport, advertisedWTURL string) {
	wtCert, wtHash, cerr := skyquic.NewWebTransportCertificate()
	if cerr != nil {
		s.log.WithError(cerr).Warn("dmsg-wt: cert gen failed; serving QUIC only")
		return
	}
	mux := http.NewServeMux()
	h3 := &http3.Server{
		TLSConfig:       skyquic.WebTransportTLSConfig(wtCert),
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

	wtLis, lerr := tr.ListenEarly(h3.TLSConfig, h3.QUICConfig)
	if lerr != nil {
		s.log.WithError(lerr).Warn("dmsg-wt: listen on shared transport failed; serving QUIC only")
		return
	}
	s.setAdvertisedWT(advertisedWTURL, wtHash)
	s.log.WithField("addr_wt", advertisedWTURL).Info("Serving dmsg over WebTransport (shared UDP socket).")
	go func() {
		<-s.done
		_ = h3.Close()
		_ = wtLis.Close()
	}()
	go func() {
		for {
			qc, aerr := wtLis.Accept(context.Background())
			if aerr != nil {
				return
			}
			go func(c *quic.Conn) { _ = wtSrv.ServeQUICConn(c) }(qc)
		}
	}()
}
