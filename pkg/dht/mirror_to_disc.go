// Package dht pkg/dht/mirror_to_disc.go
//
// DiscoveryPusher mirrors DHT writes back to HTTP discovery services.
// Runs on DMSG servers: when a visor publishes an entry to the DHT,
// the pusher forwards it to the appropriate discovery (DMSG disc, TPD,
// or SD) so the HTTP APIs stay in sync without visors needing to
// register with them directly.
package dht

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
)

// DiscoveryPusher pushes DHT items to HTTP discovery services based on salt.
type DiscoveryPusher struct {
	// endpoints maps salt → discovery base URL (e.g., "dmsg" → "http://dmsgd:9090")
	endpoints map[string]string
	client    *http.Client
	log       *logging.Logger
}

// NewDiscoveryPusher creates a pusher that forwards DHT items to discoveries.
// endpoints maps salt names to discovery base URLs:
//
//	"dmsg" → DMSG discovery SetEntry URL
//	"tp"   → Transport discovery register URL
//	"svc"  → Service discovery register URL
func NewDiscoveryPusher(endpoints map[string]string, log *logging.Logger) *DiscoveryPusher {
	return &DiscoveryPusher{
		endpoints: endpoints,
		client:    &http.Client{Timeout: 10 * time.Second},
		log:       log,
	}
}

// OnPut is the callback for Store.SetOnPut. It examines the item's salt
// and pushes the value to the corresponding discovery endpoint.
func (p *DiscoveryPusher) OnPut(target NodeID, item MutableItem) {
	salt := string(item.Salt)
	baseURL, ok := p.endpoints[salt]
	if !ok || baseURL == "" {
		return // no endpoint for this salt
	}

	// The item.V contains the JSON-encoded discovery entry.
	// Forward it to the discovery's registration endpoint.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		var endpoint string
		switch salt {
		case "dmsg":
			// DMSG discovery: POST /dmsg-discovery/entry/
			endpoint = baseURL + "/dmsg-discovery/entry/"
		case "tp":
			// Transport discovery: POST /transports/
			endpoint = baseURL + "/transports/"
		case "svc":
			// Service discovery: POST /api/services
			endpoint = baseURL + "/api/services"
		default:
			return
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(item.V))
		if err != nil {
			p.log.WithError(err).WithField("salt", salt).Debug("DHT→discovery: failed to create request")
			return
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := p.client.Do(req)
		if err != nil {
			p.log.WithError(err).WithField("salt", salt).Debug("DHT→discovery: request failed")
			return
		}
		io.Copy(io.Discard, resp.Body) //nolint:errcheck,gosec
		resp.Body.Close()              //nolint:errcheck,gosec

		if resp.StatusCode >= 300 {
			p.log.WithField("salt", salt).
				WithField("status", resp.StatusCode).
				WithField("target", fmt.Sprintf("%x", target[:8])).
				Debug("DHT→discovery: non-OK response")
		}
	}()
}

// OnPutJSON is like OnPut but re-encodes the item value before sending.
// Use when the discovery expects a different format than what's in the DHT.
func (p *DiscoveryPusher) OnPutJSON(target NodeID, item MutableItem) {
	// Verify the value is valid JSON before forwarding.
	if !json.Valid(item.V) {
		return
	}
	p.OnPut(target, item)
}
