// Package visor — pkg/visor/init_stats.go: wires the local telemetry
// store into the visor lifecycle.
//
// initStats opens the bbolt store, builds the pull-style probes
// (transport snapshot, tier states, service states), constructs the
// Tracker, and registers the Close in the visor's close stack so the
// store and goroutine drain on shutdown.
package visor

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgcurl"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/visor/stats"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

const (
	defaultStatsSampleInterval = time.Minute
	defaultStatsRetentionDays  = 30
	defaultCXOPublishWindow    = 7
	skynetMinTransports        = 2 // matches "skynet online" semantics in §07
)

func initStats(_ context.Context, v *Visor, log *logging.Logger) error {
	conf := v.conf.Stats
	if conf != nil && conf.Disabled {
		log.Info("Stats: disabled by config; skipping bbolt store and CXO publisher")
		return nil
	}

	path := statsPath(v, conf)
	store, err := stats.OpenStore(path)
	if err != nil {
		return fmt.Errorf("stats: open store at %s: %w", path, err)
	}

	publishWindow := publishWindowDays(conf)
	tracker := stats.NewTracker(store, stats.Probes{
		Transports:    transportsProbe(v),
		TierStates:    tierStatesProbe(v),
		ServiceStates: serviceStatesProbe(v),
	}, stats.Config{
		SampleInterval:    sampleInterval(conf),
		RetentionDays:     retentionDays(conf),
		PublishWindowDays: publishWindow,
		Logger:            log,
	})

	// Try to bring up a CXO publisher. Failure is non-fatal — the
	// store and HTTP /stats/* endpoints work without it; subscribers
	// just don't get push updates. Publisher feed PK is the visor's
	// own PK (one identity per node).
	pub, sink := buildStatsPublisher(v, log)
	if pub != nil {
		if err := seedSinkFromStore(store, sink, publishWindow, log); err != nil {
			log.WithError(err).Warn("Stats: hydrating CXO publisher from bbolt failed")
		}
		tracker.SetSink(sink)
		// Wire the same publisher into the transport manager so its
		// register / deregister loops mirror the canonical entry +
		// tombstone leaves to TPD's CXO aggregator (dual-write
		// alongside the HTTP path during migration).
		if v.tpM != nil {
			v.tpM.SetTPDLeafPublisher(pub)
		}
	}

	tracker.Run(v.ctx)
	v.statsTracker = tracker
	v.pushCloseStack("stats", func() error {
		err := tracker.Close()
		if pub != nil {
			if perr := pub.Close(); perr != nil && err == nil {
				err = perr
			}
		}
		return err
	})

	// Wire the store into the log server so /stats/* handlers can
	// read it. The log server was constructed earlier in initDmsg
	// (well before this module runs), so the handle is guaranteed to
	// exist by now; nil-checking it keeps the code defensive against
	// future init-order changes.
	v.initLock.RLock()
	lsAPI := v.logServer.api
	v.initLock.RUnlock()
	if lsAPI != nil {
		lsAPI.SetStatsReader(tracker.Store())
	}

	// If we have a publisher and we know the TPD's PK, kick off a
	// goroutine that periodically dials it. ConnectPK is idempotent
	// — alive conn = no-op, dropped = redial — so this single
	// pattern handles both initial announce and reconnect-after-
	// visor-or-dmsg-restart. TPD's aggregator sees the inbound conn
	// and subscribes back to our feed (PK = our own PK).
	if pub != nil {
		if tpdPK, ok := tpdCXOPeer(v); ok {
			go runAnnounceLoop(v.ctx, pub, tpdPK, log)
		} else {
			log.Debug("Stats: no Transport.DiscoveryDmsg PK; skipping CXO announce loop")
		}
	}

	log.WithField("path", path).WithField("cxo", pub != nil).Info("Stats: telemetry store running")
	return nil
}

// announceInterval is how often the visor pokes its CXO conn to the
// TPD aggregator. Short enough that a recovered visor or recovered
// TPD start exchanging telemetry within a minute; long enough to be
// negligible overhead.
const announceInterval = 30 * time.Second

