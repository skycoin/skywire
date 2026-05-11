// Package api pkg/service-discovery/api/cxo_publisher.go
//
// CXO publisher for service-discovery's services tree.
//
// Subscribers (the hypervisor's network visualizer + tab-specific
// consumers) read the live set of registered services from a single
// TreeStore feed instead of polling /api/services?type=... over HTTP.
// Tree shape:
//
//	services/<type>/<pk>/entry        // JSON-encoded servicedisc.Service
//	services/<type>/<pk>/tombstone    // JSON {"deleted_at": "..."}
//
// Type-as-prefix lets a consumer that only cares about one service
// kind (e.g. just VPN) subscribe to services/vpn/ and skip the rest.
//
// Event-driven: register/update calls Put on success; explicit
// deregister calls Delete (and Put on the tombstone leaf); expiry
// sweep emits tombstones for entries the redis cleanup found stale.
// CXO content-addresses, so re-publishing an unchanged entry is a
// no-op at the wire layer — heartbeats from a still-alive service
// don't burn bandwidth.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// ServicesCXOPublisher mirrors SD's services state into a CXO
// TreeStore feed. Started automatically at SD startup whenever
// DMSG is enabled; the API calls into it via
// SetServicesCXOPublisher.
type ServicesCXOPublisher struct {
	pub *treestore.Publisher
	log *logging.Logger

	mu        sync.Mutex
	lastError error
}

// StartServicesCXOPublisher constructs a publisher backed by the
// given DMSG client and SD secret key. The publisher's allowlist is
// open — the underlying SD HTTP routes are public reads, so the CXO
// mirror inherits that. Returns nil + error if the publisher can't be
// created (no dmsg client, listener bind failure, etc.); callers log
// and continue without it (the HTTP path remains the source of truth).
func StartServicesCXOPublisher(_ context.Context, dmsgC *dmsg.Client, sk cipher.SecKey, logger logrus.FieldLogger) (*ServicesCXOPublisher, error) {
	log := logging.MustGetLogger("sd-cxo-services-pub")

	pub, err := treestore.NewWithDMSG(dmsgC, sk, treestore.PubConfig{
		Logger:     log,
		InMemoryDB: true, // services are always recomputed from redis on next mutation/sweep
		DmsgPort:   skyenv.DmsgSDServicesCXOPort,
	})
	if err != nil {
		return nil, err
	}
	pub.SetAllowlist(nil)

	p := &ServicesCXOPublisher{pub: pub, log: log}
	if logger != nil {
		logger.WithField("feed_pk", pub.Feed()).WithField("dmsg_port", skyenv.DmsgSDServicesCXOPort).
			Info("CXO services publisher running")
	}
	return p, nil
}

// FeedPK returns the publisher's feed PK (SD's own PK, since the
// publisher was constructed with SD's secret key). Subscribers
// connect to this PK on skyenv.DmsgSDServicesCXOPort.
func (p *ServicesCXOPublisher) FeedPK() cipher.PubKey { return p.pub.Feed() }

// Close stops the publisher. Safe to call multiple times.
func (p *ServicesCXOPublisher) Close() error {
	if p == nil || p.pub == nil {
		return nil
	}
	return p.pub.Close()
}

// PutEntry mirrors a service register/update. Idempotent: identical
// bytes are a no-op at the wire layer. Best-effort; logs at debug
// on failure (HTTP path is authoritative).
func (p *ServicesCXOPublisher) PutEntry(svc *servicedisc.Service) {
	if p == nil || p.pub == nil || svc == nil {
		return
	}
	if svc.Type == "" {
		return
	}
	body, err := json.Marshal(svc)
	if err != nil {
		p.log.WithError(err).Debug("Failed to marshal service entry leaf")
		p.recordError(err)
		return
	}
	path := entryPath(svc.Type, svc.Addr.PubKey())
	if err := p.pub.Put(path, body); err != nil {
		p.log.WithError(err).WithField("path", path).Debug("Failed to publish service entry leaf")
		p.recordError(err)
		// Also clear any stale tombstone so subscribers don't see
		// a dead-then-alive flap. Best-effort.
		_ = p.pub.Delete(tombstonePath(svc.Type, svc.Addr.PubKey())) //nolint:errcheck
		return
	}
	// Drop any existing tombstone for the same key — re-registration
	// after expiry should leave only the live entry leaf.
	if err := p.pub.Delete(tombstonePath(svc.Type, svc.Addr.PubKey())); err != nil {
		p.log.WithError(err).Debug("Failed to clear stale services tombstone")
	}
}

// DelEntry mirrors a service deregister or expiry. Removes the entry
// leaf and writes a tombstone leaf so subscribers see the deletion.
// Best-effort.
func (p *ServicesCXOPublisher) DelEntry(svcType string, pk cipher.PubKey) {
	if p == nil || p.pub == nil || svcType == "" {
		return
	}
	if err := p.pub.Delete(entryPath(svcType, pk)); err != nil {
		p.log.WithError(err).WithField("type", svcType).Debug("Failed to delete service entry leaf")
		p.recordError(err)
	}
	body, err := json.Marshal(struct {
		DeletedAt time.Time `json:"deleted_at"`
	}{DeletedAt: time.Now().UTC()})
	if err != nil {
		p.log.WithError(err).Debug("Failed to marshal services tombstone")
		return
	}
	if err := p.pub.Put(tombstonePath(svcType, pk), body); err != nil {
		p.log.WithError(err).WithField("type", svcType).Debug("Failed to publish services tombstone")
		p.recordError(err)
	}
}

func (p *ServicesCXOPublisher) recordError(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastError = err
}

// LastError returns the most recent publish error, or nil. Exposed
// for /health-style introspection.
func (p *ServicesCXOPublisher) LastError() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastError
}

// EntryPath / TombstonePath are exported so subscribers can construct
// the same leaf paths without duplicating the format string.
func EntryPath(svcType string, pk cipher.PubKey) string     { return entryPath(svcType, pk) }
func TombstonePath(svcType string, pk cipher.PubKey) string { return tombstonePath(svcType, pk) }

func entryPath(svcType string, pk cipher.PubKey) string {
	return fmt.Sprintf("services/%s/%s/entry", svcType, pk.Hex())
}
func tombstonePath(svcType string, pk cipher.PubKey) string {
	return fmt.Sprintf("services/%s/%s/tombstone", svcType, pk.Hex())
}
