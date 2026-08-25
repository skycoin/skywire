// Package visor pkg/visor/init_ar_bind_cxo.go c3-vis-core
//
// AR-bind-over-CXO publisher. The visor mirrors its address-resolver
// bindings — the same stcpr/sudph/quic/wt reachable-address payloads it
// POSTs to /bind (and re-registers over a fresh dmsg stream every ~90s) —
// onto a persistent CXO feed on DmsgVisorARBindCXOPort and announces to
// the AR, which subscribes back and ingests them. This moves the address
// binding off the timer-driven re-registration, each a fresh dmsg stream
// with a full Noise handshake (the secp256k1 ECDH handshakeResponder that
// dominates AR CPU), onto one warm CXO connection.
//
// Purely ADDITIVE dual-write: the AR client keeps doing the HTTP POST /
// UDP registration exactly as before (the authoritative/fallback path).
// The publisher is fed by a hook the AR client fires on every successful
// bind (see addrresolver.BindPublisher), so the CXO leaf always carries
// the exact LocalAddresses the visor last registered. It is inert until an
// AR subscribes to the feed.
package visor

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
)

// arBindBatchWindow coalesces bind mutations into the CXO datastore. Binds
// change rarely (only when a reachable address changes) and the SUDPH
// re-registration is a periodic keepalive, so a short window adds no latency
// while batching the startup burst of per-type binds into one publish.
const arBindBatchWindow = 5 * time.Second

// arBindAnnounceInterval is how often the visor pokes its CXO feed conn to the
// AR. Short enough that a recovered visor/AR re-links within the health window.
const arBindAnnounceInterval = 30 * time.Second

func initARBindCXO(_ context.Context, v *Visor, log *logging.Logger) error {
	if v.dmsgC == nil {
		log.Warn("AR-bind-CXO: dmsg client absent; publisher not started")
		return nil
	}
	// The whole point is to announce to the AR so it subscribes back. Without
	// a dmsg:// AR PK there is no announce target, so skip rather than run an
	// orphan publisher.
	arPK, ok := arBindCXOPeer(v)
	if !ok {
		log.Warn("AR-bind-CXO: no dmsg:// address_resolver PK; cannot announce to AR; skipping")
		return nil
	}

	// The publisher is driven by the AR client's bind hook. If the AR client
	// isn't the hook-capable http client (or hasn't been constructed), there is
	// nothing to mirror — skip. arBindCXOMod depends on the address_resolver
	// module, so v.arClient is normally set by the time we run.
	v.initLock.Lock()
	arClient := v.arClient
	v.initLock.Unlock()
	bp, ok := arClient.(addrresolver.BindPublisher)
	if !ok {
		log.Warn("AR-bind-CXO: AR client does not support bind publish hook; skipping")
		return nil
	}

	dataDir := filepath.Join(v.conf.LocalPath, "cxo-ar-bind")
	pub, err := treestore.NewWithDMSG(v.dmsgC, v.conf.SK, treestore.PubConfig{
		DmsgPort:    skyenv.DmsgVisorARBindCXOPort,
		BatchWindow: arBindBatchWindow,
		Logger:      log,
		DataDir:     dataDir,
		// Each leaf is rebuilt from the AR client's live bind hook on every
		// restart (the AR re-registers on boot), so skipping per-tx fdatasync
		// is safe (matches the registration + telemetry publishers).
		NoSyncCXDS: true,
	})
	if err != nil {
		log.WithError(err).Warn("AR-bind-CXO: publisher init failed; continuing with HTTP/UDP AR registration only")
		return nil
	}

	// Mirror every successful AR bind onto the feed, one leaf per transport
	// type (leaf name == the type's canonical wire string). The hook runs on
	// the AR client's bind goroutine and must not block; Put coalesces into the
	// next BatchWindow tick, so it returns at once.
	bp.SetBindPublishHook(func(netType string, payload addrresolver.LocalAddresses) {
		b, mErr := json.Marshal(payload)
		if mErr != nil {
			log.WithError(mErr).Debug("AR-bind-CXO: marshal bind payload failed")
			return
		}
		if pErr := pub.Put(netType, b); pErr != nil {
			log.WithError(pErr).Debug("AR-bind-CXO: CXO Put failed")
		}
	})

	// Dial the AR so its aggregator sees the inbound conn and subscribes to our
	// feed (PK = our PK). ConnectPK is idempotent, so the same loop handles
	// initial announce + reconnect.
	lastAnnounceOK := new(atomic.Int64)
	go runARBindAnnounceLoop(v.ctx, pub, arPK, lastAnnounceOK, log)

	v.pushCloseStack("ar_bind_cxo", func() error {
		bp.SetBindPublishHook(nil)
		return pub.Close()
	})

	log.WithField("feed_pk", pub.Feed()).WithField("ar_pk", arPK).
		WithField("port", skyenv.DmsgVisorARBindCXOPort).
		Info("AR-bind-CXO: publisher running (bindings mirrored to address-resolver)")
	return nil
}

// arBindCXOPeer extracts the AR's dmsg publisher PK from the visor's transport
// config, preferring the explicit AddressResolverDmsg URL and falling back to
// AddressResolver (the dmsg-only default stores the dmsg:// URL there). Mirrors
// initAddressResolver's URL selection.
func arBindCXOPeer(v *Visor) (cipher.PubKey, bool) {
	if v.conf.Transport == nil {
		return cipher.PubKey{}, false
	}
	raw := v.conf.Transport.AddressResolverDmsg
	if raw == "" {
		raw = v.conf.Transport.AddressResolver
	}
	return parseDmsgPeer(raw)
}

// runARBindAnnounceLoop dials the AR on a ticker (ConnectPK is idempotent — a
// live conn is a no-op, a dropped one redials) and stamps lastOK with the
// wall-clock time of each success.
func runARBindAnnounceLoop(ctx context.Context, pub *treestore.Publisher, arPK cipher.PubKey, lastOK *atomic.Int64, log *logging.Logger) {
	t := time.NewTicker(arBindAnnounceInterval)
	defer t.Stop()
	announce := func() {
		dctx, cancel := context.WithTimeout(ctx, arBindAnnounceInterval/2)
		defer cancel()
		if err := pub.AnnounceTo(dctx, arPK); err != nil {
			log.WithError(err).WithField("ar_pk", arPK).Trace("AR-bind-CXO: announce to AR failed")
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
