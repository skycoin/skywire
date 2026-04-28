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
//
// DHT-stored entries are validated before being returned: any entry
// whose Static field doesn't match the requested PK, or whose
// Signature is empty, is treated as a DHT miss and the HTTP path is
// used instead. This stops corrupt DHT entries (a stale or
// mis-published value under our key) from being handed to callers
// like the dmsg client's updateClientEntry path, which would
// otherwise mutate-and-republish the corruption every cycle.
func (h *HybridDiscClient) Entry(ctx context.Context, pk cipher.PubKey) (*disc.Entry, error) {
	entry, err := h.dht.Entry(ctx, pk)
	if err == nil && validEntry(entry, pk) {
		return entry, nil
	}
	if err == nil {
		h.log.WithField("pk", pk.String()).
			Debug("DHT entry failed validation, falling back to HTTP discovery")
	} else {
		h.log.WithField("pk", pk.String()).Debug("DHT miss, falling back to HTTP discovery")
	}
	return h.http.Entry(ctx, pk)
}

// validEntry returns true if the entry's identity fields match the
// PK we asked for and the entry has been signed. Lets the hybrid
// client distinguish a usable DHT entry from a malformed one.
func validEntry(entry *disc.Entry, pk cipher.PubKey) bool {
	if entry == nil {
		return false
	}
	if entry.Static != pk {
		return false
	}
	if entry.Signature == "" {
		return false
	}
	return true
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

// PostEntry writes to both DHT and HTTP. The DHT write is gated on
// the entry being fully populated and signed — partial entries (e.g.
// internal seed-cache placeholders) shouldn't end up authoritative
// state visible to other visors.
func (h *HybridDiscClient) PostEntry(ctx context.Context, entry *disc.Entry) error {
	if shouldMirrorToDHT(entry) {
		if err := h.dht.PostEntry(ctx, entry); err != nil {
			h.log.WithError(err).Debug("DHT PostEntry failed (continuing with HTTP)")
		}
	}
	return h.http.PostEntry(ctx, entry)
}

// PutEntry writes to both DHT and HTTP. HTTP writes always go
// through; the DHT mirror is skipped for unsigned/partial entries
// (see shouldMirrorToDHT).
func (h *HybridDiscClient) PutEntry(ctx context.Context, sk cipher.SecKey, entry *disc.Entry) error {
	if shouldMirrorToDHT(entry) {
		if err := h.dht.PutEntry(ctx, sk, entry); err != nil {
			h.log.WithError(err).Debug("DHT PutEntry failed (continuing with HTTP)")
		}
	}
	return h.http.PutEntry(ctx, sk, entry)
}

// shouldMirrorToDHT returns true when an entry is safe to mirror to
// the DHT. The DHT can't validate inner entry signatures the way the
// HTTP discovery does, so anything we mirror needs to already be
// fully populated and signed by the publisher — otherwise the DHT
// ends up holding garbage that other visors will read back via
// HybridDiscClient.Entry.
func shouldMirrorToDHT(entry *disc.Entry) bool {
	if entry == nil {
		return false
	}
	if entry.Static.Null() {
		return false
	}
	if entry.Signature == "" {
		return false
	}
	return true
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
