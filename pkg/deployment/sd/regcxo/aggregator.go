// Package regcxo pkg/deployment/sd/regcxo/aggregator.go c4-net-discovery
//
// SD-registration-over-CXO aggregator — the service-discovery side of the
// fan-in path. Visors publish their live service-entry set (the type=visor /
// vpn / skysocks / coin registrations they POST to /api/services) as a CXO
// feed and AnnounceTo this service; the aggregator owns one CXO Node
// listening on DmsgVisorSDRegCXOPort, subscribes to each visor's feed on
// connect, and on every filled Root decodes the batched services leaf and
// hands each entry to the Sink (the SD API's IngestServiceFromCXO).
//
// This moves registration off the 90s HTTP re-POST — each a fresh dmsg
// stream with a full Noise handshake (the secp256k1 handshakeResponder that
// dominates discovery-service CPU) — onto a persistent CXO connection kept
// warm by the treestore heartbeat. The HTTP path remains authoritative, so
// ingest is idempotent and no-clobber (see IngestServiceFromCXO).
//
// The node, the connect-driven subscribe loop, the grace-gated orphan-feed
// reclaim and the service-identity binding all live in pkg/cxo/cxoaggregate,
// shared with the dmsg-discovery, AR and TPD aggregators. What is left here
// is only what is specific to this feed: the single batched services leaf.
package regcxo

import (
	"context"
	"encoding/json"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxoaggregate"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// logTag prefixes this aggregator's log lines.
const logTag = "sd-reg-cxo aggregator"

// servicesLeafPath is the single leaf a visor's SD-registration feed
// carries: its WHOLE live service set, republished on every change. Must
// match the visor publisher (pkg/visor/init_sd_reg_cxo.go).
const servicesLeafPath = "services"

// servicesBatchVersion is the wire-format version byte of that leaf's body
// (cxoutils.FrameGzip). A Root carrying any other version is from a newer
// publisher and is skipped rather than misparsed; the visor's HTTP
// registration keeps it discoverable meanwhile.
const servicesBatchVersion = 1

// ingestTimeout bounds one entry's store round-trip. Generous relative to a
// redis pipeline, so a slow store degrades into skipped keepalives rather
// than a wedged CXO event loop.
const ingestTimeout = 10 * time.Second

// Sink ingests service entries replicated from visor SD-registration feeds.
// The SD API satisfies it via (*api.API).IngestServiceFromCXO.
type Sink interface {
	IngestServiceFromCXO(ctx context.Context, reporter cipher.PubKey, se servicedisc.Service)
}

// Config tunes the aggregator loops. Zero values get sane defaults.
//
// The service SecKey is deliberately NOT here — it is a required argument to
// New. As an optional field on the sibling dmsg-discovery aggregator it was
// omitted, node.NewNode minted a random keypair, and every gated visor
// refused the subscribe (#4569).
type Config struct {
	ReconcileInterval time.Duration
	CleanupInterval   time.Duration
	MaxFillingTime    time.Duration
	Logger            *logging.Logger
	InMemoryDB        bool
	DataDir           string
}

// Aggregator receives visor SD-registration feeds. It is the shared
// cxoaggregate.Core plus this feed's batched-leaf decoding.
type Aggregator struct {
	core *cxoaggregate.Core
	sink Sink
	log  *logging.Logger
}

// New constructs an Aggregator: a CXO Node with DMSG enabled on
// DmsgVisorSDRegCXOPort so remote visors can dial in, wired to forward each
// filled Root's service entries to sink.
//
// sk is the SD's service secret key. It binds the CXO node identity so the
// aggregator's handshake PK is the SD PK gated visors allowlist (they hold
// it in launcher.service_discovery / service_discovery_dmsg).
func New(dmsgC *dmsg.Client, sk cipher.SecKey, sink Sink, conf Config) (*Aggregator, error) {
	if conf.Logger == nil {
		conf.Logger = logging.MustGetLogger("sd-reg-cxo")
	}
	a := &Aggregator{sink: sink, log: conf.Logger}

	core, err := cxoaggregate.New(dmsgC, sk, skyenv.DmsgVisorSDRegCXOPort, cxoaggregate.Options{
		ReconcileInterval: conf.ReconcileInterval,
		CleanupInterval:   conf.CleanupInterval,
		MaxFillingTime:    conf.MaxFillingTime,
		Logger:            conf.Logger,
		LogTag:            logTag,
		InMemoryDB:        conf.InMemoryDB,
		DataDir:           conf.DataDir,
		OnRootFilled:      a.handleRootFilled,
		OnFillingBreaks: func(r *registry.Root, reason error) {
			a.log.WithError(reason).WithField("visor", cipher.PubKey(r.Pub)).
				Debug(logTag + ": root filling broke")
		},
	})
	if err != nil {
		return nil, err
	}
	a.core = core
	return a, nil
}

// Run starts the reconcile + cleanup loops. Returns immediately; the loop
// runs until ctx is canceled or Close is called. Idempotent.
func (a *Aggregator) Run(ctx context.Context) { a.core.Run(ctx) }

// Close stops the loops and tears down the CXO node. Idempotent.
func (a *Aggregator) Close() error { return a.core.Close() }

// FeedPK returns the aggregator's own CXO node identity — the SD's service PK.
func (a *Aggregator) FeedPK() cipher.PubKey { return a.core.FeedPK() }

// handleRootFilled decodes the batched services leaf from a filled Root and
// forwards every entry to the Sink. r.Pub is the visor whose feed produced
// this Root — the reporter PK the ingest attributes the entries to.
func (a *Aggregator) handleRootFilled(r *registry.Root) {
	if r == nil || len(r.Refs) == 0 {
		return
	}
	reporter := cipher.PubKey(r.Pub)
	if reporter == (cipher.PubKey{}) {
		a.log.Debug(logTag + ": dropping root with zero publisher PK")
		return
	}
	pack, err := a.core.Container().Pack(r, treestore.Registry)
	if err != nil {
		a.log.WithError(err).Debug(logTag + ": get pack failed")
		return
	}
	var rootNode treestore.TreeNode
	if err := r.Refs[0].Value(pack, &rootNode); err != nil {
		a.log.WithError(err).Debug(logTag + ": decode root TreeNode failed")
		return
	}

	leaf, ok := cxoaggregate.LeafByName(pack, &rootNode, servicesLeafPath)
	if !ok || len(leaf) == 0 {
		// A visor with no live services publishes no leaf at all — its
		// entries are meant to lapse on the store TTL. Nothing to do.
		return
	}
	entries, ok := decodeServicesBatch(leaf)
	if !ok {
		a.log.WithField("visor", reporter).Debug(logTag + ": services leaf decode failed")
		return
	}
	for i := range entries {
		ctx, cancel := context.WithTimeout(context.Background(), ingestTimeout)
		a.sink.IngestServiceFromCXO(ctx, reporter, entries[i])
		cancel()
	}
}

// decodeServicesBatch unframes + gunzips the batched leaf body and decodes
// the JSON service array. A version byte this build does not know yields
// ok=false, so an older SD skips a newer publisher's leaf cleanly instead of
// misparsing it.
func decodeServicesBatch(body []byte) ([]servicedisc.Service, bool) {
	version, payload, ok := cxoutils.UnframeGzip(body)
	if !ok || version != servicesBatchVersion {
		return nil, false
	}
	var out []servicedisc.Service
	if err := json.Unmarshal(payload, &out); err != nil {
		return nil, false
	}
	return out, true
}