func runAnnounceLoop(ctx context.Context, pub *treestore.Publisher, tpdPK cipher.PubKey, log *logging.Logger) {
	t := time.NewTicker(announceInterval)
	defer t.Stop()
	announceOnce(pub, tpdPK, log)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			announceOnce(pub, tpdPK, log)
		}
	}
}

func announceOnce(pub *treestore.Publisher, tpdPK cipher.PubKey, log *logging.Logger) {
	if err := pub.AnnounceTo(tpdPK); err != nil {
		// Trace-level: this races with TPD start-up and DMSG
		// readiness; transient failures are normal.
		log.WithError(err).WithField("tpd_pk", tpdPK).Trace("Stats: CXO announce to TPD failed")
	}
}

// tpdCXOPeer extracts the TPD's PK from the visor's Transport.DiscoveryDmsg
// URL ("dmsg://<pk>:<port>"). Returns ok=false when the URL is empty
// or unparseable; the caller treats that as "no announce target."
func tpdCXOPeer(v *Visor) (cipher.PubKey, bool) {
	raw := v.conf.Transport.DiscoveryDmsg
	if raw == "" {
		return cipher.PubKey{}, false
	}
	var u dmsgcurl.URL
	if err := u.Fill(raw); err != nil {
		return cipher.PubKey{}, false
	}
	if u.Scheme != "dmsg" {
		return cipher.PubKey{}, false
	}
	return u.Addr.PK, true
}

// buildStatsPublisher constructs the visor's CXO publisher feed for
// telemetry. Returns (nil, nil) when DMSG isn't available — the rest
// of the stats subsystem still runs, just without push.
func buildStatsPublisher(v *Visor, log *logging.Logger) (*treestore.Publisher, stats.Sink) {
	if v.dmsgC == nil {
		log.Debug("Stats: dmsg client absent; CXO publisher not started")
		return nil, nil
	}
	dataDir := filepath.Join(v.conf.LocalPath, "cxo-stats")
	// BatchWindow of 10s coalesces stat mutations into ~6 bbolt
	// commits per minute on this publisher (one per window),
	// down from ~60 with the previous 1s setting. Stats are sampled
	// at 1-minute intervals (see stats.Tracker.SampleInterval), so a
	// 10s publish window introduces no observable freshness loss for
	// subscribers while cutting fsync amplification on the CXO
	// datastore by an order of magnitude — meaningful for visors
	// running on microSD or other write-sensitive media.
	pub, err := treestore.NewWithDMSG(v.dmsgC, v.conf.SK, treestore.PubConfig{
		BatchWindow: 10 * time.Second,
		Logger:      log,
		DataDir:     dataDir,
	})
	if err != nil {
		log.WithError(err).Warn("Stats: CXO publisher init failed; continuing without push")
		return nil, nil
	}
	log.WithField("feed_pk", pub.Feed()).WithField("data_dir", dataDir).
		Info("Stats: CXO publisher running")
	return pub, &cxoSink{pub: pub, log: log}
}

// seedSinkFromStore copies the in-window slice of bbolt state into
// the freshly-initialized publisher so cold subscribers see the full
// rolling window from the first connect, not just data sampled after
// the visor restarted.
func seedSinkFromStore(store *stats.Store, sink stats.Sink, publishWindowDays int, log *logging.Logger) error {
	pushed, err := stats.HydrateSink(store, sink, publishWindowDays, time.Now())
	if err != nil {
		return err
	}
	log.WithField("paths", pushed).Debug("Stats: hydrated CXO publisher from bbolt")
	return nil
}

// cxoSink adapts a treestore.Publisher to the stats.Sink contract.
// Errors are logged at debug level — sink mirroring is best-effort.
type cxoSink struct {
	pub *treestore.Publisher
	log *logging.Logger
}

func (s *cxoSink) Put(path string, value []byte) {
	if err := s.pub.Put(path, value); err != nil {
		s.log.WithError(err).WithField("path", path).Debug("Stats: CXO Put failed")
	}
}

