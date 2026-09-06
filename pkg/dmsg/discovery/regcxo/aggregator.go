// Package regcxo pkg/dmsg/discovery/regcxo/aggregator.go c1-net-dmsg
//
// Registration-over-CXO aggregator — the dmsg-discovery side of the
// fan-in path. Visors that opt into registration_cxo publish their own
// signed disc.Entry as a single-leaf CXO feed and AnnounceTo this
// service; the aggregator owns one CXO Node listening on
// DmsgDMSGDRegistrationCXOPort, subscribes to each visor's feed on
// connect, and on every filled Root reads the "entry" leaf and hands the
// entry to the Sink (the discovery API's IngestEntryFromCXO).
//
// This moves client-entry registration off the timer-driven HTTP PUT —
// each a fresh dmsg stream with a full Noise + post-quantum handshake,
// the load that dominates dmsg-discovery CPU — onto a persistent CXO
// connection kept warm by the treestore heartbeat. HTTP PUT remains the
// fallback, so ingest is idempotent (see Sink.IngestEntryFromCXO).
//
// The node, the connect-driven subscribe loop, the grace-gated
// orphan-feed reclaim and the service-identity binding all live in
// pkg/cxo/cxoaggregate, shared with the AR and TPD aggregators. What is
// left here is only what is specific to this feed: a single "entry"
// leaf carrying a disc.Entry.
package regcxo

import (
	"context"
	"encoding/json"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxoaggregate"
	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// registrationEntryPath is the single leaf path a visor publishes its
// discovery entry at. MUST match pkg/visor's registrationEntryPath.
const registrationEntryPath = "entry"

// logTag prefixes this aggregator's log lines.
const logTag = "registration-cxo aggregator"

// Sink ingests entries replicated from visor registration feeds. The
// discovery API satisfies it via (*api.API).IngestEntryFromCXO.
type Sink interface {
	IngestEntryFromCXO(ctx context.Context, entry *disc.Entry, reporter cipher.PubKey)
}

// Config tunes the aggregator loops. Zero values get sane defaults.
//
// The service SecKey is deliberately NOT here — it is a required
// argument to New. As an optional field it was omitted, node.NewNode
// minted a random keypair, and every gated visor refused the subscribe
// (#4569).
type Config struct {
	ReconcileInterval time.Duration
	CleanupInterval   time.Duration
	MaxFillingTime    time.Duration
	Logger            *logging.Logger
	InMemoryDB        bool
	DataDir           string
}

// Aggregator receives visor registration feeds. It is the shared
// cxoaggregate.Core plus this feed's entry-leaf decoding.
type Aggregator struct {
	core *cxoaggregate.Core
	sink Sink
	log  *logging.Logger
}

// New constructs an Aggregator: a CXO Node with DMSG enabled on
// DmsgDMSGDRegistrationCXOPort so remote visors can dial in, wired to
// forward each filled Root's entry leaf to sink.
//
// sk is dmsg-discovery's service secret key. It binds the CXO node
// identity so the aggregator's handshake PK is the PK gated visors
// allowlist.
func New(dmsgC *dmsg.Client, sk cipher.SecKey, sink Sink, conf Config) (*Aggregator, error) {
	if conf.Logger == nil {
		conf.Logger = logging.MustGetLogger("dmsgd-registration-cxo")
	}
	a := &Aggregator{sink: sink, log: conf.Logger}

	core, err := cxoaggregate.New(dmsgC, sk, skyenv.DmsgDMSGDRegistrationCXOPort, cxoaggregate.Options{
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

// FeedPK returns the aggregator's own CXO node identity — dmsg-discovery's
// service PK.
func (a *Aggregator) FeedPK() cipher.PubKey { return a.core.FeedPK() }

// handleRootFilled reads the "entry" leaf from a filled Root and forwards
// the decoded disc.Entry to the Sink. r.Pub is the visor whose feed
// produced this Root — the reporter PK the ingest checks against
// entry.Static.
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

	leaf, ok := cxoaggregate.LeafByName(pack, &rootNode, registrationEntryPath)
	if !ok || len(leaf) == 0 {
		return
	}
	entry := new(disc.Entry)
	if err := json.Unmarshal(leaf, entry); err != nil {
		a.log.WithError(err).WithField("visor", reporter).
			Debug(logTag + ": entry leaf decode failed")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a.sink.IngestEntryFromCXO(ctx, entry, reporter)
}
