// Package node pkg/cxo/node/kv.go
package node

import (
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
)

// KVEntry is a single key-value entry with metadata.
type KVEntry struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	UpdatedAt int64  `json:"updated_at"` // unix timestamp
}

// KVStore is a key-value store built on top of CXO.
// Each store is a feed identified by a public key.
// Puts create new root objects that are published to subscribers.
type KVStore struct {
	mu   sync.Mutex
	node *Node
	pk   cipher.PubKey
	sk   cipher.SecKey
	reg  *registry.Registry
	data map[string]KVEntry
}

var kvRegistry = registry.NewRegistry(func(t *registry.Reg) {
	t.Register("kv.Store", KVStore{})
})

// NewKVStore creates a new KV store on the given node.
// Generates a new keypair for the feed.
func NewKVStore(n *Node) (*KVStore, error) {
	pk, sk := cipher.GenerateKeyPair()

	if err := n.Share(pk); err != nil {
		return nil, err
	}

	return &KVStore{
		node: n,
		pk:   pk,
		sk:   sk,
		reg:  kvRegistry,
		data: make(map[string]KVEntry),
	}, nil
}

// OpenKVStore opens an existing KV store by public key.
// The secret key is required for writing.
func OpenKVStore(n *Node, pk cipher.PubKey, sk cipher.SecKey) (*KVStore, error) {
	kv := &KVStore{
		node: n,
		pk:   pk,
		sk:   sk,
		reg:  kvRegistry,
		data: make(map[string]KVEntry),
	}

	// Load existing data from the last root
	if err := kv.load(); err != nil {
		// No existing data is fine — new store
		_ = err
	}

	if !n.IsSharing(pk) {
		if err := n.Share(pk); err != nil {
			return nil, err
		}
	}

	return kv, nil
}

// Feed returns the public key of this KV store's feed.
func (kv *KVStore) Feed() cipher.PubKey {
	return kv.pk
}

// Put sets a key-value pair and publishes to subscribers.
func (kv *KVStore) Put(key, value string) error {
	kv.mu.Lock()
	defer kv.mu.Unlock()

	kv.data[key] = KVEntry{
		Key:       key,
		Value:     value,
		UpdatedAt: time.Now().Unix(),
	}

	return kv.publish()
}

// Get retrieves a value by key.
func (kv *KVStore) Get(key string) (string, bool) {
	kv.mu.Lock()
	defer kv.mu.Unlock()

	entry, ok := kv.data[key]
	if !ok {
		return "", false
	}
	return entry.Value, true
}

// Delete removes a key and publishes to subscribers.
func (kv *KVStore) Delete(key string) error {
	kv.mu.Lock()
	defer kv.mu.Unlock()

	if _, ok := kv.data[key]; !ok {
		return errors.New("key not found")
	}
	delete(kv.data, key)
	return kv.publish()
}

// List returns all key-value entries.
func (kv *KVStore) List() []KVEntry {
	kv.mu.Lock()
	defer kv.mu.Unlock()

	entries := make([]KVEntry, 0, len(kv.data))
	for _, e := range kv.data {
		entries = append(entries, e)
	}
	return entries
}

// publish encodes the current state and publishes as a new root object.
func (kv *KVStore) publish() error {
	// Encode the KV data as JSON in the root's Descriptor field.
	// This avoids the complex registry/dynamic/ref machinery
	// and keeps the data self-contained in the root object.
	data, err := json.Marshal(kv.data)
	if err != nil {
		return err
	}

	up, err := kv.node.c.Unpack(kv.sk, kv.reg)
	if err != nil {
		return err
	}
	defer up.Close() //nolint:errcheck

	nonce := kv.node.c.ActiveHead(kv.pk)
	if nonce == 0 {
		nonce = 1 // CXO requires non-zero nonce
	}

	r := &registry.Root{
		Pub:        kv.pk,
		Nonce:      nonce,
		Reg:        kv.reg.Reference(),
		Descriptor: data,
	}

	if err := kv.node.c.Save(up, r); err != nil {
		return err
	}

	kv.node.Publish(r)
	return nil
}

// load reads the latest root and decodes the KV data.
func (kv *KVStore) load() error {
	nonce := kv.node.c.ActiveHead(kv.pk)
	if nonce == 0 {
		nonce = 1
	}
	r, err := kv.node.c.LastRoot(kv.pk, nonce)
	if err != nil {
		return err
	}

	if len(r.Descriptor) == 0 {
		return nil
	}

	return json.Unmarshal(r.Descriptor, &kv.data)
}
