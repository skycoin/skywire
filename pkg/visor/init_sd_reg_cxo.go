// Package visor pkg/visor/init_sd_reg_cxo.go c3-vis-core
//
// SD-registration-over-CXO publisher. The visor mirrors its own
// service-discovery entries — the type=visor / vpn / skysocks / coin
// registrations pkg/app/appdisc POSTs to /api/services and re-POSTs every
// 90s (skyenv.ServiceDiscUpdateInterval) — onto a persistent CXO feed on
// DmsgVisorSDRegCXOPort, and announces to the service-discovery, which
// subscribes back and ingests them. This moves the SD heartbeat off the
// timer-driven re-registration, each a fresh dmsg stream with a full Noise
// handshake (the secp256k1 handshakeResponder that dominates discovery-
// service CPU), onto one warm CXO connection.
//
// Purely ADDITIVE dual-write, exactly as for the AR-bind, dmsg-registration
// and TPD feeds: the SD clients keep doing the HTTP POST / DELETE on the
// same schedule (the authoritative path), and this feed is inert until an
// SD subscribes to it.
//
// # What is published
//
// A visor's service set is per-app and changes when apps start and stop, so
// the feed carries ONE batched leaf holding the visor's WHOLE current live
// set, republished on every change:
//
//	services        // FrameGzip(v1, JSON []servicedisc.Service)
//
// The entries are verbatim what the SD accepted and echoed back (the
// clients feed them in through servicedisc.EntrySink), so the ingest is a
// drop-in mirror of the HTTP register.
//
// Deregistration is ABSENCE, not a tombstone — the same rule SD's own
// publisher documents (pkg/deployment/sd/api/cxo_publisher.go). A service
// that stops is simply missing from the next batch, and SD's Redis store
// expires it on the existing entry TTL. When the set empties, the leaf
// itself is deleted.
package visor

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// sdRegLeafPath is the single CXO leaf the visor publishes its whole live
// service set at. One leaf per feed keeps the Root a fixed two objects
// however many apps are registered, so it always fills in one round-trip.
const sdRegLeafPath = "services"

// sdRegBatchVersion is the wire-format version byte of the batched leaf
// body (cxoutils.FrameGzip). Bump on any breaking change to the encoding;
// readers compare it and fall back rather than misparsing.
const sdRegBatchVersion = 1

// sdRegBatchWindow coalesces service-set mutations into the CXO datastore.
// App start/stop is rare and the 90s heartbeat re-register is a keepalive
// that usually re-encodes to identical bytes, so a short window adds no
// latency while batching the startup burst (visor + vpn + skysocks entries
// all registering at once) into a single publish.
const sdRegBatchWindow = 5 * time.Second

// sdRegAnnounceInterval is how often the visor pokes its CXO feed conn to
// the SD. Short enough that a recovered visor/SD re-links well inside the
// entry TTL.
const sdRegAnnounceInterval = 30 * time.Second

// sdEntryMirror holds the visor's live service-discovery entry set and
// mirrors it onto the SD-registration CXO feed. It implements
// servicedisc.EntrySink, and every SD client the visor builds is handed it
// (see appdisc.Factory.EntrySink), so the set spans all of the visor's
// apps rather than one client's single entry.
//
// It exists from NewVisor onward and is publisher-agnostic: entries
// accumulate whether or not the CXO feed ever starts, and attaching a
// publisher later flushes whatever has accumulated. That ordering
// independence is deliberate — apps can register before (or without) the
// CXO module running.
type sdEntryMirror struct {
	mu      sync.Mutex
	entries map[string]servicedisc.Service
	pub     *treestore.Publisher
	log     *logging.Logger
}

func newSDEntryMirror() *sdEntryMirror {
	return &sdEntryMirror{entries: make(map[string]servicedisc.Service)}
}

// sdEntryKey identifies one registration: an entry is unique per service
// type AND address, matching SD's own (type, addr) store key — a visor can
// hold a type=visor and a type=skysocks entry at once, and two apps of one
// type on different routing ports are two entries.
func sdEntryKey(e servicedisc.Service) string {
	return e.Type + "|" + e.Addr.String()
}

// PutEntry records a service entry the SD accepted and republishes the set.
// Implements servicedisc.EntrySink.
func (m *sdEntryMirror) PutEntry(entry servicedisc.Service) {
	if m == nil || entry.Type == "" || entry.Addr.PubKey().Null() {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries[sdEntryKey(entry)] = entry
	m.flushLocked()
}

// DelEntry drops a deregistered entry and republishes the set. Its absence
// from the next batch IS the deregistration — no tombstone. Implements
// servicedisc.EntrySink.
func (m *sdEntryMirror) DelEntry(entry servicedisc.Service) {
	if m == nil || entry.Type == "" {
		return
	}
	key := sdEntryKey(entry)
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.entries[key]; !ok {
		return
	}
	delete(m.entries, key)
	m.flushLocked()
}

// setPublisher attaches (or, with nil, detaches) the CXO publisher and
// immediately flushes the current set, so a feed that starts after the
// first registrations still carries them.
func (m *sdEntryMirror) setPublisher(pub *treestore.Publisher, log *logging.Logger) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pub = pub
	m.log = log
	if pub != nil {
		m.flushLocked()
	}
}

