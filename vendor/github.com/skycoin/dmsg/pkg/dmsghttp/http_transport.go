// Package dmsghttp pkg/dmsghttp/http_transport.go
package dmsghttp

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"

	dmsg "github.com/skycoin/dmsg/pkg/dmsg"
)

const defaultHTTPPort = uint16(80)

// HTTPTransport implements http.RoundTripper
// Do not confuse this with a Skywire Transport implementation.
type HTTPTransport struct {
	ctx   context.Context
	dmsgC *dmsg.Client
}

// MakeHTTPTransport makes an HTTPTransport.
func MakeHTTPTransport(ctx context.Context, dmsgC *dmsg.Client) HTTPTransport {
	return HTTPTransport{
		ctx:   ctx,
		dmsgC: dmsgC,
	}
}

// RoundTrip implements golang's http package support for alternative HTTP transport protocols.
// In this case dmsg is used instead of TCP to initiate the communication with the server.
func (t HTTPTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var hostAddr dmsg.Addr
	if err := hostAddr.Set(req.Host); err != nil {
		return nil, fmt.Errorf("invalid host address: %w", err)
	}
	if hostAddr.Port == 0 {
		hostAddr.Port = defaultHTTPPort
	}

	stream, err := t.dmsgC.DialStream(req.Context(), hostAddr)
	if err != nil {
		return nil, err
	}
	if err := req.Write(stream); err != nil {
		return nil, err
	}
	bufR := bufio.NewReader(stream)
	resp, err := http.ReadResponse(bufR, req)
	if err != nil {
		return nil, err
	}

	// Wrap resp.Body to ensure the stream is closed when the body is closed
	resp.Body = &wrappedBody{
		ReadCloser: resp.Body,
		stream:     stream,
	}

	return resp, nil
}

// wrappedBody ensures that the DMSG stream is closed when the HTTP response body is closed.
type wrappedBody struct {
	io.ReadCloser
	stream *dmsg.Stream
}

func (wb *wrappedBody) Close() error {
	// Drain the response body up to a limit (e.g., 512KB).
	const maxDrainBytes = 512 * 1024
	_, _ = io.CopyN(io.Discard, wb.ReadCloser, maxDrainBytes) //nolint

	err1 := wb.ReadCloser.Close()
	err2 := wb.stream.Close()

	if err1 != nil {
		return err1
	}
	return err2
}
