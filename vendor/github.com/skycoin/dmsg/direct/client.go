package direct

import (
	"context"
	"sync"

	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/dmsg/disc"
)

var log = logging.MustGetLogger("direct")

// APIClient implements dmsg discovery API client.
type APIClient interface {
	Entry(context.Context, cipher.PubKey) (*disc.Entry, error)
	PostEntry(context.Context, *disc.Entry) error
	PutEntry(context.Context, cipher.SecKey, *disc.Entry) error
	DelEntry(context.Context, *disc.Entry) error
	AvailableServers(context.Context) ([]*disc.Entry, error)
}

// directClient represents a client that doesnot communicates with a dmsg-discovery, instead directly gets the dmsg-server info via the user or is hardcoded, it
// implements APIClient
type directClient struct {
	entries map[cipher.PubKey]*disc.Entry
	mx      sync.RWMutex
}

// NewDirectClient constructs a new APIClient that communicates with discovery via http.
func NewDirectClient(entries []*disc.Entry) APIClient {
	entriesMap := make(map[cipher.PubKey]*disc.Entry)
	for _, entry := range entries {
		entriesMap[entry.Static] = entry
	}
	log.WithField("func", "direct.NewClient").
		Debug("Created Direct client.")
	return &directClient{
		entries: entriesMap,
	}
}

// Entry retrieves an entry associated with the given public key.
func (c *directClient) Entry(ctx context.Context, pubKey cipher.PubKey) (*disc.Entry, error) {
	c.mx.RLock()
	defer c.mx.RUnlock()
	for _, entry := range c.entries {
		if entry.Static == pubKey {
			return entry, nil
		}
	}
	return &disc.Entry{}, nil
}

// PostEntry adds a new Entry.
func (c *directClient) PostEntry(ctx context.Context, e *disc.Entry) error {
	c.mx.Lock()
	defer c.mx.Unlock()
	var servers cipher.PubKeys

	for _, entry := range c.entries {
		if entry.Server != nil {
			servers = append(servers, entry.Static)
		}
	}

	if e.Client != nil {
		e.Client.DelegatedServers = servers
		c.entries[e.Static] = e
	}

	if e.Server != nil {
		servers = append(servers, e.Static)
		c.entries[e.Static] = e
	}

	for _, entry := range c.entries {
		if entry.Client != nil {
			entry.Client.DelegatedServers = servers
		}
	}

	return nil
}

// DelEntry deletes an Entry.
func (c *directClient) DelEntry(ctx context.Context, e *disc.Entry) error {
	c.mx.Lock()
	defer c.mx.Unlock()
	delete(c.entries, e.Static)
	return nil
}

// PutEntry updates Entry.
func (c *directClient) PutEntry(ctx context.Context, _ cipher.SecKey, entry *disc.Entry) error {
	c.mx.Lock()
	defer c.mx.Unlock()
	c.entries[entry.Static] = entry
	return nil
}

// AvailableServers returns list of available servers.
func (c *directClient) AvailableServers(ctx context.Context) (entries []*disc.Entry, err error) {
	c.mx.RLock()
	defer c.mx.RUnlock()
	for _, entry := range c.entries {
		if entry.Server != nil {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}
