//go:build !tinygo || (js && wasm)

// Package router pkg/router/rsn_oracle_routes.go c2-net-routing
//
// RSN-oracle 2-hop route computation (phase-1, opt-in).
//
// Given the source's OWN transports and the destination's OWN transports
// (fetched authoritatively from the destination via the RSN-signed
// transport-query, transport_query.go), the set of single-intermediate (2-hop)
// routes S->I->D is the overlap of the two peer sets: an intermediate I is
// usable iff S has a live transport S-I AND D has a transport I-D. No TPD
// snapshot is consulted — the common route type becomes independent of the
// transport-discovery service's global view.
//
// This is the S-side crux and is deliberately a pure function of its inputs
// (computeDisjoint2HopRoutes) so it is unit-testable with mock transport sets.
// The result is a disjoint set of legs (one per distinct intermediate PK) ready
// to feed the existing mux establishment (establishMuxRoutes consumes
// ForwardHops/ReverseHops per leg).
package router

import (
	"context"
	"errors"
	"sort"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	rpc "github.com/skycoin/skywire/pkg/gobrpc"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

// errRSNOracleInert signals that the RSN-oracle 2-hop path is enabled by config
// but no oracle is wired (SetDstTransportOracle was never called), so
// fetchBestRoutes must fall through to its existing behavior.
var errRSNOracleInert = errors.New("rsn-oracle: no destination-transport oracle configured")

// oracleLocalTp is the minimal view of one of the source's own transports the
// 2-hop computation needs: which peer it reaches and over which transport ID /
// type. Callers build it from transport.Manager.WalkTransports.
type oracleLocalTp struct {
	id       uuid.UUID
	remotePK cipher.PubKey
	tpType   tptypes.Type
}

// twoHopLeg is one disjoint S->I->D route: the intermediate plus the forward
// and reverse hop lists ready for DialOptions.ForwardHops / ReverseHops.
type twoHopLeg struct {
	Intermediate cipher.PubKey
	Forward      []routing.Hop // src -> I -> dst
	Reverse      []routing.Hop // dst -> I -> src
}

// computeDisjoint2HopRoutes computes the disjoint set of single-intermediate
// routes from src to dst, using ONLY the source's own transports (localTps) and
// the destination's own transports (dstEntries, as returned by the destination
// and reconstructed with transport.EntryFromCompact). It never consults TPD.
//
// An intermediate I qualifies iff:
//   - src has a live non-DMSG transport src-I (from localTps), and
//   - dst has a non-DMSG transport I-dst (from dstEntries), and
//   - I is neither src nor dst, and
//   - I is not in opts.ExcludeIntermediatePKs, and
//   - the src-I transport ID is not in opts.ExcludeTransportIDs.
//
// DMSG is excluded on both legs: a DMSG transport relays through an unobservable
// dmsg server, so it must never be an intermediate hop (same rule as the local
// BFS in calculateLocalRoutes). When src and dst each have several transports to
// the same intermediate, the lowest TypePreference (most-preferred, e.g. stcpr
// over webrtc) is chosen on each side.
//
// Legs are returned sorted by intermediate-PK string for determinism and capped
// at max (0 = uncapped). Each leg has a distinct intermediate, so the returned
// set is disjoint in the intermediate-PK sense — exactly what the mux splitter
// wants for parallel legs.
func computeDisjoint2HopRoutes(
	src, dst cipher.PubKey,
	localTps []oracleLocalTp,
	dstEntries []*transport.Entry,
	opts *DialOptions,
	max int,
) ([]twoHopLeg, error) {
	if src == dst {
		return nil, errors.New("rsn-oracle 2-hop: src == dst")
	}

	excludeIntermediates := map[cipher.PubKey]struct{}{}
	excludeTpIDs := map[uuid.UUID]struct{}{}
	if opts != nil {
		for _, pk := range opts.ExcludeIntermediatePKs {
			excludeIntermediates[pk] = struct{}{}
		}
		for _, id := range opts.ExcludeTransportIDs {
			excludeTpIDs[id] = struct{}{}
		}
	}

	// Source side: best src-I transport per intermediate peer I (lowest
	// TypePreference wins). DMSG and excluded transport IDs are dropped.
	type srcLeg struct {
		id     uuid.UUID
		tpType tptypes.Type
	}
	srcByPeer := map[cipher.PubKey]srcLeg{}
	for _, tp := range localTps {
		if tp.tpType == tptypes.DMSG {
			continue
		}
		if _, hit := excludeTpIDs[tp.id]; hit {
			continue
		}
		peer := tp.remotePK
		if peer == src || peer == dst {
			continue
		}
		if _, hit := excludeIntermediates[peer]; hit {
			continue
		}
		if cur, ok := srcByPeer[peer]; !ok ||
			tptypes.TypePreference(tp.tpType) < tptypes.TypePreference(cur.tpType) {
			srcByPeer[peer] = srcLeg{id: tp.id, tpType: tp.tpType}
		}
	}

	// Destination side: best I-dst transport per intermediate peer I. The
	// reconstructed Entry carries the canonical (deterministic) transport ID the
	// intermediate would use to reach dst.
	type dstLeg struct {
		id     uuid.UUID
		tpType tptypes.Type
	}
	dstByPeer := map[cipher.PubKey]dstLeg{}
	for _, e := range dstEntries {
		if e == nil {
			continue
		}
		if e.Type == tptypes.DMSG {
			continue
		}
		if e.Label == transport.LabelSetup {
			continue
		}
		peer := e.RemoteEdge(dst)
		if peer == src || peer == dst {
			continue
		}
		if cur, ok := dstByPeer[peer]; !ok ||
			tptypes.TypePreference(e.Type) < tptypes.TypePreference(cur.tpType) {
			dstByPeer[peer] = dstLeg{id: e.ID, tpType: e.Type}
		}
	}

	// Intersection: intermediates reachable from BOTH ends.
	intermediates := make([]cipher.PubKey, 0, len(srcByPeer))
	for peer := range srcByPeer {
		if _, ok := dstByPeer[peer]; ok {
			intermediates = append(intermediates, peer)
		}
	}
	if len(intermediates) == 0 {
		return nil, errors.New("rsn-oracle 2-hop: no shared intermediate between src and dst transports")
	}
	sort.SliceStable(intermediates, func(i, j int) bool {
		return intermediates[i].String() < intermediates[j].String()
	})

	legs := make([]twoHopLeg, 0, len(intermediates))
	for _, i := range intermediates {
		s := srcByPeer[i]
		d := dstByPeer[i]
		fwd := []routing.Hop{
			{TpID: s.id, From: src, To: i},
			{TpID: d.id, From: i, To: dst},
		}
		legs = append(legs, twoHopLeg{
			Intermediate: i,
			Forward:      fwd,
			Reverse:      reverseHops(fwd),
		})
		if max > 0 && len(legs) >= max {
			break
		}
	}
	return legs, nil
}

// reconstructDstEntries rebuilds full transport entries from a destination's
// compact query response. The reporter is the destination itself (resp.TargetPK
// / dst), so EntryFromCompact yields the canonical Entry (sorted edges,
// recomputed ID) both endpoints agree on.
func reconstructDstEntries(dst cipher.PubKey, resp *TransportQueryResponse) []*transport.Entry {
	if resp == nil {
		return nil
	}
	out := make([]*transport.Entry, 0, len(resp.Entries))
	for _, ce := range resp.Entries {
		out = append(out, transport.EntryFromCompact(dst, ce))
	}
	return out
}

// oracleDeliveryOracle is the reference DstTransportOracle: it asks the RSN
// (over rsnRPC) to sign a transport-query and delivers it to the destination via
// deliverer. Constructed by the visor once it has both the RSN RPC client and a
// delivery transport wired.
type oracleDeliveryOracle struct {
	rsnRPC    *rpc.Client
	deliverer TransportQueryDeliverer
}

// NewDstTransportOracle builds the reference DstTransportOracle from an RSN RPC
// client and a query deliverer.
func NewDstTransportOracle(rsnRPC *rpc.Client, deliverer TransportQueryDeliverer) DstTransportOracle {
	return &oracleDeliveryOracle{rsnRPC: rsnRPC, deliverer: deliverer}
}

func (o *oracleDeliveryOracle) DstTransports(ctx context.Context, src, dst cipher.PubKey) ([]*transport.Entry, error) {
	return fetchDstTransportsViaOracle(ctx, o.rsnRPC, o.deliverer, src, dst)
}

// oracle2HopRoutes is the router-side seam wired into fetchBestRoutes. When the
// RSN-oracle path is enabled and an oracle is configured, it fetches the
// destination's own transports, snapshots the source's own transports, computes
// the disjoint 2-hop legs locally (honoring opts.ExcludeIntermediatePKs /
// ExcludeTransportIDs so successive mux-leg calls advance to fresh
// intermediates), and returns the FIRST leg's forward/reverse hops. Returns
// errRSNOracleInert when no oracle is configured so the caller falls through to
// existing behavior.
func (r *router) oracle2HopRoutes(ctx context.Context, log *logging.Logger, src, dst cipher.PubKey, opts *DialOptions) (fwd, rev []routing.Hop, err error) {
	if log == nil {
		log = r.logger
	}
	oracle := r.dstTransportOracle()
	if oracle == nil {
		return nil, nil, errRSNOracleInert
	}
	if r.tm == nil {
		return nil, nil, errors.New("rsn-oracle: transport manager not available")
	}

	dstEntries, err := oracle.DstTransports(ctx, src, dst)
	if err != nil {
		return nil, nil, err
	}

	var localTps []oracleLocalTp
	r.tm.WalkTransports(func(tp *transport.ManagedTransport) bool {
		if tp == nil || tp.IsClosed() {
			return true
		}
		if tp.Entry.Label == transport.LabelSetup {
			return true
		}
		localTps = append(localTps, oracleLocalTp{
			id:       tp.Entry.ID,
			remotePK: tp.Entry.RemoteEdge(src),
			tpType:   tp.Entry.Type,
		})
		return true
	})

	legs, err := computeDisjoint2HopRoutes(src, dst, localTps, dstEntries, opts, 1)
	if err != nil {
		return nil, nil, err
	}
	leg := legs[0]
	log.Infof("RSN-oracle 2-hop route via intermediate %s (no TPD)", leg.Intermediate)
	return leg.Forward, leg.Reverse, nil
}

// TransportQueryDeliverer carries a signed transport-query to the destination
// visor and returns its response. PHASE-1 delivery is dmsg-direct to D (the
// mechanism `tp --remote` already uses to reach a remote visor); PHASE-2 is
// carrying the query over transports via intermediates like the cascade itself.
// The interface keeps computeDisjoint2HopRoutes' caller (fetchDstTransportsViaOracle)
// independent of the transport used, so the delivery seam can be filled in
// without touching the route computation.
type TransportQueryDeliverer interface {
	DeliverTransportQuery(ctx context.Context, dst cipher.PubKey, q *TransportQuery) (*TransportQueryResponse, error)
}

// fetchDstTransportsViaOracle performs the source-side flow: ask the RSN (over
// the existing setup RPC connection) to SIGN a transport-query targeting dst,
// then deliver it to dst and reconstruct dst's transport entries from the
// response. It never consults TPD.
//
// rsnRPC is the RPC client to the RSN (SetupClient.RPCClient). deliverer carries
// the signed query to dst (see TransportQueryDeliverer). Returns the destination's
// own transport entries.
func fetchDstTransportsViaOracle(
	ctx context.Context,
	rsnRPC *rpc.Client,
	deliverer TransportQueryDeliverer,
	src, dst cipher.PubKey,
) ([]*transport.Entry, error) {
	if rsnRPC == nil {
		return nil, errors.New("rsn-oracle: no RSN RPC client")
	}
	if deliverer == nil {
		return nil, errors.New("rsn-oracle: no query deliverer")
	}

	var signReply SignTransportQueryReply
	if err := rpcCall(ctx, rsnRPC, rpcName+".SignTransportQuery",
		&SignTransportQueryArgs{RequesterPK: src, TargetPK: dst}, &signReply); err != nil {
		return nil, err
	}
	q := signReply.Query
	if q == nil {
		return nil, errors.New("rsn-oracle: RSN returned nil query")
	}

	resp, err := deliverer.DeliverTransportQuery(ctx, dst, q)
	if err != nil {
		return nil, err
	}
	return reconstructDstEntries(dst, resp), nil
}
