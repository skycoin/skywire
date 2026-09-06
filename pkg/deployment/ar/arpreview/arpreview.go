// Package arpreview pkg/deployment/ar/arpreview/arpreview.go c4-net-discovery
//
// Preview-backed reads of the address resolver's CXO bindings feed: resolve
// one peer's transport addresses over an already-open CXO connection, holding
// nothing afterwards.
//
// This is the consumer half of #4584. The AR's own feed
// (pkg/deployment/ar/api/cxo_publisher.go) publishes a dense 256-bucket level
// keyed by peer public key, which lets a lookup here be a FIXED walk — root
// TreeNode, then one indexed child fetch — regardless of how many peers the
// network holds. The reader never subscribes, so it costs no resident copy and
// no cold first sync.
//
// It lives outside pkg/transport/network/addrresolver on purpose. The AR
// client is imported by pkg/cxo (through arfeed's VisorData), so it can never
// import CXO itself; the visor imports both and wires this in. Same dependency
// inversion as dmsg's EntryResolver seam.
//
// Positioning: this is a FAST PATH, never an authority. Any miss, any error,
// any budget exhaustion returns "I don't know" and the caller falls through to
// the authenticated HTTP GET /resolve, which remains the source of truth. The
// feed lags the store by the publisher's flush plus batch window, so a binding
// created seconds ago may not be here yet — which is exactly why a miss must
// be cheap and must fall through rather than being cached as a negative.
//
// # Who should use this, measured
//
// Over the live dmsg network against a 1711-peer feed, 40 lookups: this path
// runs at a p50 of 16.4 ms holding nothing, where the authenticated HTTP
// /resolve it fronts runs at 140.3 ms, and a warm subscription to the same
// feed answers in 119 us for 100 KB resident and a 0.8 s cold sync.
//
// So it is the right tool for a caller that cannot hold the feed — a browser
// tab, a microcontroller, a short-lived CLI run, anything resolving a handful
// of peers — and the WRONG tool for a long-running visor that dials constantly
// and can spare 100 KB, which should subscribe instead. It is deliberately not
// wired into the visor dial path for that reason.
package arpreview

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxoaggregate"
	"github.com/skycoin/skywire/pkg/cxo/cxopreview"
	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/deployment/ar/arfeed"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// ErrNoEntry means the feed carried no binding of the requested type for that
// peer. It is deliberately the same shape of answer as a 404 from GET
// /resolve, but callers should treat it as "not here" and fall through rather
// than as a definitive absence: the feed lags the store.
var ErrNoEntry = errors.New("arpreview: no binding in the AR feed")

// Resolver reads one peer's bindings out of the AR's CXO feed.
type Resolver struct {
	log    *logging.Logger
	reader *cxopreview.Reader
	arPK   cipher.PubKey

	// lastStats is diagnostic only, but Bindings is safe to call from several
	// goroutines (a dial path is), so the write needs guarding.
	mu        sync.Mutex
	lastStats cxopreview.Stats
}

// Config tunes a Resolver.
type Config struct {
	Logger *logging.Logger
	// Port is the AR's bindings-feed dmsg port; zero means
	// skyenv.DmsgARBindingsCXOPort.
	Port uint16
	// MaxObjects bounds one lookup. A correctly-shaped bucket walk needs
	// single digits; zero takes the cxopreview default.
	MaxObjects int
}

// New builds a Resolver against the address resolver at arPK. sk is the
// identity the reader's CXO node presents; pass the visor's own so the AR sees
// a known peer rather than a fresh key on every restart.
//
// It does NOT dial. Call Connect once at setup — a lookup that has to dial
// first has given up the property that makes this worth doing.
func New(dmsgC *dmsg.Client, sk cipher.SecKey, arPK cipher.PubKey, conf Config) (*Resolver, error) {
	if arPK == (cipher.PubKey{}) {
		return nil, errors.New("arpreview: zero address-resolver public key")
	}
	if conf.Logger == nil {
		conf.Logger = logging.MustGetLogger("ar-preview")
	}
	port := conf.Port
	if port == 0 {
		port = skyenv.DmsgARBindingsCXOPort
	}
	reader, err := cxopreview.NewDMSG(dmsgC, sk, port, cxopreview.Config{
		Logger:     conf.Logger,
		MaxObjects: conf.MaxObjects,
	})
	if err != nil {
		return nil, err
	}
	return NewWithReader(reader, arPK, conf.Logger), nil
}