func (s *cxoSink) Delete(path string) {
	if err := s.pub.Delete(path); err != nil {
		s.log.WithError(err).WithField("path", path).Debug("Stats: CXO Delete failed")
	}
}

func statsPath(v *Visor, conf *visorconfig.Stats) string {
	if conf != nil && conf.Path != "" {
		return conf.Path
	}
	return filepath.Join(v.conf.LocalPath, "stats.db")
}

func sampleInterval(conf *visorconfig.Stats) time.Duration {
	if conf == nil || conf.SampleInterval == 0 {
		return defaultStatsSampleInterval
	}
	return time.Duration(conf.SampleInterval)
}

func retentionDays(conf *visorconfig.Stats) int {
	if conf == nil || conf.RetentionDays == 0 {
		return defaultStatsRetentionDays
	}
	return conf.RetentionDays
}

func publishWindowDays(conf *visorconfig.Stats) int {
	if conf == nil || conf.CXOPublishWindow == 0 {
		return defaultCXOPublishWindow
	}
	return conf.CXOPublishWindow
}

// transportsProbe returns a closure that snapshots the visor's live
// transports each tick. Closed transports are filtered.
func transportsProbe(v *Visor) func() []stats.TransportProbe {
	return func() []stats.TransportProbe {
		if v.tpM == nil {
			return nil
		}
		var out []stats.TransportProbe
		v.tpM.WalkTransports(func(tp *transport.ManagedTransport) bool {
			if tp.IsClosed() {
				return true
			}
			bw := tp.GetBandwidth()
			lat := tp.GetLatencyStats()
			out = append(out, stats.TransportProbe{
				ID:        tp.Entry.ID,
				Edges:     []cipher.PubKey{tp.Entry.Edges[0], tp.Entry.Edges[1]},
				Type:      string(tp.Entry.Type),
				Label:     string(tp.Entry.Label),
				SentBytes: bw.SentBytes,
				RecvBytes: bw.RecvBytes,
				LatencyMS: stats.LatencyTriple{Min: lat.Min, Max: lat.Max, Avg: lat.Avg},
			})
			return true
		})
		return out
	}
}

// tierStatesProbe returns a closure that reports current tier
// states. process is true while the visor is alive (it always is
// when this probe runs); dmsg is read from the visor's DMSG client
// readiness; skynet is true when the visor has ≥2 live transports —
// the same criterion TPD uses for "skynet online", so a visor here
// counts as skynet-up only when it is actually routable through.
func tierStatesProbe(v *Visor) func() map[string]bool {
	return func() map[string]bool {
		states := map[string]bool{
			"process": true,
			"dmsg":    isDMSGOnline(v),
			"skynet":  countLiveTransports(v) >= skynetMinTransports,
		}
		return states
	}
}

func isDMSGOnline(v *Visor) bool {
	if v.dmsgC == nil {
		return false
	}
	select {
	case <-v.dmsgC.Ready():
		return true
	default:
		return false
	}
}

func countLiveTransports(v *Visor) int {
	if v.tpM == nil {
		return 0
	}
	n := 0
	v.tpM.WalkTransports(func(tp *transport.ManagedTransport) bool {
		if !tp.IsClosed() {
			n++
		}
		return true
	})
	return n
}

// serviceStatesProbe returns a closure that reports which app
// processes are currently running. The slugs match the app names
// (vpn-server, skysocks, skychat, …); the slug `visor` is always
// reported true since this probe firing is itself proof of liveness.
//
// We capture the full set of running apps rather than filtering to
// only those registered with the Service Discovery — consumers that
// want the SD-only subset can filter client-side from the bitmap
// data.
func serviceStatesProbe(v *Visor) func() map[string]bool {
	return func() map[string]bool {
		states := map[string]bool{"visor": true}
		if v.procM == nil {
			return states
		}
		v.procM.Range(func(appName string, _ *appserver.Proc) bool {
			states[appName] = true
			return true
		})
		return states
	}
}
