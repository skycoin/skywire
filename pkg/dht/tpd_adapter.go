package dht

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
)

// Salt for transport discovery entries in the DHT.
var tpSalt = []byte("tp")

// TPDAdapter implements transport.DiscoveryClient backed by the DHT.
// Each visor publishes its own transport list under its PK with salt "tp".
type TPDAdapter struct {
	node *Node
	log  *logging.Logger
}

// NewTPDAdapter creates a transport discovery client backed by the DHT.
func NewTPDAdapter(node *Node, log *logging.Logger) *TPDAdapter {
	return &TPDAdapter{node: node, log: log}
}

// RegisterTransports publishes the given transport entries to the DHT.
// All entries are stored as a list under the node's own PK.
func (d *TPDAdapter) RegisterTransports(ctx context.Context, entries ...*transport.SignedEntry) error {
	return d.putEntries(ctx, entries)
}

// RegisterTransportsV3 publishes bare-entry transports to the DHT.
// Since the DHT stores a single list per visor target, both v2 and v3
// callers end up writing the same shape — we just wrap in SignedEntry
// internally for the existing putEntries path.
func (d *TPDAdapter) RegisterTransportsV3(ctx context.Context, version string, entries ...*transport.Entry) error {
	if len(entries) == 0 {
		return nil
	}
	signed := make([]*transport.SignedEntry, 0, len(entries))
	for _, e := range entries {
		signed = append(signed, &transport.SignedEntry{Entry: e, Version: version})
	}
	return d.putEntries(ctx, signed)
}

func (d *TPDAdapter) putEntries(ctx context.Context, entries []*transport.SignedEntry) error {
	data, err := json.Marshal(entries)
	if err != nil {
		return fmt.Errorf("dht tpd: marshal: %w", err)
	}
	if len(data) > MaxValueSize {
		return fmt.Errorf("dht tpd: transport list too large (%d bytes, max %d)", len(data), MaxValueSize)
	}
	// Wall-clock nanoseconds as monotonic seq generator. Survives restarts
	// (in-memory entry counts and bandwidth totals do not) and is virtually
	// guaranteed to climb past whatever peers cached for our PK previously.
	// On clock skew the DHT layer's per-peer rejection still keeps things
	// safe — we'll catch up next tick.
	seq := uint64(time.Now().UnixNano())
	return d.node.Put(ctx, data, seq, tpSalt)
}

// GetTransportByID searches the DHT for a transport by ID.
// This is expensive — requires fetching transport lists from multiple visors.
// Returns ErrNotFound if not located.
func (d *TPDAdapter) GetTransportByID(ctx context.Context, id uuid.UUID) (*transport.Entry, error) {
	// The DHT stores transport lists per-visor, not per-transport.
	// A full lookup by ID would require iterating all visors.
	// This is best handled by the HTTP fallback.
	return nil, fmt.Errorf("dht tpd: GetTransportByID not supported (use HTTP fallback)")
}

// GetTransportsByEdge returns all transports for a given visor PK.
func (d *TPDAdapter) GetTransportsByEdge(ctx context.Context, pk cipher.PubKey) ([]*transport.Entry, error) {
	item, err := d.node.Get(ctx, pk, tpSalt)
	if err != nil {
		return nil, err
	}

	var signed []*transport.SignedEntry
	if err := json.Unmarshal(item.V, &signed); err != nil {
		return nil, fmt.Errorf("dht tpd: unmarshal: %w", err)
	}

	entries := make([]*transport.Entry, 0, len(signed))
	for _, se := range signed {
		if se.Entry != nil {
			entries = append(entries, se.Entry)
		}
	}
	return entries, nil
}

// GetAllTransports is not efficiently supported by the DHT.
func (d *TPDAdapter) GetAllTransports(_ context.Context) ([]*transport.Entry, error) {
	return nil, nil
}

// GetTransportStats is not supported by the DHT.
func (d *TPDAdapter) GetTransportStats(_ context.Context, _ cipher.PubKey) (*transport.TransportStats, error) {
	return nil, fmt.Errorf("dht tpd: stats not supported")
}

// GetAllTransportsStats is not supported by the DHT.
func (d *TPDAdapter) GetAllTransportsStats(_ context.Context) (*transport.NetworkTransportStats, error) {
	return nil, fmt.Errorf("dht tpd: stats not supported")
}

