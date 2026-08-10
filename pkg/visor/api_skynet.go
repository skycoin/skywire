// Package visor pkg/visor/api_skynet.go c3-vis-core
package visor

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// SkynetHTTP performs an HTTP request over skynet using the visor's router.
func (v *Visor) SkynetHTTP(req SkynetHTTPRequest) (*SkynetHTTPResponse, error) {
	if v.router == nil {
		return nil, fmt.Errorf("router not available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Dial the remote visor over skynet using the SAME ladder the browse uses:
	// direct transport → 1-hop relay (route-0, no route-finder) → multihop route.
	// Previously this went straight to DialRoutes, so general skynet fetches
	// never used the visor-relay — only the browse UI did. Routing through the
	// shared dialer makes the relay carry ordinary skynet traffic too.
	// DialSkynet performs the skynet handshake itself, so we do not repeat it.
	dialer := &routerSkynetDialer{
		router:       v.router,
		localPK:      v.conf.PK,
		log:          v.log,
		tpM:          v.tpM,
		skynetMuxPtr: &v.skynetFwdMux,
	}
	conn, err := dialer.DialSkynet(ctx, req.PK, req.Port, nil)
	if err != nil {
		return nil, fmt.Errorf("skynet dial failed: %w", err)
	}
	defer conn.Close() //nolint:errcheck,gosec

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
