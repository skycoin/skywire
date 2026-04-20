// Package dht pkg/dht/backend.go
//
// Backend defines the persistence interface for the DHT store.
// Implementations must be safe for concurrent use.
package dht

// Backend persists DHT items to durable storage. The in-memory Store
// uses a Backend (when configured) to survive restarts. On startup,
// Load populates the store. On every Put, Save persists the item.
// On eviction/expiry, Delete removes it.
//
// Implementations: memBackend (default/noop), boltBackend, redisBackend.
type Backend interface {
	// Save persists a single item. Called on every successful Put/PutMirror.
	Save(target NodeID, item MutableItem) error

	// Delete removes a single item. Called on eviction or expiry.
	Delete(target NodeID) error

	// Load returns all persisted items. Called once on startup to
	// rehydrate the in-memory store.
	Load() (map[NodeID]MutableItem, error)

	// Close releases backend resources.
	Close() error
}

// memBackend is a no-op backend used when no persistence is configured.
type memBackend struct{}

func (memBackend) Save(_ NodeID, _ MutableItem) error        { return nil }
func (memBackend) Delete(_ NodeID) error                     { return nil }
func (memBackend) Load() (map[NodeID]MutableItem, error)     { return nil, nil }
func (memBackend) Close() error                              { return nil }
