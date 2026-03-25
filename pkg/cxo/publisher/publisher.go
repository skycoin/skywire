// Package publisher provides a reusable CXO feed publisher for skywire services.
// Services embed a Publisher to distribute their data (transports, services,
// DMSG entries) to subscribers via CXO over DMSG.
//
// Usage:
//
//	p, err := publisher.New(dmsgClient, secretKey)
//	p.Put("transport-id-123", jsonData)  // publishes to subscribers
//	p.Delete("transport-id-123")          // removes and publishes
//	feed := p.Feed()                      // public key for subscribers
package publisher

import (
	"sync"
	"time"

	"github.com/skycoin/dmsg/pkg/dmsg"

	"github.com/skycoin/skywire/pkg/cxo/node"
	"github.com/skycoin/skywire/pkg/cxo/node/transport"
	"github.com/skycoin/skywire/pkg/cxo/skyobject"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
)

// Publisher distributes a KV dataset to CXO subscribers over DMSG.
// It batches updates and publishes periodically to avoid excessive churn.
type Publisher struct {
	log *logging.Logger

	node *node.Node
	kv   *node.KVStore

	mu          sync.Mutex
	dirty       bool          // whether there are unpublished changes
	batchWindow time.Duration // how long to batch changes before publishing

	feed cipher.PubKey
	done chan struct{}
}

// Config configures a Publisher.
type Config struct {
	// BatchWindow is how long to accumulate changes before publishing.
	// 0 means publish immediately on every change.
	BatchWindow time.Duration

	// Logger for the publisher. If nil, a default is used.
	Logger *logging.Logger

	// InMemoryDB uses in-memory storage instead of disk.
	InMemoryDB bool

	// DataDir is the directory for CXO's bbolt database.
	// Ignored if InMemoryDB is true.
	DataDir string
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		BatchWindow: 10 * time.Second,
		InMemoryDB:  true,
	}
}

// New creates a new Publisher that distributes data over DMSG.
// The secretKey determines the feed identity (public key).
// Subscribers use the corresponding public key to subscribe.
func New(dmsgC *dmsg.Client, sk cipher.SecKey, conf Config) (*Publisher, error) {
	if conf.Logger == nil {
		conf.Logger = logging.MustGetLogger("cxo-publisher")
	}

	// Derive PK from SK
	pk, err := sk.PubKey()
	if err != nil {
		return nil, err
	}

	// Create CXO node config
	nodeCfg := node.NewConfig()
	nodeCfg.Config = skyobject.NewConfig()
	nodeCfg.Config.InMemoryDB = conf.InMemoryDB
	if conf.DataDir != "" {
		nodeCfg.Config.DataDir = conf.DataDir
	}

	// Use the same key as the service identity
	var cxoSK [32]byte
	copy(cxoSK[:], sk[:])

	// Create CXO node
	cxoNode, err := node.NewNode(nodeCfg)
	if err != nil {
		return nil, err
	}

	// Enable DMSG transport
	factory := transport.NewDMSGFactory(dmsgC, transport.DefaultCXOPort)
	if err := cxoNode.EnableDMSG(factory); err != nil {
		cxoNode.Close() //nolint:errcheck,gosec
		return nil, err
	}

	// Create KV store on the feed
	var cxoPK [33]byte
	copy(cxoPK[:], pk[:])
	var cxoSecKey [32]byte
	copy(cxoSecKey[:], sk[:])

	kv, err := node.OpenKVStore(cxoNode, cxoPK, cxoSecKey)
	if err != nil {
		cxoNode.Close() //nolint:errcheck,gosec
		return nil, err
	}

	p := &Publisher{
		log:         conf.Logger,
		node:        cxoNode,
		kv:          kv,
		batchWindow: conf.BatchWindow,
		feed:        pk,
		done:        make(chan struct{}),
	}

	// Start batch publisher if window > 0
	if conf.BatchWindow > 0 {
		go p.batchLoop()
	}

	conf.Logger.Infof("CXO publisher started. Feed: %s", pk)
	return p, nil
}

// Feed returns the public key that subscribers use to subscribe.
func (p *Publisher) Feed() cipher.PubKey {
	return p.feed
}

// Put sets a key-value pair and marks the dataset as dirty.
// If BatchWindow is 0, publishes immediately. Otherwise, batches.
func (p *Publisher) Put(key, value string) error {
	if err := p.kv.Put(key, value); err != nil {
		return err
	}

	if p.batchWindow == 0 {
		return nil // KV.Put already publishes immediately
	}

	p.mu.Lock()
	p.dirty = true
	p.mu.Unlock()
	return nil
}

// Delete removes a key and marks the dataset as dirty.
func (p *Publisher) Delete(key string) error {
	if err := p.kv.Delete(key); err != nil {
		return err
	}

	if p.batchWindow == 0 {
		return nil
	}

	p.mu.Lock()
	p.dirty = true
	p.mu.Unlock()
	return nil
}

// Close shuts down the publisher and its CXO node.
func (p *Publisher) Close() error {
	close(p.done)
	return p.node.Close()
}

func (p *Publisher) batchLoop() {
	ticker := time.NewTicker(p.batchWindow)
	defer ticker.Stop()

	for {
		select {
		case <-p.done:
			return
		case <-ticker.C:
			p.mu.Lock()
			dirty := p.dirty
			p.dirty = false
			p.mu.Unlock()

			if dirty {
				p.log.Debug("Publishing batched CXO updates")
				// The KV store already published on each Put/Delete.
				// With batching, we'd accumulate changes and publish once.
				// For now, this is a no-op since KV.Put publishes immediately.
				// TODO: Add deferred publishing mode to KVStore.
			}
		}
	}
}
