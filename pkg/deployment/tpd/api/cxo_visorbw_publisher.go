// Package api pkg/deployment/tpd/api/cxo_visorbw_publisher.go c4-net-discovery
//
// CXO per-visor bandwidth feed: what the reward system pays pool 2 from.
//
//	visorbw/day/<YYYY-MM-DD>   gzipped JSON store.VisorBWDay
//
// One leaf per SETTLED day, for the last visorBWWindowDays days. Each is
// reduced once from the per-transport records the metrics publisher saved
// when the day settled, so it is final: the bytes of a past day do not change.
// Transports between visors on one IP are left out, by the IP classes the
// reward system publishes (store.IPClasses); until those have arrived no day
// is published, because a day without the exclusion would pay for exactly the
// traffic it exists to stop.
//
// See pkg/deployment/tpd/store/visorbw.go for why the exclusion is done here.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
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

// classSource is where the reward system's IP classes are read: its CXO feed,
// or a fixed value in tests.
type classSource interface {
	Get(path string) ([]byte, bool)
	Close() error
}

// VisorBWCXOPublisher publishes the per-visor bandwidth feed.
type VisorBWCXOPublisher struct {
	api     *API
	pub     *treestore.Publisher
	classes classSource
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
// DmsgTPDVisorBWCXOPort, and its subscription to the reward system's IP
// classes on DmsgRewardIPClassCXOPort.
func StartVisorBWCXOPublisher(ctx context.Context, api *API, dmsgC *dmsg.Client, sk cipher.SecKey, logger logrus.FieldLogger) (*VisorBWCXOPublisher, error) {
	rewardPK := cmdutil.PKFromDmsgURL(deployment.Prod.RewardSystem)
	if rewardPK == (cipher.PubKey{}) {
		return nil, fmt.Errorf("no reward system public key in the deployment config")
	}
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
	sub, err := treestore.NewSubscriber(dmsgC, rewardPK, treestore.SubConfig{
		Logger:     logging.MustGetLogger("tpd-cxo-ipclass-sub"),
		InMemoryDB: true,
		DmsgPort:   skyenv.DmsgRewardIPClassCXOPort,
	})
	if err != nil {
		_ = pub.Close() //nolint:errcheck
		return nil, err
	}
	pubCtx, cancel := context.WithCancel(ctx)
	p := &VisorBWCXOPublisher{
		api: api, pub: pub, classes: sub, log: log,
		putBatch: pub.PutBatch,
		cancel:   cancel, done: make(chan struct{}), published: map[string]bool{},
	}
	if logger != nil {
		logger.WithField("feed_pk", pub.Feed()).WithField("dmsg_port", skyenv.DmsgTPDVisorBWCXOPort).
			WithField("reward_pk", rewardPK).Info("CXO per-visor bandwidth publisher running")
	}
	go p.connectClasses(pubCtx, sub, rewardPK)
	go p.loop(pubCtx)
	return p, nil
}

// connectClasses subscribes to the reward system's IP classes, retrying until
// the first connection holds; after that the subscriber's watchdog reconnects.
func (p *VisorBWCXOPublisher) connectClasses(ctx context.Context, sub *treestore.Subscriber, rewardPK cipher.PubKey) {
	delay := 10 * time.Second
	for {
		dctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := sub.Connect(dctx, rewardPK)
		cancel()
		if err == nil {
			return
		}
		p.log.WithError(err).Debug("could not reach the reward system's IP classes; retrying")
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		if delay < 5*time.Minute {
			delay *= 2
		}
	}
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
	classes, ok := p.readClasses()
	if !ok {
		p.log.Debug("no IP classes from the reward system yet; not publishing")
		return
	}
	dates := settledDates(now)
	var missing []string
	for _, d := range dates {
		if !p.published[d] {
			missing = append(missing, d)
		}
	}
	ls, isLeafStore := p.api.store.(leafStore)
	if !isLeafStore {
		p.recordError(fmt.Errorf("store keeps no settled metrics days"))
		return
	}
	var ops []treestore.PutOp
	inWindow := make(map[string]bool, len(dates))
	for _, d := range dates {
		inWindow[d] = true
	}
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
			day := store.ComputeVisorBW(records, d, classes)
			body, err := json.Marshal(day)
			if err != nil {
				continue
			}
			ops = append(ops, treestore.PutOp{Path: store.VisorBWDayPath(d), Value: cxoutils.Gzip(body)})
			p.log.WithField("date", d).WithField("visors", len(day.Visors)).
				WithField("transports", day.Transports).WithField("same_ip_excluded", day.SameIPExcluded).
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

// readClasses returns the reward system's current IP classes, if any.
func (p *VisorBWCXOPublisher) readClasses() (*store.IPClasses, bool) {
	body, ok := p.classes.Get(store.IPClassPath)
	if !ok || len(body) == 0 {
		return nil, false
	}
	var c store.IPClasses
	if err := json.Unmarshal(cxoutils.Gunzip(body), &c); err != nil || len(c.Classes) == 0 {
		return nil, false
	}
	return &c, true
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

// Close stops the publisher and the IP-class subscription.
func (p *VisorBWCXOPublisher) Close() error {
	p.cancel()
	<-p.done
	_ = p.classes.Close() //nolint:errcheck
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
