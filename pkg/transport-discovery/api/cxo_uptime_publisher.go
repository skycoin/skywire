// Package api pkg/transport-discovery/api/cxo_uptime_publisher.go
//
// CXO publisher for the network-wide visor-uptime aggregate. Mirrors
// MetricsCXOPublisher (cxo_metrics_publisher.go) but produces the
// `GET /uptimes?v=v3` shape — `[]VisorSummary` with the per-day
// timeline strings — so subscribers (the hvui Network Uptime tab,
// reached through a visor's on-demand subscriber) get the exact
// online/offline intervals the integrated tracker recorded.
//
// Same content-addressing benefit as the metrics publisher: only
// the visors whose Daily/Timeline buckets actually changed since
// the last tick produce a Root delta, so the wire footprint scales
// with churn rather than fleet size.
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
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport-discovery/store"
)

// uptimePublishDays mirrors metricsPublishDays — the windows the
// hvui's Network Uptime tab can pick from. Anything outside this set
// falls through to the existing HTTP/DMSG-HTTP path.
var uptimePublishDays = []int{1, 7, 30}

// uptimePublishInterval is the recompute cadence. Same value as the
// metrics publisher (60s) — the tracker's underlying redis state
// refreshes on the visor heartbeat cycle (~90s) so faster ticks
// would just republish identical Roots.
const uptimePublishInterval = 60 * time.Second

// UptimeCXOPublisher publishes `[]VisorSummary` (v3 shape) for each
// of uptimePublishDays at "uptimes/days/<n>". Closed by Close.
type UptimeCXOPublisher struct {
	api *API
	pub *treestore.Publisher
	log *logging.Logger

	cancel context.CancelFunc
	done   chan struct{}

	mu        sync.Mutex
	lastError error
}

// StartUptimeCXOPublisher constructs a publisher backed by the given
// DMSG client and TPD secret key, then kicks off the recompute
// ticker. The publisher's allowlist is left open (any subscriber may
// read) — same access policy as `GET /uptimes`.
//
// Returns nil + error when the publisher can't be created (no DMSG
// client, listener bind failure, etc.); the caller should log and
// continue without it. Best-effort: the existing HTTP /uptimes
// route remains the source of truth.
func StartUptimeCXOPublisher(ctx context.Context, api *API, dmsgC *dmsg.Client, sk cipher.SecKey, logger logrus.FieldLogger) (*UptimeCXOPublisher, error) {
	log := logging.MustGetLogger("tpd-cxo-uptime-pub")

	pub, err := treestore.NewWithDMSG(dmsgC, sk, treestore.PubConfig{
		Logger:     log,
		InMemoryDB: true, // recomputed from redis on each tick
		DmsgPort:   skyenv.DmsgTPDUptimeCXOPort,
	})
	if err != nil {
		return nil, err
	}
	pub.SetAllowlist(nil) // open feed

	pubCtx, cancel := context.WithCancel(ctx)
	up := &UptimeCXOPublisher{
		api:    api,
		pub:    pub,
		log:    log,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	if logger != nil {
		logger.WithField("feed_pk", pub.Feed()).WithField("dmsg_port", skyenv.DmsgTPDUptimeCXOPort).
			Info("CXO uptime publisher running")
	}
	go up.loop(pubCtx)
	return up, nil
}

// FeedPK returns the publisher's feed PK (TPD's own PK). Subscribers
// connect to this PK at port skyenv.DmsgTPDUptimeCXOPort.
func (u *UptimeCXOPublisher) FeedPK() cipher.PubKey { return u.pub.Feed() }

// Close stops the ticker and tears down the publisher.
func (u *UptimeCXOPublisher) Close() error {
	if u.cancel != nil {
		u.cancel()
	}
	<-u.done
	return u.pub.Close()
}

func (u *UptimeCXOPublisher) loop(ctx context.Context) {
	defer close(u.done)

	// Publish once immediately so a freshly-connected subscriber
	// gets a snapshot without waiting a full tick.
	u.publishOnce(ctx)

	t := time.NewTicker(uptimePublishInterval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			u.publishOnce(ctx)
		}
	}
}

func (u *UptimeCXOPublisher) publishOnce(ctx context.Context) {
	// The visor-uptime cache is independent of the day window — the
	// "days" parameter is purely a client-side trim hint. We compute
	// once and republish per-window so subscribers can request the
	// shape they need without redoing the timeline work themselves.
	full := u.api.getUptimesV2FromCache()
	if full == nil {
		full = []store.VisorSummary{}
	}
	now := time.Now().UTC()

	// Enrich every entry with timeline data once. Avoids redoing the
	// Redis-backed daily-timeline lookups per window.
	enriched := make([]store.VisorSummary, len(full))
	copy(enriched, full)
	for i := range enriched {
		enriched[i].Timeline = u.api.store.GetDailyTimeline(ctx, enriched[i].PK.Hex(), now)
	}

	for _, days := range uptimePublishDays {
		// Trim Daily and Timeline to the requested day window so the
		// payload doesn't carry more than the subscriber asked for.
		trimmed := trimSummariesToDays(enriched, days, now)
		body, err := json.Marshal(trimmed)
		if err != nil {
			u.log.WithError(err).WithField("days", days).Warn("uptime marshal failed")
			u.recordError(err)
			continue
		}
		path := uptimePath(days)
		if err := u.pub.Put(path, body); err != nil {
			u.log.WithError(err).WithField("path", path).Warn("uptime publisher Put failed")
			u.recordError(err)
			continue
		}
	}
}

func (u *UptimeCXOPublisher) recordError(err error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.lastError = err
}

// LastError returns the most recent error encountered by the publish
// loop, or nil if the last tick succeeded for every window.
func (u *UptimeCXOPublisher) LastError() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.lastError
}

// UptimePath returns the TreeStore path for a given day window.
// Exported so visor-side subscribers don't have to duplicate the
// format string.
func UptimePath(days int) string { return uptimePath(days) }

func uptimePath(days int) string {
	return fmt.Sprintf("uptimes/days/%d", days)
}

// trimSummariesToDays drops Daily and Timeline entries whose date
// is older than `days` UTC days before `now`. days <= 0 is treated
// as "no trim" — the full retention window leaks through. The cap
// stays inside the function so callers don't have to remember the
// uptimePublishDays max.
func trimSummariesToDays(in []store.VisorSummary, days int, now time.Time) []store.VisorSummary {
	if days <= 0 {
		return in
	}
	cutoff := now.AddDate(0, 0, -days).Format("2006-01-02")
	out := make([]store.VisorSummary, len(in))
	for i, s := range in {
		out[i] = s
		if len(s.Daily) > 0 {
			d := make(map[string]string, len(s.Daily))
			for k, v := range s.Daily {
				if k >= cutoff {
					d[k] = v
				}
			}
			out[i].Daily = d
		}
		if len(s.Timeline) > 0 {
			t := make(map[string]string, len(s.Timeline))
			for k, v := range s.Timeline {
				if k >= cutoff {
					t[k] = v
				}
			}
			out[i].Timeline = t
		}
	}
	return out
}
