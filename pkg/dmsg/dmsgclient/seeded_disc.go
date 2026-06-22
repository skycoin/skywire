// Package dmsgclient pkg/dmsg/dmsgclient/seeded_disc.go
package dmsgclient

import (
	"context"
	"fmt"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
)

// seededDiscClient is the discovery client for a seeded dmsg client (browser /
// standalone). It speaks to the REAL discovery for everything — registering its
// own entry, resolving arbitrary peers, and fetching the live server list — and
// keeps a small `shortcut` map ONLY for the bootstrap/recursion-avoidance set:
// the discovery's own PK (so reaching the discovery never recurses through the
// discovery) and the seed server PKs (so the first session can be dialed).
//
// This replaces the direct-first RegisteringFallbackDiscClient on the seeded
// path. That one resolved the client's OWN pk from a synthetic direct entry, so
// the dmsg client's registration logic (initilizeClientEntry / updateClientEntry)
// saw a fake "already registered" entry and skipped the real PostEntry — the
// client connected to a server but never actually registered in discovery. Here
// the own pk resolves against the real discovery (404 when absent), so the
// register-fresh-entry path fires; and AvailableServers returns the live
// discovery list (falling back to the seeds before a session exists).
type seededDiscClient struct {
	real     disc.APIClient
	shortcut map[cipher.PubKey]*disc.Entry
	seeds    []*disc.Entry
	log      *logging.Logger
}

// NewSeededDiscClient builds the seeded discovery client. shortcut must contain
// the discovery PK + seed server entries; seeds is the seed server list used as
// the AvailableServers/AllServers bootstrap fallback before the real discovery
// is reachable.
func NewSeededDiscClient(real disc.APIClient, shortcut map[cipher.PubKey]*disc.Entry, seeds []*disc.Entry, log *logging.Logger) disc.APIClient {
	return &seededDiscClient{real: real, shortcut: shortcut, seeds: seeds, log: log}
}

func (c *seededDiscClient) Entry(ctx context.Context, pk cipher.PubKey) (*disc.Entry, error) {
	if e, ok := c.shortcut[pk]; ok {
		return e, nil
	}
	return c.real.Entry(ctx, pk)
}

func (c *seededDiscClient) PostEntry(ctx context.Context, entry *disc.Entry) error {
	return c.real.PostEntry(ctx, entry)
}

func (c *seededDiscClient) PutEntry(ctx context.Context, sk cipher.SecKey, entry *disc.Entry) error {
	return c.real.PutEntry(ctx, sk, entry)
}

func (c *seededDiscClient) DelEntry(ctx context.Context, entry *disc.Entry) error {
	return c.real.DelEntry(ctx, entry)
}

// AvailableServers returns the live discovery server list, falling back to the
// seeds before a session exists (so the bootstrap dial always has a target).
func (c *seededDiscClient) AvailableServers(ctx context.Context) ([]*disc.Entry, error) {
	srv, err := c.real.AvailableServers(ctx)
	if err != nil || len(srv) == 0 {
		jslog(fmt.Sprintf("seeded.AvailableServers -> seeds(%d) (real err=%v n=%d)", len(c.seeds), err, len(srv)))
		return c.seeds, nil
	}
	jslog(fmt.Sprintf("seeded.AvailableServers -> real(%d)", len(srv)))
	return srv, nil
}

func (c *seededDiscClient) AllServers(ctx context.Context) ([]*disc.Entry, error) {
	srv, err := c.real.AllServers(ctx)
	if err != nil || len(srv) == 0 {
		return c.seeds, nil
	}
	return srv, nil
}

func (c *seededDiscClient) AllEntries(ctx context.Context) ([]string, error) {
	return c.real.AllEntries(ctx)
}

func (c *seededDiscClient) AllClientsByServer(ctx context.Context) (map[string][]*disc.Entry, error) {
	return c.real.AllClientsByServer(ctx)
}

func (c *seededDiscClient) ClientsByServer(ctx context.Context, serverPK cipher.PubKey) ([]*disc.Entry, error) {
	return c.real.ClientsByServer(ctx, serverPK)
}
