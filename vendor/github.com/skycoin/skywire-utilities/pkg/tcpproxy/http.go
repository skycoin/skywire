// Package tcpproxy pkg/tcpproxy/tcpproxy.go
package tcpproxy

import (
	"net"
	"net/http"

	proxyproto "github.com/pires/go-proxyproto"
)

// ListenAndServe starts http server with tcp proxy support
func ListenAndServe(addr string, handler http.Handler) error {
	srv := &http.Server{Addr: addr, Handler: handler} //nolint
	if addr == "" {
		addr = ":http"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	proxyListener := &proxyproto.Listener{Listener: ln}
	defer proxyListener.Close() // nolint:errcheck
	return srv.Serve(proxyListener)
}
