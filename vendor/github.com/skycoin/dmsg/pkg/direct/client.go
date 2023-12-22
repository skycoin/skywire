// Package direct pkg/direct/client.go
package direct

import (
	"context"
	"sync"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"

	"github.com/skycoin/dmsg/pkg/disc"
)

// directClient represents a client that doesnot communicates with a dmsg-discovery,
// instead directly gets the dmsg-server info via the user or is hardcoded,
// all the data is stored in memory and
// it implements disc.APIClient
type directClient struct {
	entries map[cipher.PubKey]*disc.Entry
	mx      sync.RWMutex
}

// NewClient constructs a new APIClient that communicates with discovery via http.
func NewClient(entries []*disc.Entry, log *logging.Logger) disc.APIClient {
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

// Entry retrieves an entry associated with the given public key from the entries field of directClient.
func (c *directClient) Entry(_ context.Context, pubKey cipher.PubKey) (*disc.Entry, error) {
	c.mx.RLock()
	defer c.mx.RUnlock()
	for _, entry := range c.entries {
		if entry.Static == pubKey {
			return entry, nil
		}
	}
	return &disc.Entry{}, nil
}

// PostEntry adds a new Entry to the entries field of directClient.
func (c *directClient) PostEntry(_ context.Context, entry *disc.Entry) error {
	c.mx.Lock()
	defer c.mx.Unlock()
	c.entries[entry.Static] = entry
	return nil
}

// DelEntry deletes an Entry from the entries field of directClient.
func (c *directClient) DelEntry(_ context.Context, entry *disc.Entry) error {
	c.mx.Lock()
	defer c.mx.Unlock()
	delete(c.entries, entry.Static)
	return nil
}

// PutEntry updates Entry in the entries field of directClient.
func (c *directClient) PutEntry(_ context.Context, _ cipher.SecKey, entry *disc.Entry) error {
	c.mx.Lock()
	defer c.mx.Unlock()
	c.entries[entry.Static] = entry
	return nil
}

// AvailableServers returns list of available servers from the entries field of directClient.
func (c *directClient) AvailableServers(_ context.Context) (entries []*disc.Entry, err error) {
	c.mx.RLock()
	defer c.mx.RUnlock()
	for _, entry := range c.entries {
		if entry.Server != nil {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

// AllServers return list of all servers from the entries field of directClient
func (c *directClient) AllServers(_ context.Context) (entries []*disc.Entry, err error) {
	c.mx.RLock()
	defer c.mx.RUnlock()
	for _, entry := range c.entries {
		if entry.Server != nil {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

// AllEntries return list of all entries of directClient
func (c *directClient) AllEntries(_ context.Context) (entries []string, err error) {
	c.mx.RLock()
	defer c.mx.RUnlock()
	for _, entry := range c.entries {
		entries = append(entries, entry.Static.Hex())
	}
	return entries, nil
}
