// Package dht pkg/dht/backend_bolt.go
//
// BoltBackend persists DHT items to a local bbolt file.
// Suitable for visors that want DHT data to survive restarts.
package dht

import (
	"encoding/json"
	"fmt"

	"go.etcd.io/bbolt"
)

var boltBucket = []byte("dht_items")

// BoltBackend implements Backend using bbolt (an embedded key-value store).
type BoltBackend struct {
	db *bbolt.DB
}

// NewBoltBackend opens (or creates) a bbolt database at path.
func NewBoltBackend(path string) (*BoltBackend, error) {
	db, err := bbolt.Open(path, 0600, &bbolt.Options{NoSync: false})
	if err != nil {
		return nil, fmt.Errorf("dht bolt: open %s: %w", path, err)
	}
	// Ensure the bucket exists.
	if err := db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(boltBucket)
		return err
	}); err != nil {
		db.Close() //nolint:errcheck
		return nil, fmt.Errorf("dht bolt: create bucket: %w", err)
	}
	return &BoltBackend{db: db}, nil
}

// Save persists a single item.
func (b *BoltBackend) Save(target NodeID, item MutableItem) error {
	data, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("dht bolt: marshal: %w", err)
	}
	return b.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket(boltBucket).Put(target[:], data)
	})
}

// Delete removes a single item.
func (b *BoltBackend) Delete(target NodeID) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket(boltBucket).Delete(target[:])
	})
}

// Load returns all persisted items.
func (b *BoltBackend) Load() (map[NodeID]MutableItem, error) {
	items := make(map[NodeID]MutableItem)
	err := b.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(boltBucket)
		return bucket.ForEach(func(k, v []byte) error {
			if len(k) != len(NodeID{}) {
				return nil // skip malformed keys
			}
			var id NodeID
			copy(id[:], k)
			var item MutableItem
			if err := json.Unmarshal(v, &item); err != nil {
				return nil // skip corrupt entries
			}
			items[id] = item
			return nil
		})
	})
	return items, err
}

// Close closes the database.
func (b *BoltBackend) Close() error {
	return b.db.Close()
}
