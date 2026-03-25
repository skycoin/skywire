// Package tpdclient implements transport discovery client.
// cxo_client.go provides a CXO-aware wrapper that uses a local CXO cache
// for read operations, falling back to HTTP when CXO data is unavailable.
package tpdclient

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cxo/subscriber"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
)

// CXOClient wraps a DiscoveryClient with a CXO subscriber cache.
// Read operations check the local CXO cache first, falling back to HTTP.
// Write operations always go through HTTP.
type CXOClient struct {
	http transport.DiscoveryClient // underlying HTTP client
	sub  *subscriber.Subscriber    // CXO subscriber (may be nil)
	log  *logging.Logger
}

// NewCXOClient creates a CXO-aware transport discovery client.
// If sub is nil, all operations fall through to the HTTP client.
func NewCXOClient(httpClient transport.DiscoveryClient, sub *subscriber.Subscriber, log *logging.Logger) transport.DiscoveryClient {
	if sub == nil {
		return httpClient // no CXO, just use HTTP
	}
	return &CXOClient{
		http: httpClient,
		sub:  sub,
		log:  log,
	}
}

// GetAllTransports returns all transports, preferring CXO cache over HTTP.
func (c *CXOClient) GetAllTransports(ctx context.Context) ([]*transport.Entry, error) {
	entries := c.sub.Entries()
	if len(entries) > 0 {
		c.log.Debugf("CXO cache hit: %d transport entries", len(entries))
		return c.decodeEntries(entries)
	}

	c.log.Debug("CXO cache empty, falling back to HTTP")
	return c.http.GetAllTransports(ctx)
}

// GetTransportsByEdge checks CXO cache first, then HTTP.
func (c *CXOClient) GetTransportsByEdge(ctx context.Context, pk cipher.PubKey) ([]*transport.Entry, error) {
	entries := c.sub.Entries()
	if len(entries) > 0 {
		// Filter locally
		pkStr := pk.String()
		var result []*transport.Entry
		for _, data := range entries {
			var entry transport.Entry
			if err := json.Unmarshal([]byte(data), &entry); err != nil {
				continue
			}
			for _, edge := range entry.Edges {
				if edge.String() == pkStr {
					entryCopy := entry
					result = append(result, &entryCopy)
					break
				}
			}
		}
		if len(result) > 0 {
			return result, nil
		}
	}

	return c.http.GetTransportsByEdge(ctx, pk)
}

// GetTransportByID checks CXO cache first, then HTTP.
func (c *CXOClient) GetTransportByID(ctx context.Context, id uuid.UUID) (*transport.Entry, error) {
	if data, ok := c.sub.Get(id.String()); ok {
		var entry transport.Entry
		if err := json.Unmarshal([]byte(data), &entry); err == nil {
			return &entry, nil
		}
	}

	return c.http.GetTransportByID(ctx, id)
}

// Write operations always go through HTTP — CXO is read-only for visors.

func (c *CXOClient) RegisterTransports(ctx context.Context, entries ...*transport.SignedEntry) error {
	return c.http.RegisterTransports(ctx, entries...)
}

func (c *CXOClient) RegisterTransportsWithSync(ctx context.Context, entries ...*transport.SignedEntry) ([]*transport.Entry, error) {
	return c.http.RegisterTransportsWithSync(ctx, entries...)
}

func (c *CXOClient) DeleteTransport(ctx context.Context, id uuid.UUID) error {
	return c.http.DeleteTransport(ctx, id)
}

func (c *CXOClient) DeleteTransports(ctx context.Context, ids []uuid.UUID) (int, error) {
	return c.http.DeleteTransports(ctx, ids)
}

func (c *CXOClient) GetTransportStats(ctx context.Context, pk cipher.PubKey) (*transport.TransportStats, error) {
	return c.http.GetTransportStats(ctx, pk)
}

func (c *CXOClient) GetAllTransportsStats(ctx context.Context) (*transport.NetworkTransportStats, error) {
	return c.http.GetAllTransportsStats(ctx)
}

func (c *CXOClient) GetAllTransportsPerKeyStats(ctx context.Context) (transport.PerKeyStats, error) {
	return c.http.GetAllTransportsPerKeyStats(ctx)
}

// decodeEntries converts CXO cache entries to transport entries.
func (c *CXOClient) decodeEntries(data map[string]string) ([]*transport.Entry, error) {
	entries := make([]*transport.Entry, 0, len(data))
	for _, raw := range data {
		var entry transport.Entry
		if err := json.Unmarshal([]byte(raw), &entry); err != nil {
			c.log.WithError(err).Debug("Skipping malformed CXO entry")
			continue
		}
		entries = append(entries, &entry)
	}
	return entries, nil
}
