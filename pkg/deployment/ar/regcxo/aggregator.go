// Package regcxo pkg/deployment/ar/regcxo/aggregator.go c4-net-discovery
//
// AR-bind-over-CXO aggregator — the address-resolver side of the fan-in
// path. Visors publish their AR bindings (the stcpr/sudph/quic/wt
// reachable-address payloads they POST to /bind) as a CXO feed and
// AnnounceTo this service; the aggregator owns one CXO Node listening on
// DmsgVisorARBindCXOPort, subscribes to each visor's feed on connect, and
// on every filled Root reads the per-type bind leaves and hands each to
// the Sink (the AR API's IngestBindFromCXO).
//
// This moves address binding off the timer-driven re-registration — each a
// fresh dmsg stream with a full Noise handshake (the secp256k1
// handshakeResponder that dominates AR CPU) — onto a persistent CXO
// connection kept warm by the treestore heartbeat. The HTTP/UDP bind path
// remains authoritative, so ingest is idempotent (see IngestBindFromCXO).
//
// The node, the connect-driven subscribe loop, the grace-gated
// orphan-feed reclaim and the service-identity binding all live in
// pkg/cxo/cxoaggregate, shared with the dmsg-discovery and TPD
// aggregators. What is left here is only what is specific to this feed:
// the fixed set of per-transport-type bind leaves.
package regcxo

import (
	"context"
	"encoding/json"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxoaggregate"
	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// logTag prefixes this aggregator's log lines.
const logTag = "ar-bind-cxo aggregator"

// cxoBindLeaf maps a CXO leaf name to the transport type it carries. The leaf
// name is the type's canonical wire string, matching what the visor's AR-bind
// publisher Puts (see addrresolver bind hooks + pkg/visor/init_ar_bind_cxo.go).
type cxoBindLeaf struct {
	name string
	t    types.Type
}

// cxoBindLeaves is the fixed set of per-type leaves the AR-bind feed carries.
var cxoBindLeaves = []cxoBindLeaf{
	{"stcpr", types.STCPR},
	{"sudph", types.SUDPH},
	{"squicr", types.QUIC},
	{"swtr", types.WT},
}

// Sink ingests bindings replicated from visor AR-bind feeds. The AR API
// satisfies it via (*api.API).IngestBindFromCXO.
type Sink interface {
	IngestBindFromCXO(ctx context.Context, reporter cipher.PubKey, tpType types.Type, la addrresolver.LocalAddresses)
}

// Config tunes the aggregator loops. Zero values get sane defaults.
//
// The service SecKey is deliberately NOT here — it is a required
// argument to New. As an optional field on the sibling dmsg-discovery
// aggregator it was omitted, node.NewNode minted a random keypair, and
// every gated visor refused the subscribe (#4569).
type Config struct {
	ReconcileInterval time.Duration
	CleanupInterval   time.Duration
	MaxFillingTime    time.Duration
	Logger            *logging.Logger
	InMemoryDB        bool
	DataDir           string
}

// Aggregator receives visor AR-bind feeds. It is the shared
// cxoaggregate.Core plus this feed's per-type leaf decoding.
type Aggregator struct {
	core *cxoaggregate.Core
	sink Sink
	log  *logging.Logger
}

// New constructs an Aggregator: a CXO Node with DMSG enabled on
// DmsgVisorARBindCXOPort so remote visors can dial in, wired to forward each
// filled Root's bind leaves to sink.
//
// sk is the AR's service secret key. It binds the CXO node identity so the
// aggregator's handshake PK is the AR PK gated visors allowlist (they hold it
// in transport.address_resolver_dmsg).
func New(dmsgC *dmsg.Client, sk cipher.SecKey, sink Sink, conf Config) (*Aggregator, error) {
	if conf.Logger == nil {
		conf.Logger = logging.MustGetLogger("ar-bind-cxo")
	}
	a := &Aggregator{sink: sink, log: conf.Logger}

	core, err := cxoaggregate.New(dmsgC, sk, skyenv.DmsgVisorARBindCXOPort, cxoaggregate.Options{
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

// Run starts the reconcile + cleanup loops. Returns immediately; the loop runs
// until ctx is canceled or Close is called. Idempotent.
func (a *Aggregator) Run(ctx context.Context) { a.core.Run(ctx) }

// Close stops the loops and tears down the CXO node. Idempotent.
func (a *Aggregator) Close() error { return a.core.Close() }

// FeedPK returns the aggregator's own CXO node identity — the AR's service PK.
func (a *Aggregator) FeedPK() cipher.PubKey { return a.core.FeedPK() }

// handleRootFilled reads every per-type bind leaf from a filled Root and
// forwards each decoded LocalAddresses to the Sink. r.Pub is the visor whose
// feed produced this Root — the reporter PK the ingest stores the binding
// under.
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

	for _, bl := range cxoBindLeaves {
		leaf, ok := cxoaggregate.LeafByName(pack, &rootNode, bl.name)
		if !ok || len(leaf) == 0 {
			continue
		}
		var la addrresolver.LocalAddresses
		if err := json.Unmarshal(leaf, &la); err != nil {
			a.log.WithError(err).WithField("visor", reporter).WithField("type", bl.t).
				Debug(logTag + ": bind leaf decode failed")
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		a.sink.IngestBindFromCXO(ctx, reporter, bl.t, la)
		cancel()
	}
}
