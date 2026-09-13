// Package dmsgsrv pkg/services/dmsgsrv/wstls.go c2-vis-appsvc
//
// Built-in wss: a dmsg server self-terminating TLS for its own
// <DNSLabel>.<suffix> host via Let's Encrypt, so a dmsg host needs no
// external reverse proxy. Shared by the standalone service (Run) and by
// the server a visor folds in on its own key (pkg/visor) — the fold
// moved the WS front onto the visor's transport port but left the TLS
// listener behind with the process that used to own it.
package dmsgsrv

import (
	"crypto/tls"
	"net"

	"golang.org/x/crypto/acme/autocert"

	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
)

// ServeWSTLS terminates TLS for wssHost on addr with an autocert-managed
// certificate cached in cacheDir, and serves dmsg-over-WebSocket there,
// advertising advertisedWSURL. It only ADDS a listener: the plain-ws port and
// the wss advert are untouched, so it coexists with an external front.
//
// Best-effort by design. If addr cannot be bound (a reverse proxy or another
// dmsg server on the host already owns it) this logs and returns nil, leaving
// the external-front path standing rather than failing startup. Requires the
// address autocert's TLS-ALPN-01 challenge runs on, i.e. :443.
//
// The returned listener is the caller's to close; nil means nothing was bound.
func ServeWSTLS(log *logging.Logger, srv *dmsg.Server, addr, cacheDir, wssHost, advertisedWSURL string) net.Listener {
	if addr == "" || wssHost == "" || cacheDir == "" {
		return nil
	}
	acm := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(wssHost),
		Cache:      autocert.DirCache(cacheDir),
	}
	tlsLis, err := tls.Listen("tcp", addr, acm.TLSConfig())
	if err != nil {
		log.WithError(err).Warnf("dmsg-ws-tls: cannot bind %s (a reverse proxy or another dmsg server may own it) — leaving TLS to an external front", addr)
		return nil
	}
	log.WithField("ws_url", advertisedWSURL).WithField("tls_addr", addr).WithField("cache", cacheDir).
		Infof("dmsg-ws-tls: self-terminating wss for %s via Let's Encrypt — no reverse proxy needed", wssHost)
	go func() {
		if serr := srv.ServeWS(tlsLis, advertisedWSURL); serr != nil {
			log.Errorf("dmsg-ws-tls ServeWS: %v", serr)
		}
	}()
	return tlsLis
}
