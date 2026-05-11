// Package api pkg/transport-discovery/api/cxo_all_transports_publisher.go
//
// CXO publisher for the network-wide all-transports snapshot. On a
// fixed cadence (default 60s) the publisher reads the store's
// memoized GetAllTransports(...) result for both the with-self and
// without-self variants and writes JSON-encoded []*transport.Entry
// to TreeStore paths
//
//	transports/all/with-self
//	transports/all/without-self
//
// Subscribers (the CLI's `pv -t`, `tp tree`, `tp viz`) read the
// snapshot instead of HTTP-polling /all-transports. CXO's content
// addressing means a stable transport set produces a stable Root,
// so reading the same snapshot twice in a row is effectively free
// after the first sync.
package api

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// allTransportsPublishInterval is the recompute cadence. 60s matches
// the metrics/uptime publishers; the store's allTransportsCache
// memoizes between ticks so the actual cost is bounded.
const allTransportsPublishInterval = 60 * time.Second

// Published paths. Exported so visor-side subscribers don't have to
// duplicate the format strings.
const (
	AllTransportsPathWithSelf    = "transports/all/with-self"
	AllTransportsPathWithoutSelf = "transports/all/without-self"
)

// AllTransportsCXOPublisher periodically reads the store's
// GetAllTransports snapshot and publishes the with-self and
// without-self variants. Close shuts the publisher and stops the
// ticker.
type AllTransportsCXOPublisher struct {
	api *API
	pub *treestore.Publisher
	log *logging.Logger

	cancel context.CancelFunc
	done   chan struct{}

	mu        sync.Mutex
	lastError error
}

// StartAllTransportsCXOPublisher constructs a publisher backed by
// the given DMSG client and TPD secret key, then kicks off the
// recompute ticker. Best-effort: HTTP /all-transports stays the
// source of truth; callers should log and continue if this fails.
func StartAllTransportsCXOPublisher(ctx context.Context, api *API, dmsgC *dmsg.Client, sk cipher.SecKey, logger logrus.FieldLogger) (*AllTransportsCXOPublisher, error) {
	log := logging.MustGetLogger("tpd-cxo-all-transports-pub")

	pub, err := treestore.NewWithDMSG(dmsgC, sk, treestore.PubConfig{
		Logger:     log,
		InMemoryDB: true,
		DmsgPort:   skyenv.DmsgTPDAllTransportsCXOPort,
	})
	if err != nil {
		return nil, err
	}
	// nil allowlist = open feed (any subscriber accepted).
	pub.SetAllowlist(nil)

	pubCtx, cancel := context.WithCancel(ctx)
	ap := &AllTransportsCXOPublisher{
		api:    api,
		pub:    pub,
		log:    log,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	if logger != nil {
		logger.WithField("feed_pk", pub.Feed()).WithField("dmsg_port", skyenv.DmsgTPDAllTransportsCXOPort).
			Info("CXO all-transports publisher running")
	}
	go ap.loop(pubCtx)
	return ap, nil
}

// FeedPK returns the publisher's feed PK (TPD's own PK).
func (a *AllTransportsCXOPublisher) FeedPK() cipher.PubKey { return a.pub.Feed() }

// Close stops the ticker and tears down the publisher.
func (a *AllTransportsCXOPublisher) Close() error {
	if a.cancel != nil {
		a.cancel()
	}
	<-a.done
	return a.pub.Close()
}

func (a *AllTransportsCXOPublisher) loop(ctx context.Context) {
	defer close(a.done)

	// Publish once immediately so an early subscriber gets a snapshot.
	a.publishOnce(ctx)

	t := time.NewTicker(allTransportsPublishInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.publishOnce(ctx)
		}
	}
}

func (a *AllTransportsCXOPublisher) publishOnce(ctx context.Context) {
	for _, v := range []struct {
		path     string
		withSelf bool
	}{
		{AllTransportsPathWithoutSelf, false},
		{AllTransportsPathWithSelf, true},
	} {
		entries, err := a.api.store.GetAllTransports(ctx, v.withSelf)
		if err != nil {
			a.log.WithError(err).WithField("path", v.path).Debug("all-transports fetch failed; will retry next tick")
			a.recordError(err)
			continue
		}
		body, err := json.Marshal(entries)
		if err != nil {
			a.log.WithError(err).WithField("path", v.path).Warn("all-transports marshal failed")
			a.recordError(err)
			continue
		}
		if err := a.pub.Put(v.path, body); err != nil {
			a.log.WithError(err).WithField("path", v.path).Warn("publisher Put failed")
			a.recordError(err)
			continue
		}
	}
}

func (a *AllTransportsCXOPublisher) recordError(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lastError = err
}

// LastError returns the most recent error from the publish loop, or
// nil if the last tick succeeded for both variants.
func (a *AllTransportsCXOPublisher) LastError() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastError
}
