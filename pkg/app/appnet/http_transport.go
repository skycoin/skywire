// Package appnet pkg/app/appnet/http_transport.go
package appnet

import (
	"bufio"
	"net"
	"net/http"

	"github.com/skycoin/skywire-utilities/pkg/logging"
)

// HTTPTransport implements http.RoundTripper
type HTTPTransport struct {
	appConn net.Conn
	log     *logging.Logger
}

// MakeHTTPTransport makes an HTTPTransport.
func MakeHTTPTransport(appConn net.Conn, log *logging.Logger) HTTPTransport {
	return HTTPTransport{
		appConn: appConn,
		log:     log,
	}
}

// RoundTrip implements golang's http package support for alternative HTTP transport protocols.
// In this case skynet is used instead of TCP to initiate the communication with the server.
func (t HTTPTransport) RoundTrip(req *http.Request) (*http.Response, error) {

	if err := req.Write(t.appConn); err != nil {
		return nil, err
	}
	bufR := bufio.NewReader(t.appConn)
	resp, err := http.ReadResponse(bufR, req)
	if err != nil {
		return nil, err
	}

	return resp, nil
}
