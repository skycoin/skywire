//go:build !tinygo

// Package rfclient implements client for route finder.
//
// This file holds the net/http-backed implementation (NewHTTP) and is
// native-only; the net/http-free interface and types live in types.go so
// pkg/router compiles under the TinyGo js/wasm target.
package rfclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

const defaultContextTimeout = 10 * time.Second

// APIClient implements Client interface
type apiClient struct {
	addr       string
	client     *http.Client
	apiTimeout time.Duration
	log        *logging.Logger
}

// NewHTTP constructs new Client that communicates over http.
func NewHTTP(addr string, apiTimeout time.Duration, client *http.Client, mlogger *logging.MasterLogger) Client {
	if apiTimeout == 0 {
		apiTimeout = defaultContextTimeout
	}
	log := logging.MustGetLogger("routefinder")
	if mlogger != nil {
		log = mlogger.PackageLogger("routefinder")
	}
	return &apiClient{
		addr:       sanitizedAddr(addr),
		client:     client,
		apiTimeout: apiTimeout,
		log:        log,
	}
}

// FindRoutes returns routes from source skywire visor to destiny, that has at least the given minHops and as much
// the given maxHops as well as the reverse routes from destiny to source.
func (c *apiClient) FindRoutes(ctx context.Context, rts []routing.PathEdges, opts *RouteOptions) (map[routing.PathEdges][][]routing.Hop, error) {
	requestBody := &FindRoutesRequest{
		Edges: rts,
		Opts:  opts,
	}
	marshaledBody, err := json.Marshal(requestBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, c.addr+"/routes", bytes.NewBuffer(marshaledBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")

	ctx, cancel := context.WithTimeout(ctx, c.apiTimeout)
	defer cancel()

	req = req.WithContext(ctx)

	res, err := c.client.Do(req)
	if res != nil {
		defer func() {
			if err := res.Body.Close(); err != nil {
				c.log.WithError(err).Warn("Failed to close HTTP response body")
			}
		}()
	}

	if err != nil {
		return nil, err
	}

	if res.StatusCode == http.StatusNotFound {
		return nil, ErrTransportNotFound
	}

	if res.StatusCode != http.StatusOK {
		var apiErr HTTPResponse

		err = json.NewDecoder(res.Body).Decode(&apiErr)
		if err != nil {
			// If we can't decode JSON, return HTTP status info
			return nil, fmt.Errorf("route finder error: %s (status %d)", http.StatusText(res.StatusCode), res.StatusCode)
		}

		return nil, fmt.Errorf("route finder error: %s (status %d)", apiErr.Error.Message, res.StatusCode)
	}

	var paths map[routing.PathEdges][][]routing.Hop
	err = json.NewDecoder(res.Body).Decode(&paths)
	if err != nil {
		return nil, err
	}

	return paths, nil
}

func sanitizedAddr(addr string) string {
	if addr == "" {
		return "http://localhost"
	}

	u, err := url.Parse(addr)
	if err != nil {
		return "http://localhost"
	}

	if u.Scheme == "" {
		u.Scheme = "http"
	}

	u.Path = strings.TrimSuffix(u.Path, "/")
	return u.String()
}
