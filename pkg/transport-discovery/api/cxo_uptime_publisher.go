// Package api pkg/transport-discovery/api/cxo_uptime_publisher.go c4-net-discovery
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

// UptimeCXOPublisher publishes the network-wide visor-uptime aggregate for
// each of uptimePublishDays. Rather than one monolithic `[]VisorSummary` leaf
// per window at "uptimes/days/<n>" (which, at fleet scale with the v3 288-char
// timeline strings, exceeded CXO's 16 MB MaxObjectSize and failed the Put with
// "object is too large" — the serve-side feed the subscriber could never
// fill), it writes one small per-visor leaf at "uptimes/days/<n>/<pkHex>". No
// single object is fleet-sized, so the Put always succeeds; content-addressing
// re-broadcasts only the visors whose bucket changed; and a subscriber whose
// fill truncates still salvages every per-visor leaf that landed (see the
// treestore subscriber's partial-fill recovery). Closed by Close.
type UptimeCXOPublisher struct {
	api *API
	pub *treestore.Publisher
	log *logging.Logger

	cancel context.CancelFunc
	done   chan struct{}

	// prevPKs tracks, per day-window, the set of per-visor leaf hex-PK
	// segments published on the previous tick, so this tick can emit delete
	// ops for visors that dropped out of the aggregate (a naive Put-only
	// refresh would leave their stale leaves lingering forever). Touched only
	// from the single publish loop goroutine, so no lock.
	prevPKs map[int]map[string]struct{}

	mu        sync.Mutex
	lastError error
}

// compactVisorSummary is the space-optimized per-visor uptime leaf written at
// "uptimes/days/<n>/<pkHex>". It carries the same data as store.VisorSummary
// but encodes each day's Timeline as the raw 36-byte MSB-first bitmap (base64
// in JSON) instead of the 288-char '.'/' ' string — 8x smaller for the field
// that dominates the payload and the reason the monolithic leaf blew past
// MaxObjectSize. Re-declared verbatim on the reader side (pkg/visor's
// FetchVisorUptimeCXO), which reconstructs the full store.VisorSummary v3
// shape; keep the JSON tags in sync across both.
type compactVisorSummary struct {
	PK       cipher.PubKey     `json:"pk"`
	Online   bool              `json:"on"`
	Version  string            `json:"v,omitempty"`
	Daily    map[string]string `json:"d,omitempty"`
	Timeline map[string][]byte `json:"tb,omitempty"` // date -> 36-byte bitmap
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
		api:     api,
		pub:     pub,
		log:     log,
		cancel:  cancel,
		done:    make(chan struct{}),
		prevPKs: make(map[int]map[string]struct{}),
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
		u.publishWindow(days, trimmed)
	}
}

// publishWindow writes one small per-visor leaf at "uptimes/days/<n>/<pkHex>"
// for every summary, plus delete ops for any visor published last tick that
// is gone this tick, in a single PutBatch. Splitting the window into per-visor
// leaves (each a compactVisorSummary with bitmap timelines) keeps every CXO
// object well under MaxObjectSize — the whole point of the change: the
// monolithic []VisorSummary leaf failed the Put once the fleet's v3 timelines
// pushed it past 16 MB.
func (u *UptimeCXOPublisher) publishWindow(days int, summaries []store.VisorSummary) {
	base := uptimePath(days)
	live := make(map[string]struct{}, len(summaries))
	ops := make([]treestore.PutOp, 0, len(summaries))
	for i := range summaries {
		hexPK := summaries[i].PK.Hex()
		body, err := json.Marshal(toCompactVisorSummary(summaries[i]))
		if err != nil {
			u.log.WithError(err).WithField("days", days).Warn("uptime per-visor marshal failed")
			u.recordError(err)
			continue
		}
		live[hexPK] = struct{}{}
		ops = append(ops, treestore.PutOp{Path: base + "/" + hexPK, Value: body})
	}
	// Delete leaves for visors that dropped out of the aggregate since the
	// previous tick (nil Value = delete). Without this, a visor that leaves
	// the cache keeps a stale leaf forever.
	for hexPK := range u.prevPKs[days] {
		if _, still := live[hexPK]; !still {
			ops = append(ops, treestore.PutOp{Path: base + "/" + hexPK, Value: nil})
		}
	}
	if err := u.pub.PutBatch(ops); err != nil {
		u.log.WithError(err).WithField("days", days).Warn("uptime publisher PutBatch failed")
		u.recordError(err)
		return
	}
	u.prevPKs[days] = live
}

// toCompactVisorSummary converts a store.VisorSummary (v3 shape, 288-char
// timeline strings) to the compact per-visor leaf shape, packing each day's
// timeline into its 36-byte bitmap.
func toCompactVisorSummary(s store.VisorSummary) compactVisorSummary {
	c := compactVisorSummary{
		PK:      s.PK,
		Online:  s.Online,
		Version: s.Version,
		Daily:   s.Daily,
	}
	if len(s.Timeline) > 0 {
		c.Timeline = make(map[string][]byte, len(s.Timeline))
		for date, line := range s.Timeline {
			c.Timeline[date] = timelineStringToBitmap(line)
		}
	}
	return c
}

// timelineStringToBitmap packs a v3 288-char '.'/' ' timeline string into the
// 36-byte MSB-first bitmap redis/pkg/visor-stats already use ('.' = bit set).
func timelineStringToBitmap(line string) []byte {
	bm := make([]byte, 36)
	for i := 0; i < len(line) && i < 288; i++ {
		if line[i] == '.' {
			bm[i/8] |= 1 << uint(7-i%8)
		}
	}
	return bm
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
