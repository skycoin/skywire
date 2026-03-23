// Package node pkg/cxo/node/kv_rpc.go
package node

import (
	"errors"
	"sync"

	"github.com/skycoin/skycoin/src/cipher"
)

// KVRPC is the RPC interface for the key-value store.
type KVRPC struct {
	n      *Node
	mu     sync.Mutex
	stores map[cipher.PubKey]*KVStore
}

func newKVRPC(n *Node) *KVRPC {
	return &KVRPC{
		n:      n,
		stores: make(map[cipher.PubKey]*KVStore),
	}
}

func (r *KVRPC) getOrCreate(pk cipher.PubKey, sk cipher.SecKey) (*KVStore, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if kv, ok := r.stores[pk]; ok {
		return kv, nil
	}

	kv, err := OpenKVStore(r.n, pk, sk)
	if err != nil {
		return nil, err
	}
	r.stores[pk] = kv
	return kv, nil
}

func (r *KVRPC) getReadOnly(pk cipher.PubKey) (*KVStore, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if kv, ok := r.stores[pk]; ok {
		return kv, nil
	}

	// For read-only, create a store that loads from the last root
	// Use the node's own identity key (won't be used for writing)
	kv := &KVStore{
		node: r.n,
		pk:   pk,
		data: make(map[string]KVEntry),
	}
	if err := kv.load(); err != nil {
		return nil, err
	}
	r.stores[pk] = kv
	return kv, nil
}

// KVCreateResult is the result of creating a new KV store.
type KVCreateResult struct {
	Feed   cipher.PubKey
	Secret cipher.SecKey
}

// Create creates a new KV store and returns the feed keypair.
func (r *KVRPC) Create(_ struct{}, result *KVCreateResult) error {
	kv, err := NewKVStore(r.n)
	if err != nil {
		return err
	}

	r.mu.Lock()
	r.stores[kv.pk] = kv
	r.mu.Unlock()

	result.Feed = kv.pk
	result.Secret = kv.sk
	return nil
}

// KVPutInput is the input for Put.
type KVPutInput struct {
	Feed   cipher.PubKey
	Secret cipher.SecKey
	Key    string
	Value  string
}

// Put sets a key-value pair in the store.
func (r *KVRPC) Put(in KVPutInput, _ *struct{}) error {
	if in.Key == "" {
		return errors.New("key cannot be empty")
	}

	kv, err := r.getOrCreate(in.Feed, in.Secret)
	if err != nil {
		return err
	}

	return kv.Put(in.Key, in.Value)
}

// KVGetInput is the input for Get.
type KVGetInput struct {
	Feed cipher.PubKey
	Key  string
}

// KVGetResult is the result of Get.
type KVGetResult struct {
	Value string
	Found bool
}

// Get retrieves a value by key.
func (r *KVRPC) Get(in KVGetInput, result *KVGetResult) error {
	kv, err := r.getReadOnly(in.Feed)
	if err != nil {
		return err
	}

	result.Value, result.Found = kv.Get(in.Key)
	return nil
}

// KVDeleteInput is the input for Delete.
type KVDeleteInput struct {
	Feed   cipher.PubKey
	Secret cipher.SecKey
	Key    string
}

// Delete removes a key from the store.
func (r *KVRPC) Delete(in KVDeleteInput, _ *struct{}) error {
	kv, err := r.getOrCreate(in.Feed, in.Secret)
	if err != nil {
		return err
	}

	return kv.Delete(in.Key)
}

// List returns all entries in a feed's KV store.
func (r *KVRPC) List(feed cipher.PubKey, entries *[]KVEntry) error {
	kv, err := r.getReadOnly(feed)
	if err != nil {
		return err
	}

	*entries = kv.List()
	return nil
}
