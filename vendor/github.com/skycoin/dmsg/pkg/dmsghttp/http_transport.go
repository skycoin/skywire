// Package dmsghttp pkg/dmsghttp/http_transport.go
package dmsghttp

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

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

	defer func() {
		go closeStream(t.ctx, resp, stream)
	}()

	return resp, nil
}

func closeStream(ctx context.Context, resp *http.Response, stream *dmsg.Stream) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, err := resp.Body.Read(nil)
			log := stream.Logger()
			// If error is not nil and is equal to ErrBodyReadAfterClose or EOF
			// then it means that the body has been closed so we close the stream
			if err != nil && (errors.Is(err, http.ErrBodyReadAfterClose) || errors.Is(err, io.EOF)) {
				err := stream.Close()
				if err != nil {
					log.Warnf("Error closing stream: %v", err)
				}
				return
			}
		}
	}

}
