// Package api pkg/deployment/tpd/api/cxo_all_transports_publisher.go c4-net-discovery
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
	"math"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// allTransportsWireEntry is the published shape of one transport in the
// all-transports snapshot. It mirrors transport.Entry MINUS the t_id: the ID
// is a deterministic hash of (edges, type), so it is fully derivable by the
// reader (transport.MakeTransportID) and shipping it wastes ~25% of the
// compressed body — a UUID is high-entropy and does not gzip away. The reader
// (pkg/visor's FetchAllTransportsCXO) reconstructs the full transport.Entry,
// recomputing t_id. Keep the JSON tags aligned with transport.Entry so an
// older subscriber that unmarshals straight into transport.Entry still reads
// every field it recognizes.
type allTransportsWireEntry struct {
	Edges         [2]cipher.PubKey `json:"edges"`
	Type          types.Type       `json:"type"`
	Label         transport.Label  `json:"label"`
	Latency       float64          `json:"latency_ms,omitempty"`
	ThroughputBps float64          `json:"throughput_bps,omitempty"`
}

// toWireEntries drops the derivable t_id from each entry for publication.
func toWireEntries(entries []*transport.Entry) []allTransportsWireEntry {
	out := make([]allTransportsWireEntry, 0, len(entries))
	for _, e := range entries {
		if e == nil {
			continue
		}
		out = append(out, allTransportsWireEntry{
			Edges:         e.Edges,
			Type:          e.Type,
			Label:         e.Label,
			Latency:       math.Round(e.Latency*10) / 10,
			ThroughputBps: roundSig3(e.ThroughputBps),
		})
	}
	return out
}

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
	entries, err := a.routingSnapshot(ctx)
	if err != nil {
		a.log.WithError(err).Debug("all-transports fetch failed; will retry next tick")
		a.recordError(err)
		return
	}
	body, err := json.Marshal(toWireEntries(entries))
	if err != nil {
		a.log.WithError(err).Warn("all-transports marshal failed")
		a.recordError(err)
		return
	}
	// gzip the snapshot before publishing: CXO stores + propagates object
	// bytes verbatim, so a raw JSON body travels uncompressed. Subscribers
	// auto-detect + gunzip (cxoutils.Gunzip).
	//
	// The same bytes go to both paths. with-self differed only by self-loops,
	// which are not routes; identical bytes are one CXO object, so a
	// subscriber fetches the snapshot once instead of twice.
	gz := cxoutils.Gzip(body)
	for _, path := range []string{AllTransportsPathWithoutSelf, AllTransportsPathWithSelf} {
		if err := a.pub.Put(path, gz); err != nil {
			a.log.WithError(err).WithField("path", path).Warn("publisher Put failed")
			a.recordError(err)
		}
	}
}

// routingSnapshot is what routers need: the transports that exist now —
// registrations refreshed within the entry TTL, withdrawn at once when a
// visor's published transport list drops one — with the latency and
// throughput routes are weighed by. It used to be the metric-free read, so
// every visor synced ~80k transports and not one latency figure.
func (a *AllTransportsCXOPublisher) routingSnapshot(ctx context.Context) ([]*transport.Entry, error) {
	if qs, ok := a.api.store.(interface {
		GetAllTransportsWithLatency(context.Context, bool) ([]*transport.Entry, error)
	}); ok {
		return qs.GetAllTransportsWithLatency(ctx, false)
	}
	return a.api.store.GetAllTransports(ctx, false)
}

// roundSig3 rounds v to three significant digits, so a figure that only jitters
// does not change the published bytes.
func roundSig3(v float64) float64 {
	if v == 0 {
		return 0
	}
	p := math.Pow(10, 3-math.Ceil(math.Log10(math.Abs(v))))
	return math.Round(v*p) / p
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

// Publisher returns the underlying feed publisher, for introspection.
func (x *AllTransportsCXOPublisher) Publisher() *treestore.Publisher { return x.pub }
