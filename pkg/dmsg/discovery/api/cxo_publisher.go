// Package api pkg/dmsg/discovery/api/cxo_publisher.go
//
// CXO publisher for dmsg-discovery's "clients by server" view.
//
// The hypervisor's network visualizer needs to know "which clients
// are currently delegated to each dmsg server" — the same view
// AllClientsByServer / ClientsByServer expose over HTTP. This
// publisher mirrors that as a TreeStore feed:
//
//	clients-by-server/<server-pk>/<client-pk>/entry        // JSON disc.Entry
//	clients-by-server/<server-pk>/<client-pk>/tombstone    // JSON {"deleted_at": "..."}
//
// We deliberately do NOT publish a separate entries/<pk> tree (the
// non-denormalized form) because no consumer we know of subscribes
// to per-PK lookups; the network-visualizer pattern is always
// server-grouped, and AllClientsByServer / ClientsByServer is what
// the existing http path serves.
//
// On a client SetEntry, the publisher diffs the new DelegatedServers
// list against the old (if any) and emits:
//   - Put on clients-by-server/<server>/<client>/entry for every
//     server in the new list.
//   - Tombstone on clients-by-server/<server>/<client>/tombstone for
//     every server that was in the old list but not the new (the
//     client moved off that server).
//
// Server entries are ignored by this publisher — a server PK is the
// path-prefix bucket, not a member of the view.
package api

