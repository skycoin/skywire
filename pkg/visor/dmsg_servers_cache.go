// Package visor pkg/visor/dmsg_servers_cache.go
//
// DmsgServersCache persists dmsg-server discovery entries (the
// "static" PK plus the live `server.address` and metadata) to a JSON
// file in the visor's local_path. Its purpose is to remember the
// addresses learned from dmsg-discovery across visor restarts so the
// bootstrap direct client doesn't have to start from the addresses
// frozen into skywire.json (which can be months stale).
//
// Read order at startup:
//
//	1. cache file  (`<local_path>/dmsg_servers.json`)
//	2. main config (`v.conf.Dmsg.Servers` — typically /opt/skywire/skywire.json)
//
// The two are merged: for any PK present in the cache, the cached
// entry wins; PKs only in the config are kept; PKs only in the cache
// are also included. This way a server that has rotated its address
// is reached via the new address without losing PKs the cache hasn't
// seen yet.
//
// Write trigger: a background loop in initDmsg refreshes the cache
// from dmsg-discovery once dmsgC is connected, then on a fixed
// interval. If dmsgd is unreachable, the previous cache contents are
// kept (no destructive write on transient errors).
package visor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/skycoin/skywire/pkg/cipher"
	dmsgdisc "github.com/skycoin/skywire/pkg/dmsg/disc"
)

// DmsgServersCache is a thread-safe map of dmsg-server PK → entry,
// optionally backed by a JSON file on disk.
type DmsgServersCache struct {
	mu       sync.RWMutex
	entries  map[cipher.PubKey]*dmsgdisc.Entry
	filePath string // empty means in-memory only
}

// NewDmsgServersCache creates a cache backed by the given file. If
// filePath is empty the cache is in-memory only. If the file exists it
// is loaded immediately; a missing file is not an error (first run).
func NewDmsgServersCache(filePath string) *DmsgServersCache {
	c := &DmsgServersCache{
		entries:  make(map[cipher.PubKey]*dmsgdisc.Entry),
		filePath: filePath,
	}
	if filePath != "" {
		_ = c.load() // missing file or unreadable cache -> start empty
	}
	return c
}

// Set replaces the entry for one PK and saves to disk.
func (c *DmsgServersCache) Set(e *dmsgdisc.Entry) error {
	if e == nil || e.Server == nil || e.Server.Address == "" {
		return fmt.Errorf("dmsg-servers cache: invalid entry")
	}
	c.mu.Lock()
	c.entries[e.Static] = e
	c.mu.Unlock()
	return c.save()
}

// Replace swaps the entire map for the given list and saves. Entries
// without a Server.Address are dropped. Use this when a refresh
// returned a complete fresh list — PKs no longer present in dmsgd
// are pruned. An empty input list is treated as a no-op (callers
// should check for and skip empty refreshes themselves to avoid
// destroying the cache during a dmsgd outage).
func (c *DmsgServersCache) Replace(es []*dmsgdisc.Entry) error {
	if len(es) == 0 {
		return nil
	}
	fresh := make(map[cipher.PubKey]*dmsgdisc.Entry, len(es))
	for _, e := range es {
		if e == nil || e.Server == nil || e.Server.Address == "" {
			continue
		}
		fresh[e.Static] = e
	}
	if len(fresh) == 0 {
		return nil
	}
	c.mu.Lock()
	c.entries = fresh
	c.mu.Unlock()
	return c.save()
}

// All returns a copy of the cached entries as a slice.
func (c *DmsgServersCache) All() []*dmsgdisc.Entry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]*dmsgdisc.Entry, 0, len(c.entries))
	for _, e := range c.entries {
		out = append(out, e)
	}
	return out
}

// MergePreferringCache returns a list of dmsg-server entries built by
// preferring the cached entry for any PK present in the cache, and
// using the corresponding entry from `configured` for PKs only in the
// configured list. PKs in the cache but not configured are appended.
//
// Used by the bootstrap path so that on restart the visor connects
// using the address it last learned from dmsg-discovery, not whatever
// address was in skywire.json when it was generated.
func (c *DmsgServersCache) MergePreferringCache(configured []*dmsgdisc.Entry) []*dmsgdisc.Entry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]*dmsgdisc.Entry, 0, len(configured)+len(c.entries))
	seen := make(map[cipher.PubKey]bool, len(configured))
	for _, e := range configured {
		if e == nil {
			continue
		}
		if cached, ok := c.entries[e.Static]; ok {
			out = append(out, cached)
		} else {
			out = append(out, e)
		}
		seen[e.Static] = true
	}
	for pk, e := range c.entries {
		if !seen[pk] {
			out = append(out, e)
		}
	}
	return out
}

func (c *DmsgServersCache) save() error {
	if c.filePath == "" {
		return nil
	}
	c.mu.RLock()
	data, err := json.MarshalIndent(c.entries, "", "  ")
	c.mu.RUnlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.filePath), 0o750); err != nil { //nolint:gosec
		return err
	}
	return os.WriteFile(c.filePath, data, 0o600)
}

func (c *DmsgServersCache) load() error {
	data, err := os.ReadFile(c.filePath) //nolint:gosec
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return json.Unmarshal(data, &c.entries)
}
