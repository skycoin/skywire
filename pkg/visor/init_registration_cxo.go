// Package visor pkg/visor/init_registration_cxo.go c3-vis-core
//
// Registration-over-CXO publisher. When the visor opts in
// (Dmsg.RegistrationCXO), it publishes its own signed discovery entry as
// a CXO feed on DmsgDMSGDRegistrationCXOPort and announces to the
// deployment's dmsg-discovery, which subscribes back and ingests the
// entry. This moves client-entry registration off the timer-driven HTTP
// PUT — each a fresh dmsg stream with a full Noise + post-quantum
// handshake, the load that dominates dmsg-discovery CPU — onto a
// persistent CXO connection kept warm by the treestore heartbeat.
//
// The entry is published verbatim from the dmsg client's canonical
// registration path (see EntityCommon.SetEntryPublishHook): the same
// bytes, sequence and signature dmsg-discovery accepts over HTTP, so the
// CXO ingest is a drop-in mirror. HTTP PUT is kept alongside as the
// fallback, so this is safe to enable ahead of the server-side
// aggregator rollout — it is simply inert until a dmsg-discovery
// subscribes to the feed.
package visor

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	dmsgdisc "github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// registrationEntryPath is the single CXO leaf path the visor publishes
// its discovery entry at. The dmsg-discovery aggregator reads this leaf,
// unmarshals a disc.Entry, and ingests it. One leaf per feed (feed PK =
// visor PK = entry.Static), so there is never more than this one value.
const registrationEntryPath = "entry"

// registrationBatchWindow coalesces entry mutations into the CXO
// datastore. Registrations change rarely (only when the delegated-server
// set changes), so a short window adds no latency while still batching
// the startup burst of session establishes into one publish.
const registrationBatchWindow = 5 * time.Second

func initRegistrationCXO(_ context.Context, v *Visor, log *logging.Logger) error {
	if v.dmsgC == nil {
		log.Warn("Registration-CXO: dmsg client absent; publisher not started")
		return nil
	}
	// The whole point is to announce to dmsg-discovery so it subscribes
	// back. Without a dmsg:// discovery PK there is no announce target, so
	// the feed would never be consumed — skip rather than run an orphan
	// publisher.
	dmsgdPK, ok := dmsgdCXOPeer(v)
	if !ok {
		log.Warn("Registration-CXO: no dmsg.discovery_dmsg PK; cannot announce to dmsg-discovery; skipping")
		return nil
	}

	dataDir := filepath.Join(v.conf.LocalPath, "cxo-registration")
	pub, err := treestore.NewWithDMSG(v.dmsgC, v.conf.SK, treestore.PubConfig{
		DmsgPort:    skyenv.DmsgDMSGDRegistrationCXOPort,
		BatchWindow: registrationBatchWindow,
		Logger:      log,
		DataDir:     dataDir,
		// The entry is content-addressed and rebuilt from the dmsg client's
		// live registration on every restart, so skipping per-tx fdatasync
		// is safe (matches the telemetry publisher).
		NoSyncCXDS: true,
	})
	if err != nil {
		log.WithError(err).Warn("Registration-CXO: publisher init failed; continuing with HTTP registration only")
		return nil
	}

	// Mirror every successful primary-discovery registration onto the feed.
	// The hook runs on the dmsg entry-update goroutine and must not block;
	// Put coalesces into the next BatchWindow tick, so it returns at once.
	v.dmsgC.SetEntryPublishHook(func(entry *dmsgdisc.Entry) {
		b, mErr := json.Marshal(entry)
		if mErr != nil {
			log.WithError(mErr).Debug("Registration-CXO: marshal entry failed")
			return
		}
		if pErr := pub.Put(registrationEntryPath, b); pErr != nil {
			log.WithError(pErr).Debug("Registration-CXO: CXO Put failed")
		}
	})

	// Dial dmsg-discovery so its aggregator sees the inbound conn and
	// subscribes to our feed (PK = our PK). ConnectPK is idempotent, so the
	// same loop handles initial announce + reconnect. Each success stamps
	// lastAnnounceOK, which drives the keepalive-health predicate below.
	lastAnnounceOK := new(atomic.Int64)
	go runRegistrationAnnounceLoop(v.ctx, pub, dmsgdPK, lastAnnounceOK, log)

	// Tell the dmsg client the CXO registration keepalive is healthy while we
	// have recently reached dmsg-discovery over the feed conn. While healthy,
	// the client stretches its periodic HTTP keepalive re-PUT (see
	// EntityCommon.cxoKeepaliveHealthyFn); if the feed conn drops, announces
	// stop succeeding and this falls back to false within the window, so the
	// frequent HTTP keepalive resumes. Delegated-server CHANGES are unaffected
	// (they publish immediately over both HTTP and CXO).
	v.dmsgC.SetCXOKeepaliveHealthyFunc(func() bool {
		last := lastAnnounceOK.Load()
		return last != 0 && time.Since(time.Unix(0, last)) < cxoKeepaliveHealthyWindow
	})

	v.pushCloseStack("registration_cxo", func() error {
		v.dmsgC.SetCXOKeepaliveHealthyFunc(nil)
		v.dmsgC.SetEntryPublishHook(nil)
		return pub.Close()
	})

	log.WithField("feed_pk", pub.Feed()).WithField("dmsgd_pk", dmsgdPK).
		WithField("port", skyenv.DmsgDMSGDRegistrationCXOPort).
		Info("Registration-CXO: publisher running (entry mirrored to dmsg-discovery)")
	return nil
}

// registrationAnnounceInterval is how often the visor pokes its CXO feed conn
// to dmsg-discovery. Short enough that a recovered visor/dmsg-disc re-links
// within the health window.
const registrationAnnounceInterval = 30 * time.Second

// cxoKeepaliveHealthyWindow is how long after the last successful announce the
// registration-over-CXO keepalive is still considered healthy. ~3 announce
// intervals of slack, so one missed announce doesn't flap the HTTP-keepalive
// fallback, while a genuinely dropped feed conn falls back within a minute or
// two.
const cxoKeepaliveHealthyWindow = 95 * time.Second

// runRegistrationAnnounceLoop dials dmsg-discovery on a ticker (ConnectPK is
// idempotent — a live conn is a no-op, a dropped one redials) and stamps
// lastOK with the wall-clock time of each success. lastOK drives the client's
// keepalive-health predicate.
func runRegistrationAnnounceLoop(ctx context.Context, pub *treestore.Publisher, dmsgdPK cipher.PubKey, lastOK *atomic.Int64, log *logging.Logger) {
	t := time.NewTicker(registrationAnnounceInterval)
	defer t.Stop()
	announce := func() {
		dctx, cancel := context.WithTimeout(ctx, registrationAnnounceInterval/2)
		defer cancel()
		if err := pub.AnnounceTo(dctx, dmsgdPK); err != nil {
			log.WithError(err).WithField("dmsgd_pk", dmsgdPK).Trace("Registration-CXO: announce to dmsg-discovery failed")
			return
		}
		lastOK.Store(time.Now().UnixNano())
	}
	announce()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			announce()
		}
	}
}
