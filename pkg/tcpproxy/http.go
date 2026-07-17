// Package tcpproxy pkg/tcpproxy/tcpproxy.go
package tcpproxy

import (
	"net"
	"net/http"
	"time"

	proxyproto "github.com/pires/go-proxyproto"
)

// ListenAndServe starts http server with tcp proxy support
func ListenAndServe(addr string, handler http.Handler) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	if addr == "" {
		addr = ":http"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	proxyListener := &proxyproto.Listener{Listener: ln, ConnPolicy: optionalProxyHeader}
	defer proxyListener.Close() // nolint:errcheck
	return srv.Serve(proxyListener)
}

// optionalProxyHeader restores go-proxyproto's pre-v0.15 lenient default (USE):
// honor a PROXY header when an upstream proxy sends one, but ALSO accept
// connections that arrive without one (direct dmsg clients doing a Noise
// handshake, health checks). v0.15 changed the zero-value default to REQUIRE,
// which rejects every header-less connection with ErrNoProxyProtocol — that
// silently broke the client-facing dmsg listener fleet-wide.
func optionalProxyHeader(proxyproto.ConnPolicyOptions) (proxyproto.Policy, error) {
	return proxyproto.USE, nil
}
