// Package subscriber provides a reusable CXO feed subscriber for skywire visors.
// Visors use a Subscriber to receive data updates (transports, services, DMSG
// entries) from service publishers via CXO over DMSG.
//
// Usage:
//
//	s, err := subscriber.New(dmsgClient, feedPK, conf)
//	entries := s.Entries()  // returns current cached data
//	s.OnUpdate(func(entries map[string]string) { ... })  // callback on updates
package subscriber

import (
	"encoding/json"
	"sync"

	"github.com/skycoin/dmsg/pkg/dmsg"

	"github.com/skycoin/skywire/pkg/cxo/node"
	"github.com/skycoin/skywire/pkg/cxo/node/transport"
	"github.com/skycoin/skywire/pkg/cxo/skyobject"
	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
)

// UpdateCallback is called when new data is received from the feed.
type UpdateCallback func(entries map[string]string)

// Subscriber connects to a CXO feed over DMSG and maintains a local cache.
type Subscriber struct {
	log *logging.Logger

	node   *node.Node
	feedPK cipher.PubKey

	mu       sync.RWMutex
	entries  map[string]string // key -> value cache
	callback UpdateCallback

	done chan struct{}
}

// Config configures a Subscriber.
type Config struct {
	Logger     *logging.Logger
	InMemoryDB bool
	DataDir    string
}

// DefaultConfig returns default subscriber configuration.
func DefaultConfig() Config {
	return Config{
		InMemoryDB: true,
	}
}

// New creates a new Subscriber that connects to the given feed over DMSG.
func New(dmsgC *dmsg.Client, feedPK cipher.PubKey, conf Config) (*Subscriber, error) {
	if conf.Logger == nil {
		conf.Logger = logging.MustGetLogger("cxo-subscriber")
	}

	// Create CXO node config
	nodeCfg := node.NewConfig()
	nodeCfg.Config = skyobject.NewConfig()
	nodeCfg.Config.InMemoryDB = conf.InMemoryDB
	if conf.DataDir != "" {
		nodeCfg.Config.DataDir = conf.DataDir
	}

	// Set callback to receive root updates
	nodeCfg.OnRootFilled = nil // will be set below after node creation

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

	s := &Subscriber{
		log:     conf.Logger,
		node:    cxoNode,
		feedPK:  feedPK,
		entries: make(map[string]string),
		done:    make(chan struct{}),
	}

	// Set the root filled callback — this fires when a new root is fully synced
	cxoNode.Config().OnRootFilled = func(_ *node.Node, r *registry.Root) {
		s.handleRootFilled(r)
	}

	conf.Logger.Infof("CXO subscriber created for feed %s", feedPK)
	return s, nil
}

// Connect connects to the publisher over DMSG and subscribes to the feed.
func (s *Subscriber) Connect(publisherPK cipher.PubKey) error {
	dmsgT := s.node.DMSG()
	if dmsgT == nil {
		return node.ErrAlreadyListen // TODO: better error
	}

	// Convert skywire PK to CXO PK for connection
	var swPK cipher.PubKey
	copy(swPK[:], publisherPK[:])

	conn, err := dmsgT.ConnectPK(swPK)
	if err != nil {
		return err
	}

	// Convert feed PK for subscription
	var cxoFeedPK [33]byte
	copy(cxoFeedPK[:], s.feedPK[:])

	return conn.Subscribe(cxoFeedPK)
}

// Entries returns a copy of the current cached entries.
func (s *Subscriber) Entries() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string]string, len(s.entries))
	for k, v := range s.entries {
		result[k] = v
	}
	return result
}

// Get retrieves a single entry by key.
func (s *Subscriber) Get(key string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.entries[key]
	return v, ok
}

// OnUpdate sets a callback that fires when the feed data changes.
func (s *Subscriber) OnUpdate(cb UpdateCallback) {
	s.mu.Lock()
	s.callback = cb
	s.mu.Unlock()
}

// Close shuts down the subscriber.
func (s *Subscriber) Close() error {
	close(s.done)
	return s.node.Close()
}

func (s *Subscriber) handleRootFilled(r *registry.Root) {
	if len(r.Descriptor) == 0 {
		return
	}

	// Decode the KV data from the root's Descriptor field
	var data map[string]node.KVEntry
	if err := json.Unmarshal(r.Descriptor, &data); err != nil {
		s.log.WithError(err).Warn("Failed to decode CXO root data")
		return
	}

	// Update cache
	s.mu.Lock()
	newEntries := make(map[string]string, len(data))
	for k, v := range data {
		newEntries[k] = v.Value
	}
	s.entries = newEntries
	cb := s.callback
	s.mu.Unlock()

	s.log.Debugf("Received CXO update: %d entries", len(newEntries))

	// Notify callback
	if cb != nil {
		cb(newEntries)
	}
}
