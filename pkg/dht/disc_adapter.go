package dht

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
)

// Salt used for DMSG discovery entries in the DHT.
var dmsgSalt = []byte("dmsg")

// DiscAdapter implements disc.APIClient backed by the DHT.
// It can operate as a full replacement or as a fallback layer
// alongside the centralized HTTP discovery.
type DiscAdapter struct {
	node *Node
	log  *logging.Logger

	// serverCache tracks known DMSG server entries seen via the DHT.
	// Populated when entries are fetched and identified as servers.
	serverMu    sync.RWMutex
	serverCache map[cipher.PubKey]*disc.Entry
}

// NewDiscAdapter creates a disc.APIClient backed by the DHT node.
func NewDiscAdapter(node *Node, log *logging.Logger) *DiscAdapter {
	return &DiscAdapter{
		node:        node,
		log:         log,
		serverCache: make(map[cipher.PubKey]*disc.Entry),
	}
}

// PopulateServerCache scans the local DHT store for DMSG server entries
// and populates the cache so AvailableServers() works without prior lookups.
func (d *DiscAdapter) PopulateServerCache() {
	items, _, _ := d.node.Store().GetItems("dmsg", 0, 0)
	d.serverMu.Lock()
	defer d.serverMu.Unlock()
	for _, item := range items {
		var entry disc.Entry
		if json.Unmarshal(item.V, &entry) != nil {
			continue
		}
		if entry.Server != nil && !entry.Static.Null() {
			d.serverCache[entry.Static] = &entry
		}
	}
}

// Entry retrieves a DMSG discovery entry by public key from the DHT.
func (d *DiscAdapter) Entry(ctx context.Context, pk cipher.PubKey) (*disc.Entry, error) {
	item, err := d.node.Get(ctx, pk, dmsgSalt)
	if err != nil {
		return nil, disc.ErrKeyNotFound
	}

	var entry disc.Entry
	if err := json.Unmarshal(item.V, &entry); err != nil {
		return nil, fmt.Errorf("dht disc: unmarshal entry: %w", err)
	}

	// Cache servers we discover.
	if entry.Server != nil {
		d.serverMu.Lock()
		d.serverCache[pk] = &entry
		d.serverMu.Unlock()
	}

	return &entry, nil
}

// PostEntry publishes a new DMSG discovery entry to the DHT.
func (d *DiscAdapter) PostEntry(ctx context.Context, entry *disc.Entry) error {
	return d.putEntry(ctx, entry)
}

// PutEntry updates an existing DMSG discovery entry in the DHT.
func (d *DiscAdapter) PutEntry(ctx context.Context, _ cipher.SecKey, entry *disc.Entry) error {
	// The DHT node signs with its own key. The sk parameter is for the
	// HTTP client's signature scheme — the DHT uses its own signing.
	return d.putEntry(ctx, entry)
}

// DelEntry removes a DMSG discovery entry. In the DHT, entries expire
// naturally via TTL — we publish a tombstone (empty value) with seq+1.
func (d *DiscAdapter) DelEntry(ctx context.Context, entry *disc.Entry) error {
	return d.node.Put(ctx, []byte("{}"), entry.Sequence+1, dmsgSalt)
}

func (d *DiscAdapter) putEntry(ctx context.Context, entry *disc.Entry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("dht disc: marshal entry: %w", err)
	}
	if len(data) > MaxValueSize {
		return fmt.Errorf("dht disc: entry too large (%d bytes, max %d)", len(data), MaxValueSize)
	}
	return d.node.Put(ctx, data, entry.Sequence, dmsgSalt)
}

// AvailableServers returns DMSG server entries from the local cache.
// In the DHT model, servers are discovered as peers report them.
func (d *DiscAdapter) AvailableServers(_ context.Context) ([]*disc.Entry, error) {
	d.serverMu.RLock()
	defer d.serverMu.RUnlock()

	var servers []*disc.Entry
	for _, e := range d.serverCache {
		if e.Server != nil {
			servers = append(servers, e)
		}
	}
	return servers, nil
}

// AllServers returns all known DMSG server entries.
func (d *DiscAdapter) AllServers(ctx context.Context) ([]*disc.Entry, error) {
	return d.AvailableServers(ctx)
}

// AllEntries returns all known entry PKs. In the DHT, this returns
// entries from the local store only (no network scan).
func (d *DiscAdapter) AllEntries(_ context.Context) ([]string, error) {
	// Not efficiently supported by DHT — return empty.
	// The centralized discovery should handle bulk queries.
	return nil, nil
}

// AllClientsByServer returns clients grouped by their delegated server.
// This is an expensive operation that the DHT doesn't naturally support.
func (d *DiscAdapter) AllClientsByServer(_ context.Context) (map[string][]*disc.Entry, error) {
	return nil, nil
}

// ClientsByServer returns clients delegated to a specific server.
func (d *DiscAdapter) ClientsByServer(_ context.Context, _ cipher.PubKey) ([]*disc.Entry, error) {
	return nil, nil
}

// SeedServers pre-populates the server cache with known server PKs.
// Call this during bootstrap with the hardcoded server list from config.
func (d *DiscAdapter) SeedServers(ctx context.Context, pks []cipher.PubKey) {
	for _, pk := range pks {
		entry, err := d.Entry(ctx, pk)
		if err != nil {
			d.log.WithField("pk", pk.String()[:8]).WithError(err).Debug("Failed to seed server from DHT")
			continue
		}
		if entry.Server != nil {
			d.log.WithField("pk", pk.String()[:8]).Debug("Seeded DMSG server from DHT")
		}
	}
}

// StartReannounce begins a background goroutine that refreshes the
// local item's TTL and re-pushes it to the K closest nodes every
// interval to prevent TTL expiry on remote stores.
func (d *DiscAdapter) StartReannounce(ctx context.Context, entry *disc.Entry, interval time.Duration) {
	target := (&MutableItem{K: d.node.pk, Salt: dmsgSalt}).Target()
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Refresh local TTL.
				d.node.store.Refresh(target)

				// Re-push to remote peers. Get the item from local store
				// and push it directly (bypasses Put's seq check since we
				// push to remotes only, not to our own store again).
				item := d.node.store.Get(target)
				if item == nil {
					continue
				}
				closest, _, _ := d.node.iterativeLookup(ctx, target, false) //nolint:errcheck
				for _, p := range closest {
					_ = d.node.rpcPutValue(ctx, p, *item) //nolint:errcheck
				}
			}
		}
	}()
}
