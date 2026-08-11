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
	"time"

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
	if v.conf.Dmsg == nil || !v.conf.Dmsg.RegistrationCXO {
		log.Debug("Registration-CXO: not opted in (dmsg.registration_cxo=false); skipping")
		return nil
	}
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
	// same loop handles initial announce + reconnect. Reuses the telemetry
	// announce loop (identical shape).
	go runAnnounceLoop(v.ctx, pub, dmsgdPK, log)

	v.pushCloseStack("registration_cxo", func() error {
		v.dmsgC.SetEntryPublishHook(nil)
		return pub.Close()
	})

	log.WithField("feed_pk", pub.Feed()).WithField("dmsgd_pk", dmsgdPK).
		WithField("port", skyenv.DmsgDMSGDRegistrationCXOPort).
		Info("Registration-CXO: publisher running (entry mirrored to dmsg-discovery)")
	return nil
}
