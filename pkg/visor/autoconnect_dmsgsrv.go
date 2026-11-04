// Package visor pkg/visor/autoconnect_dmsgsrv.go c3-vis-core
//
// A visor keeps one automatic transport to each dmsg server it holds a
// session with, when that server is also a visor in the address resolver.
// This runs whether public autoconnect is on or not, so a visor with it off
// (the Android app) still has as many skynet transports as dmsg sessions.
package visor

import (
	"context"
	"runtime"
	"slices"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/deployment/ar/arfeed"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
	"github.com/skycoin/skywire/pkg/visor/visorcore"
)

// initialDmsgServerTransportDelay leaves time for the dmsg sessions to form.
const initialDmsgServerTransportDelay = 20 * time.Second

// dmsgServerTransportTypes is the dial preference for a dmsg server transport.
var dmsgServerTransportTypes = []tptypes.Type{tptypes.STCPR, tptypes.QUIC, tptypes.SUDPH}

// allTransportLabels lists every label, for "any transport to this peer".
var allTransportLabels = []transport.Label{transport.LabelUser, transport.LabelAutomatic,
	transport.LabelSkycoin, transport.LabelSetup, transport.LabelSysFeed}

// dmsgServerDial is one dmsg server to dial and the types to try, in order.
type dmsgServerDial struct {
	pk    cipher.PubKey
	types []tptypes.Type
}

// planDmsgServerDials picks the dmsg servers that have no transport yet and
// orders the locally dialable types for each by reach verdict, then
// preference. A nil reach (feed not synced) leaves every server untested; a
// synced feed without the server means it is not a visor, so it is skipped.
func planDmsgServerDials(self cipher.PubKey, servers []cipher.PubKey, reached map[cipher.PubKey]bool,
	reach map[cipher.PubKey]*arfeed.Reach, local []tptypes.Type, selfNAT string, now time.Time) []dmsgServerDial {
	var out []dmsgServerDial
	seen := make(map[cipher.PubKey]bool, len(servers))
	for _, pk := range servers {
		if pk == self || reached[pk] || seen[pk] {
			continue
		}
		seen[pk] = true
		r := reach[pk]
		if reach != nil && r == nil {
			continue
		}
		var confirmed, capable, untested []tptypes.Type
		for _, t := range local {
			v := arfeed.Untested
			if name, ok := reachName(t); ok {
				v = r.Verdict(name, selfNAT, now)
			}
			switch v {
			case arfeed.Confirmed:
				confirmed = append(confirmed, t)
			case arfeed.Capable:
				capable = append(capable, t)
			case arfeed.Untested:
				untested = append(untested, t)
			}
		}
		if types := slices.Concat(confirmed, capable, untested); len(types) > 0 {
			out = append(out, dmsgServerDial{pk: pk, types: types})
		}
	}
	return out
}

// skynetReached is the peers this visor has a non-dmsg transport to.
func skynetReached(tps []*transport.ManagedTransport) map[cipher.PubKey]bool {
	out := make(map[cipher.PubKey]bool, len(tps))
	for _, tp := range tps {
		if tp.Type() != tptypes.DMSG {
			out[tp.Remote()] = true
		}
	}
	return out
}

// dmsgServerPKs is the servers of the visor's dmsg sessions.
func (v *Visor) dmsgServerPKs() []cipher.PubKey {
	if v.dmsgC == nil {
		return nil
	}
	sessions := v.dmsgC.AllSessions()
	out := make([]cipher.PubKey, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, s.RemotePK())
	}
	return out
}

func initDmsgServerTransports(ctx context.Context, v *Visor, log *logging.Logger) {
	// A browser has no raw sockets, so it cannot dial these types.
	if runtime.GOOS == "js" || !v.conf.DmsgServerTransportsOn() || v.tpM == nil || v.dmsgC == nil {
		return
	}
	a := &autoconnector{log: log, tm: v.tpM, dmsgC: v.dmsgC,
		conn: &visorcore.Connector{Tm: v.tpM, DmsgC: v.dmsgC, Log: log}}
	go a.runDmsgServerTransports(ctx, v)
}

func (a *autoconnector) runDmsgServerTransports(ctx context.Context, v *Visor) {
	timer := time.NewTimer(initialDmsgServerTransportDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			timer.Reset(PublicServiceDelay)
			if v.pressure.busy.Load() {
				continue
			}
			a.dialDmsgServers(ctx, v)
		}
	}
}

// dialDmsgServers runs one cycle: one transport to each dmsg server that has none.
func (a *autoconnector) dialDmsgServers(ctx context.Context, v *Visor) {
	servers := v.dmsgServerPKs()
	if len(servers) == 0 {
		return
	}
	var local []tptypes.Type
	for _, t := range dmsgServerTransportTypes {
		if a.tm.IsKnownNetwork(t) && a.tm.CanCreateTransport(t) {
			local = append(local, t)
		}
	}
	if len(local) == 0 {
		return
	}
	reached := skynetReached(a.tm.GetTransportsByLabels(allTransportLabels...))
	if len(planDmsgServerDials(v.conf.PK, servers, reached, nil, local, "", time.Now())) == 0 {
		return
	}
	plan := planDmsgServerDials(v.conf.PK, servers, reached, a.loadReach(ctx, v), local, v.selfNAT(), time.Now())

	var wg sync.WaitGroup
	for _, d := range plan {
		wg.Add(1)
		go func(d dmsgServerDial) {
			defer wg.Done()
			for _, t := range d.types {
				// Public autoconnect may have reached it meanwhile.
				if skynetReached(a.tm.GetTransportsByLabels(allTransportLabels...))[d.pk] {
					return
				}
				err := a.conn.Dial(ctx, d.pk, t)
				if err == nil {
					a.log.WithField("pk", d.pk).WithField("type", string(t)).Info("Added transport to dmsg server")
					return
				}
				a.log.WithError(err).WithField("pk", d.pk).WithField("type", string(t)).Debug("Transport to dmsg server failed")
				if ctx.Err() != nil {
					return
				}
			}
		}(d)
	}
	wg.Wait()
}

// dmsgServerTransportCount is how many of the visor's dmsg servers it also
// reaches over a non-dmsg transport, and how many servers there are.
func (v *Visor) dmsgServerTransportCount() (reached, servers int) {
	if v.tpM == nil {
		return 0, 0
	}
	have := skynetReached(v.tpM.GetTransportsByLabels(allTransportLabels...))
	seen := map[cipher.PubKey]bool{}
	for _, pk := range v.dmsgServerPKs() {
		if seen[pk] {
			continue
		}
		seen[pk] = true
		servers++
		if have[pk] {
			reached++
		}
	}
	return reached, servers
}
