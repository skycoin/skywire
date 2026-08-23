// Package visor pkg/visor/dmsg_rpc_transport_oracle.go c3-vis-core
//
// A destination-transport oracle that sources a peer's OWN transports over the
// always-on visor RPC (skyenv.DmsgVisorRPCPort), plus a composite that layers
// the general RSN-signed oracle over it. Feeds the RSN-oracle 2-hop route
// computation (pkg/router/rsn_oracle_routes.go) so the adaptive mux can find
// disjoint intermediates WITHOUT depending on TPD's (chronically under-filled)
// view of a busy exit's transports.
package visor

import (
	"context"
	"errors"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport"
)

// dmsgRPCTransportOracle sources a destination visor's OWN transports over the
// always-on visor net/rpc server on skyenv.DmsgVisorRPCPort — the exact channel
// `skywire cli tp ls --via dmsg://<pk>` uses. Its Transports() reply is the
// destination's authoritative transport list, which is far more complete than
// TPD's CXO view for a high-degree exit.
//
// IMPORTANT — this is NOT a general any-source→any-dest mechanism, and must
// never be the sole source. That RPC port is whitelist-gated to the
// destination's Hypervisors + Pty.Whitelist (pkg/visor/init_apps.go), so it only
// yields data for destinations that already trust THIS visor (an exit this
// visor's hypervisor manages, or a pty-whitelisted peer). It is wired as a BONUS
// fallback behind the general RSN-signed oracle (which any visor can use once
// the destination runs the always-on transport-query listener). "Use the
// hypervisor connection if present, but the general path is what a normal visor
// sources."
type dmsgRPCTransportOracle struct {
	dmsgC *dmsg.Client
	log   *logging.Logger
}

// newDmsgRPCTransportOracle returns the oracle as a router.DstTransportOracle,
// or a real nil interface when there is no dmsg client (so the composite's
// nil-check works — no typed-nil trap).
func newDmsgRPCTransportOracle(dmsgC *dmsg.Client, log *logging.Logger) router.DstTransportOracle {
	if dmsgC == nil {
		return nil
	}
	return &dmsgRPCTransportOracle{dmsgC: dmsgC, log: log}
}

func (o *dmsgRPCTransportOracle) DstTransports(ctx context.Context, _, dst cipher.PubKey) ([]*transport.Entry, error) {
	conn, err := o.dmsgC.DialStream(ctx, dmsg.Addr{PK: dst, Port: skyenv.DmsgVisorRPCPort})
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }() //nolint:errcheck
	// Bound the RPC round-trip to the oracle's deadline (net/rpc ignores ctx).
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl) //nolint:errcheck
	}

	api := NewRPCClient(o.log, conn, RPCPrefix, 0)
	sums, err := api.Transports(nil, nil, false)
	if err != nil {
		return nil, err
	}

	out := make([]*transport.Entry, 0, len(sums))
	for _, s := range sums {
		if s == nil {
			continue
		}
		// s.Local == dst (the destination reporting its own transports), s.Remote
		// is the peer. MakeEntry sorts the edges canonically so RemoteEdge(dst)
		// yields the intermediate peer — exactly what computeDisjoint2HopRoutes
		// intersects against the source's own transports. LabelSetup / DMSG
		// entries are filtered downstream in computeDisjoint2HopRoutes.
		e := transport.MakeEntry(dst, s.Remote, s.Type, s.Label)
		out = append(out, &e)
	}
	if len(out) == 0 {
		return nil, errors.New("dmsg-rpc oracle: destination reported no transports")
	}
	return out, nil
}

// compositeDstOracle tries each member in order and returns the first
// non-empty, non-error result. It layers the general RSN-signed oracle
// (primary — any visor can use it once the destination runs the always-on
// transport-query listener) over the visor-RPC oracle (bonus — only for
// destinations this visor is whitelisted on). Nil members are skipped.
type compositeDstOracle struct {
	oracles []router.DstTransportOracle
}

func newCompositeDstOracle(oracles ...router.DstTransportOracle) router.DstTransportOracle {
	live := make([]router.DstTransportOracle, 0, len(oracles))
	for _, o := range oracles {
		if o != nil {
			live = append(live, o)
		}
	}
	if len(live) == 0 {
		return nil
	}
	if len(live) == 1 {
		return live[0]
	}
	return &compositeDstOracle{oracles: live}
}

// perSourceTimeout bounds EACH member's attempt so a slow/failing source (e.g.
// the RSN listener on a destination that doesn't run it, or a cold dmsg session)
// cannot starve the next source of budget. Without this, a shared deadline let
// the first attempt burn all of it and the second (the one that actually works)
// never ran.
const perSourceTimeout = 6 * time.Second

func (c *compositeDstOracle) DstTransports(ctx context.Context, src, dst cipher.PubKey) ([]*transport.Entry, error) {
	var lastErr error
	for _, o := range c.oracles {
		mctx, cancel := context.WithTimeout(ctx, perSourceTimeout)
		entries, err := o.DstTransports(mctx, src, dst)
		cancel()
		if err == nil && len(entries) > 0 {
			return entries, nil
		}
		if err != nil {
			lastErr = err
		}
		if ctx.Err() != nil { // overall budget exhausted
			break
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("composite oracle: no transports from any source")
}
