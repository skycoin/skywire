// Package visor api_skynet.go — SkynetHTTP RPC method
package visor

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skynetweb"
)

// SkynetHTTP performs an HTTP request over skynet using the visor's router.
func (v *Visor) SkynetHTTP(req SkynetHTTPRequest) (*SkynetHTTPResponse, error) {
	if v.router == nil {
		return nil, fmt.Errorf("router not available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Dial the remote visor via skynet routing
	conn, err := v.router.DialRoutes(ctx, req.PK, 0, routing.Port(skyenv.SkyForwardingServerPort), nil)
	if err != nil {
		return nil, fmt.Errorf("skynet dial failed: %w", err)
	}
	defer conn.Close() //nolint:errcheck,gosec

	// Perform skynet handshake
	if err := skynetweb.PerformHandshake(conn, req.Port); err != nil {
		return nil, fmt.Errorf("skynet handshake failed: %w", err)
	}

	// Build HTTP request
	method := req.Method
	if method == "" {
		method = "GET"
	}
	path := req.Path
	if path == "" {
		path = "/"
	}

	var bodyReader io.Reader
	if len(req.Body) > 0 {
		bodyReader = bytes.NewReader(req.Body)
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Host = fmt.Sprintf("%s:%d", req.PK.Hex(), req.Port)
	for k, val := range req.Header {
		httpReq.Header.Set(k, val)
	}

	// Send request
	if err := httpReq.Write(conn); err != nil {
		return nil, fmt.Errorf("request write failed: %w", err)
	}

	// Read response
	resp, err := http.ReadResponse(bufio.NewReader(conn), httpReq)
	if err != nil {
		return nil, fmt.Errorf("response read failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	headers := make(map[string]string)
	for k := range resp.Header {
		headers[k] = resp.Header.Get(k)
	}

	return &SkynetHTTPResponse{
		StatusCode: resp.StatusCode,
		Status:     resp.Status,
		Header:     headers,
		Body:       body,
	}, nil
}
