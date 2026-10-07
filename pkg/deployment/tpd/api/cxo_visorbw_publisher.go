// Package api pkg/deployment/tpd/api/cxo_visorbw_publisher.go c4-net-discovery
//
// CXO per-visor bandwidth feed: what the reward system pays pool 2 from.
//
//	visorbw/day/<YYYY-MM-DD>   gzipped JSON store.VisorBWDay
//
// One leaf per SETTLED day, for the last visorBWWindowDays days. Each is
// reduced once from the per-transport records the metrics publisher saved
// when the day settled, so it is final: the bytes of a past day do not change.
// Transports a visor marked as reaching a peer on its own network are left
// out (see pkg/deployment/tpd/store/visorbw.go).
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/deployment/tpd/store"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
)

const (
	// visorBWWindowDays is how many settled days the feed carries: enough
	// for the reward system to catch up after a few days down.
	visorBWWindowDays = 7

	// visorBWTick is how often the publisher looks for a newly settled day.
	// The metrics publisher saves a day within metricsTick of midnight UTC.
	visorBWTick = 10 * time.Minute
)

// VisorBWCXOPublisher publishes the per-visor bandwidth feed.
type VisorBWCXOPublisher struct {
	api *API
	pub *treestore.Publisher
	// putBatch writes to the feed: pub.PutBatch, or a capture in tests.
	putBatch func(ops []treestore.PutOp) error
	log      *logging.Logger

	cancel context.CancelFunc
	done   chan struct{}

	// published is the set of dates whose leaf is on the feed. Only the
	// publish loop touches it.
	published map[string]bool

	mu        sync.Mutex
	lastError error
}

// StartVisorBWCXOPublisher starts the per-visor bandwidth feed on
// DmsgTPDVisorBWCXOPort.
func StartVisorBWCXOPublisher(ctx context.Context, api *API, dmsgC *dmsg.Client, sk cipher.SecKey, logger logrus.FieldLogger) (*VisorBWCXOPublisher, error) {
	log := logging.MustGetLogger("tpd-cxo-visorbw-pub")
	pub, err := treestore.NewWithDMSG(dmsgC, sk, treestore.PubConfig{
		Logger:     log,
		InMemoryDB: true,
		DmsgPort:   skyenv.DmsgTPDVisorBWCXOPort,
	})
	if err != nil {
		return nil, err
	}
	pub.SetAllowlist(nil) // per-visor byte counts; the per-transport metrics they come from are public too
	pubCtx, cancel := context.WithCancel(ctx)
	p := &VisorBWCXOPublisher{
		api: api, pub: pub, log: log, putBatch: pub.PutBatch,
		cancel: cancel, done: make(chan struct{}), published: map[string]bool{},
	}
	if logger != nil {
		logger.WithField("feed_pk", pub.Feed()).WithField("dmsg_port", skyenv.DmsgTPDVisorBWCXOPort).
			Info("CXO per-visor bandwidth publisher running")
	}
	go p.loop(pubCtx)
	return p, nil
}

func (p *VisorBWCXOPublisher) loop(ctx context.Context) {
	defer close(p.done)
	t := time.NewTicker(visorBWTick)
	defer t.Stop()
	for {
		p.publishOnce(ctx, time.Now().UTC())
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// settledDates are the last visorBWWindowDays days before now's, newest first.
func settledDates(now time.Time) []string {
	out := make([]string, 0, visorBWWindowDays)
	for i := 1; i <= visorBWWindowDays; i++ {
		out = append(out, now.AddDate(0, 0, -i).Format("2006-01-02"))
	}
	return out
}

func (p *VisorBWCXOPublisher) publishOnce(ctx context.Context, now time.Time) {
	ls, isLeafStore := p.api.store.(leafStore)
	if !isLeafStore {
		p.recordError(fmt.Errorf("store keeps no settled metrics days"))
		return
	}
	dates := settledDates(now)
	inWindow := make(map[string]bool, len(dates))
	var missing []string
	for _, d := range dates {
		inWindow[d] = true
		if !p.published[d] {
			missing = append(missing, d)
		}
	}
	var ops []treestore.PutOp
	for d := range p.published {
		if !inWindow[d] {
			ops = append(ops, treestore.PutOp{Path: store.VisorBWDayPath(d)})
		}
	}
	if len(missing) > 0 {
		leaves, err := ls.LoadMetricsLeaves(ctx, missing)
		if err != nil {
			p.recordError(err)
			p.log.WithError(err).Debug("could not load settled metrics days")
			return
		}
		for _, d := range missing {
			parts, ok := leaves[d]
			if !ok {
				continue // not settled yet, or before the store kept days
			}
			records, err := decodeMetricsParts(parts)
			if err != nil {
				p.log.WithError(err).WithField("date", d).Warn("settled metrics day does not decode")
				continue
			}
			day := store.ComputeVisorBW(records, d)
			if len(day.Visors) == 0 {
				// The day's leaf missed its rows. Publishing it would hand the
				// reward system an empty day, so wait for the leaf to be rebuilt.
				p.log.WithField("date", d).Debug("settled day has no per-visor bandwidth yet")
				continue
			}
			body, err := json.Marshal(day)
			if err != nil {
				continue
			}
			ops = append(ops, treestore.PutOp{Path: store.VisorBWDayPath(d), Value: cxoutils.Gzip(body)})
			p.log.WithField("date", d).WithField("visors", len(day.Visors)).
				WithField("transports", day.Transports).WithField("same_network_excluded", day.SameNetworkExcluded).
				Info("Published a settled day of per-visor bandwidth")
		}
	}
	if len(ops) == 0 {
		return
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].Path < ops[j].Path })
	if err := p.putBatch(ops); err != nil {
		p.recordError(err)
		p.log.WithError(err).Warn("per-visor bandwidth PutBatch failed")
		return
	}
	for _, op := range ops {
		d := op.Path[len(store.VisorBWDayPrefix):]
		if op.Value == nil {
			delete(p.published, d)
		} else {
			p.published[d] = true
		}
	}
}

// decodeMetricsParts decodes a settled day's saved leaf, gzipped JSON
// []store.TransportMetric in one or more parts.
func decodeMetricsParts(parts [][]byte) ([]store.TransportMetric, error) {
	var out []store.TransportMetric
	for _, part := range parts {
		var recs []store.TransportMetric
		if err := json.Unmarshal(cxoutils.Gunzip(part), &recs); err != nil {
			return nil, err
		}
		out = append(out, recs...)
	}
	return out, nil
}

// FeedPK is the feed's public key.
func (p *VisorBWCXOPublisher) FeedPK() cipher.PubKey { return p.pub.Feed() }

// Publisher returns the underlying treestore publisher.
func (p *VisorBWCXOPublisher) Publisher() *treestore.Publisher { return p.pub }

// Close stops the publisher.
func (p *VisorBWCXOPublisher) Close() error {
	p.cancel()
	<-p.done
	return p.pub.Close()
}

func (p *VisorBWCXOPublisher) recordError(err error) {
	p.mu.Lock()
	p.lastError = err
	p.mu.Unlock()
}

// LastError is the most recent publish failure, if any.
func (p *VisorBWCXOPublisher) LastError() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastError
}