// flushLocked re-encodes the whole live set into the batched leaf, or
// deletes the leaf when the set is empty. CXO is content-addressed, so a
// re-encode of an unchanged set is a wire no-op — which is what a steady
// 90s heartbeat produces. Caller holds m.mu.
func (m *sdEntryMirror) flushLocked() {
	if m.pub == nil {
		return
	}
	if len(m.entries) == 0 {
		if err := m.pub.Delete(sdRegLeafPath); err != nil && m.log != nil {
			m.log.WithError(err).Debug("SD-reg-CXO: delete of emptied services leaf failed")
		}
		return
	}
	blob, err := encodeSDEntries(m.entries)
	if err != nil {
		if m.log != nil {
			m.log.WithError(err).Debug("SD-reg-CXO: marshal service set failed")
		}
		return
	}
	if err := m.pub.Put(sdRegLeafPath, blob); err != nil && m.log != nil {
		m.log.WithError(err).Debug("SD-reg-CXO: CXO Put failed")
	}
}

// encodeSDEntries serializes the live set as one leaf body: a JSON array of
// servicedisc.Service sorted by (type, addr) so an unchanged set re-encodes
// to identical bytes, then version-framed + gzipped.
func encodeSDEntries(entries map[string]servicedisc.Service) ([]byte, error) {
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]servicedisc.Service, 0, len(keys))
	for _, k := range keys {
		out = append(out, entries[k])
	}
	payload, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	return cxoutils.FrameGzip(sdRegBatchVersion, payload), nil
}

func initSDRegCXO(_ context.Context, v *Visor, log *logging.Logger) error {
	if v.dmsgC == nil {
		log.Warn("SD-reg-CXO: dmsg client absent; publisher not started")
		return nil
	}
	// The whole point is to announce to the SD so it subscribes back.
	// Without a dmsg:// service-discovery PK there is no announce target, so
	// skip rather than run an orphan publisher.
	sdPK, ok := sdCXOPeer(v)
	if !ok {
		log.Warn("SD-reg-CXO: no dmsg:// service_discovery PK; cannot announce to SD; skipping")
		return nil
	}

	dataDir := filepath.Join(v.conf.LocalPath, "cxo-sd-reg")
	// Gate the feed: peer whitelist (hypervisors + dmsgpty whitelist + own
	// PK) plus the consuming SD. The SD MUST be allowed or its announce-conn
	// subscribe is rejected by the OnSubscribeRemote hook. sdPK is always
	// known here (we returned early above otherwise).
	allow := composeFeedAllowlist(v, sdPK, true)
	pub, err := treestore.NewWithDMSG(v.dmsgC, v.conf.SK, treestore.PubConfig{
		DmsgPort:            skyenv.DmsgVisorSDRegCXOPort,
		BatchWindow:         sdRegBatchWindow,
		Logger:              log,
		DataDir:             dataDir,
		SubscriberAllowlist: allow,
		// The leaf is rebuilt from the SD clients' live registrations on
		// every restart (they re-register on boot), so skipping per-tx
		// fdatasync is safe (matches the AR-bind + registration publishers).
		NoSyncCXDS: true,
	})
	if err != nil {
		log.WithError(err).Warn("SD-reg-CXO: publisher init failed; continuing with HTTP service registration only")
		return nil
	}

	// Register so the feed's subscriber allowlist is recomputed and
	// re-applied when the peer whitelist changes at runtime. The initial
	// allowlist is already set at construction via PubConfig above.
	v.registerGatedCXOFeed(pub, sdPK, true)
	// Surface this feed's gating in `visor state`: a refused SD subscribe is
	// otherwise invisible from both ends.
	v.setSDRegCXOPub(pub)

	// Attach the publisher to the mirror. Entries registered before this
	// point (the boot burst) are flushed immediately.
	v.sdEntryMirror.setPublisher(pub, log)

	// Dial the SD so its aggregator sees the inbound conn and subscribes to
	// our feed (PK = our PK). ConnectPK is idempotent, so the same loop
	// handles initial announce + reconnect.
	lastAnnounceOK := new(atomic.Int64)
	go runSDRegAnnounceLoop(v.ctx, pub, sdPK, lastAnnounceOK, log)

	v.pushCloseStack("sd_reg_cxo", func() error {
		v.sdEntryMirror.setPublisher(nil, nil)
		return pub.Close()
	})

	log.WithField("feed_pk", pub.Feed()).WithField("sd_pk", sdPK).
		WithField("port", skyenv.DmsgVisorSDRegCXOPort).
		Info("SD-reg-CXO: publisher running (service entries mirrored to service-discovery)")
	return nil
}

// runSDRegAnnounceLoop dials the SD on a ticker (ConnectPK is idempotent —
// a live conn is a no-op, a dropped one redials) and stamps lastOK with the
// wall-clock time of each success.
func runSDRegAnnounceLoop(ctx context.Context, pub *treestore.Publisher, sdPK cipher.PubKey, lastOK *atomic.Int64, log *logging.Logger) {
	t := time.NewTicker(sdRegAnnounceInterval)
	defer t.Stop()
	announce := func() {
		dctx, cancel := context.WithTimeout(ctx, sdRegAnnounceInterval/2)
		defer cancel()
		if err := pub.AnnounceTo(dctx, sdPK); err != nil {
			log.WithError(err).WithField("sd_pk", sdPK).Trace("SD-reg-CXO: announce to service-discovery failed")
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