// NewWithReader builds a Resolver over an already-constructed preview reader.
// New is the normal entry point; this one exists so a caller that already owns
// a reader (or one running over the CXO node's native TCP transport, as the
// end-to-end test does) can reuse it.
func NewWithReader(reader *cxopreview.Reader, arPK cipher.PubKey, log *logging.Logger) *Resolver {
	if log == nil {
		log = logging.MustGetLogger("ar-preview")
	}
	return &Resolver{log: log, reader: reader, arPK: arPK}
}

// Reader exposes the underlying preview reader — for setup (dialing over a
// non-dmsg transport) and for assertions about what it is holding.
func (r *Resolver) Reader() *cxopreview.Reader { return r.reader }

// Connect opens the CXO connection to the address resolver, and keeps it open.
func (r *Resolver) Connect(ctx context.Context) error {
	return r.reader.Connect(ctx, r.arPK)
}

// Connected reports whether the connection to the AR is up.
func (r *Resolver) Connected() bool { return r.reader.Connected(r.arPK) }

// Close releases the reader's CXO node.
func (r *Resolver) Close() error { return r.reader.Close() }

// Bindings returns every binding the feed holds for pk, plus what the lookup
// cost. The walk is: preview the Root, read the root TreeNode, fetch the one
// bucket child at the index pk's own key names. Nothing else is touched and
// nothing is retained.
func (r *Resolver) Bindings(ctx context.Context, pk cipher.PubKey) (*arfeed.PeerBindings, cxopreview.Stats, error) {
	var rec *arfeed.PeerBindings
	stats, err := r.reader.Lookup(ctx, r.arPK, r.arPK, func(pack registry.Pack, root *treestore.TreeNode) error {
		segs, indices := arfeed.Segments(pk), arfeed.Indices(pk)
		node, blob := root, []byte(nil)
		for lvl, seg := range segs {
			leaf, sub, ok := cxoaggregate.ChildByIndex(pack, node, indices[lvl], seg)
			if !ok {
				// The level was not the dense shape we assumed — an older
				// publisher, or a Root whose objects we could not read. Binary
				// search still works on any sorted level, at O(log N) fetches
				// instead of one.
				leaf, sub, ok = cxoaggregate.ChildByNameSorted(pack, node, seg)
				if !ok {
					return ErrNoEntry
				}
			}
			if lvl == len(segs)-1 {
				if len(leaf) == 0 {
					return ErrNoEntry
				}
				blob = leaf
				break
			}
			if sub == nil {
				return ErrNoEntry
			}
			node = sub
		}
		peers, derr := arfeed.DecodeBucket(blob)
		if derr != nil {
			return fmt.Errorf("decode bucket %s: %w", arfeed.BucketPath(pk), derr)
		}
		got, found := peers[pk.Hex()]
		if !found || got.Empty() {
			return ErrNoEntry
		}
		rec = got
		return nil
	})
	r.mu.Lock()
	r.lastStats = stats
	r.mu.Unlock()
	if err != nil {
		return nil, stats, err
	}
	return rec, stats, nil
}

// Resolve returns one transport type's binding for pk, matching the shape of
// addrresolver.APIClient.Resolve so it can sit in front of it.
func (r *Resolver) Resolve(ctx context.Context, tType string, pk cipher.PubKey) (addrresolver.VisorData, error) {
	rec, _, err := r.Bindings(ctx, pk)
	if err != nil {
		return addrresolver.VisorData{}, err
	}
	vd := rec.Get(types.Type(tType))
	if vd == nil {
		return addrresolver.VisorData{}, ErrNoEntry
	}
	return *vd, nil
}

// LastStats reports the cost of the most recent lookup. Diagnostic only.
func (r *Resolver) LastStats() cxopreview.Stats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastStats
}

// LookupTimeout is the budget a dial-path caller should give a lookup. It
// matches dmsg's resolverTimeout: a resolver that is cold, wedged or simply
// slower than the fallback must lose the race, not delay it.
const LookupTimeout = 500 * time.Millisecond
