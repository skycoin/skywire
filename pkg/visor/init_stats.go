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
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/visor/stats"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

const (
	defaultStatsSampleInterval = time.Minute
	defaultStatsRetentionDays  = 30
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

	tracker := stats.NewTracker(store, stats.Probes{
		Transports:    transportsProbe(v),
		TierStates:    tierStatesProbe(v),
		ServiceStates: serviceStatesProbe(v),
	}, stats.Config{
		SampleInterval: sampleInterval(conf),
		RetentionDays:  retentionDays(conf),
		Logger:         log,
	})

	tracker.Run(v.ctx)
	v.statsTracker = tracker
	v.pushCloseStack("stats", func() error {
		return tracker.Close()
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

	log.WithField("path", path).Info("Stats: telemetry store running")
	return nil
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
// readiness; skynet derives from the live transport count.
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