import (
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// json is the package-scope jsoniter.ConfigFastest value declared
// in api.go. We reuse it here to avoid pulling in encoding/json
// (which collides with the package-scope name) and to match the
// performance characteristics of the rest of dmsg-discovery's
// hot path.
var _ = json // referenced from api.go; reuse here

// ClientsByServerCXOPublisher mirrors dmsg-discovery's clients-by-
// server view into a CXO TreeStore feed. Constructed at DMSG-D
// startup when --cxo is set; the API calls into it via
// SetClientsByServerCXOPublisher.
type ClientsByServerCXOPublisher struct {
	pub *treestore.Publisher
	log *logging.Logger

	mu        sync.Mutex
	lastError error
}

// StartClientsByServerCXOPublisher constructs the publisher backed by
// the given dmsg client and DMSG-D secret key. The publisher's
// allowlist is open — the underlying HTTP routes are public reads,
// so the CXO mirror inherits that. Returns nil + error if the
// publisher can't be created (no dmsg client, listener bind failure,
// etc.); callers log and continue without it (HTTP path stays the
// source of truth).
func StartClientsByServerCXOPublisher(dmsgC *dmsg.Client, sk cipher.SecKey, logger logrus.FieldLogger) (*ClientsByServerCXOPublisher, error) {
	log := logging.MustGetLogger("dmsgd-cxo-clients-pub")

	pub, err := treestore.NewWithDMSG(dmsgC, sk, treestore.PubConfig{
		Logger:     log,
		InMemoryDB: true, // recomputed from redis on every mutation
		DmsgPort:   skyenv.DmsgDMSGDClientsByServerCXOPort,
	})
	if err != nil {
		return nil, err
	}
	pub.SetAllowlist(nil)

	p := &ClientsByServerCXOPublisher{pub: pub, log: log}
	if logger != nil {
		logger.WithField("feed_pk", pub.Feed()).WithField("dmsg_port", skyenv.DmsgDMSGDClientsByServerCXOPort).
			Info("CXO clients-by-server publisher running")
	}
	return p, nil
}

// FeedPK returns the publisher's feed PK (DMSG-D's own PK).
func (p *ClientsByServerCXOPublisher) FeedPK() cipher.PubKey { return p.pub.Feed() }

// Close stops the publisher. Safe to call multiple times.
func (p *ClientsByServerCXOPublisher) Close() error {
	if p == nil || p.pub == nil {
		return nil
	}
	return p.pub.Close()
}

// PublishSetEntry mirrors a set/update of a client entry. Emits Puts
// for every server in the new DelegatedServers list and tombstones
// for any server that dropped out (compared to old). Does nothing
// for server entries (they're path buckets here, not members) or
// for client entries with empty DelegatedServers (nothing to publish).
func (p *ClientsByServerCXOPublisher) PublishSetEntry(oldEntry, newEntry *disc.Entry) {
	if p == nil || p.pub == nil || newEntry == nil || newEntry.Client == nil {
		return
	}
	clientPK := newEntry.Static
	newServers := pkSet(newEntry.Client.DelegatedServers)
	oldServers := map[cipher.PubKey]struct{}{}
	if oldEntry != nil && oldEntry.Client != nil {
		oldServers = pkSet(oldEntry.Client.DelegatedServers)
	}

	body, err := json.Marshal(newEntry)
	if err != nil {
		p.log.WithError(err).Debug("Failed to marshal client entry leaf")
		p.recordError(err)
		return
	}

	// Put / re-Put for every server in the new list. CXO is content-
	// addressed so unchanged bytes are wire-no-ops.
	for srv := range newServers {
		path := entryLeafPath(srv, clientPK)
		if err := p.pub.Put(path, body); err != nil {
			p.log.WithError(err).WithField("path", path).Debug("Failed to publish clients-by-server entry leaf")
			p.recordError(err)
			continue
		}
		// Clear any prior tombstone for this (server, client) pair —
		// re-delegation should leave only the live leaf.
		if err := p.pub.Delete(tombstoneLeafPath(srv, clientPK)); err != nil {
			p.log.WithError(err).Debug("Failed to clear prior clients-by-server tombstone")
		}
	}

	// Tombstone every server that was in the old list but not the new.
	now := time.Now().UTC()
	tomb, err := json.Marshal(struct {
		DeletedAt time.Time `json:"deleted_at"`
	}{DeletedAt: now})
	if err != nil {
		p.log.WithError(err).Debug("Failed to marshal clients-by-server tombstone")
		return
	}
	for srv := range oldServers {
		if _, kept := newServers[srv]; kept {
			continue
		}
		entryPath := entryLeafPath(srv, clientPK)
		if err := p.pub.Delete(entryPath); err != nil {
			p.log.WithError(err).WithField("path", entryPath).
				Debug("Failed to delete dropped clients-by-server entry leaf")
		}
		tombPath := tombstoneLeafPath(srv, clientPK)
		if err := p.pub.Put(tombPath, tomb); err != nil {
			p.log.WithError(err).WithField("path", tombPath).
				Debug("Failed to publish clients-by-server tombstone")
			p.recordError(err)
		}
	}
}

// PublishDelEntry mirrors a full client-entry delete: tombstones for
// every server the client was previously delegated to. oldEntry is
// the entry being deleted (caller fetches it from store before the
// DelEntry call).
func (p *ClientsByServerCXOPublisher) PublishDelEntry(oldEntry *disc.Entry) {
	if p == nil || p.pub == nil || oldEntry == nil || oldEntry.Client == nil {
		return
	}
	clientPK := oldEntry.Static
	servers := pkSet(oldEntry.Client.DelegatedServers)
	if len(servers) == 0 {
		return
	}
	tomb, err := json.Marshal(struct {
		DeletedAt time.Time `json:"deleted_at"`
	}{DeletedAt: time.Now().UTC()})
	if err != nil {
		p.log.WithError(err).Debug("Failed to marshal clients-by-server tombstone")
		return
	}
	for srv := range servers {
		entryPath := entryLeafPath(srv, clientPK)
		if err := p.pub.Delete(entryPath); err != nil {
			p.log.WithError(err).Debug("Failed to delete clients-by-server entry leaf on full delete")
		}
		tombPath := tombstoneLeafPath(srv, clientPK)
		if err := p.pub.Put(tombPath, tomb); err != nil {
			p.log.WithError(err).Debug("Failed to publish clients-by-server tombstone on full delete")
			p.recordError(err)
		}
	}
}

func (p *ClientsByServerCXOPublisher) recordError(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastError = err
}

// LastError returns the most recent publish error, or nil.
func (p *ClientsByServerCXOPublisher) LastError() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastError
}

// EntryLeafPath / TombstoneLeafPath are exported so subscribers can
// reconstruct paths without duplicating the format string.
func EntryLeafPath(serverPK, clientPK cipher.PubKey) string {
	return entryLeafPath(serverPK, clientPK)
}
func TombstoneLeafPath(serverPK, clientPK cipher.PubKey) string {
	return tombstoneLeafPath(serverPK, clientPK)
}

func entryLeafPath(serverPK, clientPK cipher.PubKey) string {
	return fmt.Sprintf("clients-by-server/%s/%s/entry", serverPK.Hex(), clientPK.Hex())
}
func tombstoneLeafPath(serverPK, clientPK cipher.PubKey) string {
	return fmt.Sprintf("clients-by-server/%s/%s/tombstone", serverPK.Hex(), clientPK.Hex())
}

// pkSet builds a set-of-PK from a slice; small helper to avoid
// repeated O(N²) membership tests in the diff loops above.
func pkSet(pks []cipher.PubKey) map[cipher.PubKey]struct{} {
	out := make(map[cipher.PubKey]struct{}, len(pks))
	for _, pk := range pks {
		out[pk] = struct{}{}
	}
	return out
}
