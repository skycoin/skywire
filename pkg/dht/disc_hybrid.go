package dht

import (
	"context"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
)

// HybridDiscClient wraps a DHT-backed discovery adapter with an HTTP
// fallback. Reads try DHT first, then fall back to HTTP. Writes go
// to both DHT and HTTP to keep them in sync during the transition.
type HybridDiscClient struct {
	dht  *DiscAdapter
	http disc.APIClient
	log  *logging.Logger
}

// NewHybridDiscClient creates a hybrid discovery client.
func NewHybridDiscClient(dhtAdapter *DiscAdapter, httpClient disc.APIClient, log *logging.Logger) *HybridDiscClient {
	return &HybridDiscClient{
		dht:  dhtAdapter,
		http: httpClient,
		log:  log,
	}
}

// Entry tries DHT first, falls back to HTTP.
func (h *HybridDiscClient) Entry(ctx context.Context, pk cipher.PubKey) (*disc.Entry, error) {
	entry, err := h.dht.Entry(ctx, pk)
	if err == nil {
		return entry, nil
	}
	h.log.WithField("pk", pk.String()[:8]).Debug("DHT miss, falling back to HTTP discovery")
	return h.http.Entry(ctx, pk)
}

// AvailableServers tries DHT cache first, falls back to HTTP.
func (h *HybridDiscClient) AvailableServers(ctx context.Context) ([]*disc.Entry, error) {
	servers, err := h.dht.AvailableServers(ctx)
	if err == nil && len(servers) > 0 {
		return servers, nil
	}
	return h.http.AvailableServers(ctx)
}

// AllServers tries DHT cache first, falls back to HTTP.
func (h *HybridDiscClient) AllServers(ctx context.Context) ([]*disc.Entry, error) {
	servers, err := h.dht.AllServers(ctx)
	if err == nil && len(servers) > 0 {
		return servers, nil
	}
	return h.http.AllServers(ctx)
}

// PostEntry writes to both DHT and HTTP.
func (h *HybridDiscClient) PostEntry(ctx context.Context, entry *disc.Entry) error {
	// Write to DHT (best-effort).
	if err := h.dht.PostEntry(ctx, entry); err != nil {
		h.log.WithError(err).Debug("DHT PostEntry failed (continuing with HTTP)")
	}
	return h.http.PostEntry(ctx, entry)
}

// PutEntry writes to both DHT and HTTP.
func (h *HybridDiscClient) PutEntry(ctx context.Context, sk cipher.SecKey, entry *disc.Entry) error {
	if err := h.dht.PutEntry(ctx, sk, entry); err != nil {
		h.log.WithError(err).Debug("DHT PutEntry failed (continuing with HTTP)")
	}
	return h.http.PutEntry(ctx, sk, entry)
}

// DelEntry deletes from both DHT and HTTP.
func (h *HybridDiscClient) DelEntry(ctx context.Context, entry *disc.Entry) error {
	if err := h.dht.DelEntry(ctx, entry); err != nil {
		h.log.WithError(err).Debug("DHT DelEntry failed (continuing with HTTP)")
	}
	return h.http.DelEntry(ctx, entry)
}

// AllEntries delegates to HTTP (DHT doesn't support bulk listing).
func (h *HybridDiscClient) AllEntries(ctx context.Context) ([]string, error) {
	return h.http.AllEntries(ctx)
}

// AllClientsByServer delegates to HTTP.
func (h *HybridDiscClient) AllClientsByServer(ctx context.Context) (map[string][]*disc.Entry, error) {
	return h.http.AllClientsByServer(ctx)
}

// ClientsByServer delegates to HTTP.
func (h *HybridDiscClient) ClientsByServer(ctx context.Context, pk cipher.PubKey) ([]*disc.Entry, error) {
	return h.http.ClientsByServer(ctx, pk)
}