// GetAllTransportsPerKeyStats is not supported by the DHT.
func (d *TPDAdapter) GetAllTransportsPerKeyStats(_ context.Context) (transport.PerKeyStats, error) {
	return nil, fmt.Errorf("dht tpd: stats not supported")
}

// DeleteTransport publishes an updated transport list without the given ID.
// In practice, the visor's transport manager handles this locally and
// the next RegisterTransports call will reflect the removal.
func (d *TPDAdapter) DeleteTransport(_ context.Context, _ uuid.UUID) error {
	return nil // no-op: next register cycle will publish updated list
}

// DeleteTransports is a batch version of DeleteTransport.
func (d *TPDAdapter) DeleteTransports(_ context.Context, _ []uuid.UUID) (int, error) {
	return 0, nil
}

// HybridTPDClient wraps a DHT adapter with an HTTP fallback.
type HybridTPDClient struct {
	dht  *TPDAdapter
	http transport.DiscoveryClient
	log  *logging.Logger
}

// NewHybridTPDClient creates a hybrid transport discovery client.
func NewHybridTPDClient(dhtAdapter *TPDAdapter, httpClient transport.DiscoveryClient, log *logging.Logger) *HybridTPDClient {
	return &HybridTPDClient{dht: dhtAdapter, http: httpClient, log: log}
}

// RegisterTransports writes to both DHT and HTTP.
func (h *HybridTPDClient) RegisterTransports(ctx context.Context, entries ...*transport.SignedEntry) error {
	if err := h.dht.RegisterTransports(ctx, entries...); err != nil {
		h.log.WithError(err).Debug("DHT RegisterTransports failed")
	}
	return h.http.RegisterTransports(ctx, entries...)
}

// RegisterTransportsV3 writes bare entries to both DHT and HTTP.
func (h *HybridTPDClient) RegisterTransportsV3(ctx context.Context, version string, entries ...*transport.Entry) error {
	if err := h.dht.RegisterTransportsV3(ctx, version, entries...); err != nil {
		h.log.WithError(err).Debug("DHT RegisterTransportsV3 failed")
	}
	return h.http.RegisterTransportsV3(ctx, version, entries...)
}

// GetTransportByID tries HTTP (DHT can't efficiently look up by ID).
func (h *HybridTPDClient) GetTransportByID(ctx context.Context, id uuid.UUID) (*transport.Entry, error) {
	return h.http.GetTransportByID(ctx, id)
}

// GetTransportsByEdge tries DHT first, falls back to HTTP.
func (h *HybridTPDClient) GetTransportsByEdge(ctx context.Context, pk cipher.PubKey) ([]*transport.Entry, error) {
	entries, err := h.dht.GetTransportsByEdge(ctx, pk)
	if err == nil && len(entries) > 0 {
		return entries, nil
	}
	return h.http.GetTransportsByEdge(ctx, pk)
}

// GetAllTransports delegates to HTTP.
func (h *HybridTPDClient) GetAllTransports(ctx context.Context) ([]*transport.Entry, error) {
	return h.http.GetAllTransports(ctx)
}

// GetTransportStats delegates to HTTP.
func (h *HybridTPDClient) GetTransportStats(ctx context.Context, pk cipher.PubKey) (*transport.TransportStats, error) {
	return h.http.GetTransportStats(ctx, pk)
}

// GetAllTransportsStats delegates to HTTP.
func (h *HybridTPDClient) GetAllTransportsStats(ctx context.Context) (*transport.NetworkTransportStats, error) {
	return h.http.GetAllTransportsStats(ctx)
}

// GetAllTransportsPerKeyStats delegates to HTTP.
func (h *HybridTPDClient) GetAllTransportsPerKeyStats(ctx context.Context) (transport.PerKeyStats, error) {
	return h.http.GetAllTransportsPerKeyStats(ctx)
}

// DeleteTransport writes to both.
func (h *HybridTPDClient) DeleteTransport(ctx context.Context, id uuid.UUID) error {
	return h.http.DeleteTransport(ctx, id)
}

// DeleteTransports writes to both.
func (h *HybridTPDClient) DeleteTransports(ctx context.Context, ids []uuid.UUID) (int, error) {
	return h.http.DeleteTransports(ctx, ids)
}
