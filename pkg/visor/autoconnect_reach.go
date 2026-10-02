// Package visor pkg/visor/autoconnect_reach.go c3-vis-core
//
// Autoconnect picks its peers from the address resolver's reach feed: per
// peer and per transport type, whether a transport is known to work, should
// work, is untested, or is known to fail (arfeed.Reach.Verdict). Known
// failures are never dialed; confirmed peers are dialed first; untested
// ones — visors too old to state anything, bindings not yet probed — get a
// few dials per cycle so they are not starved, but no more.
package visor

import (
	"context"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/deployment/ar/arfeed"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
	"github.com/skycoin/skywire/pkg/visor/visorcore"
)

// reachExplorePerCycle is how many untested peers a phase dials per cycle.
const reachExplorePerCycle = 3

// reachName maps a dialed transport type to the reach type that decides it.
// WS rides the peer's stcpr port, so its reachability is stcpr's.
func reachName(t tptypes.Type) (string, bool) {
	switch tptypes.NormalizeType(t) {
	case tptypes.STCPR, tptypes.WS:
		return arfeed.TypeSTCPR, true
	case tptypes.SUDPH:
		return arfeed.TypeSUDPH, true
	case tptypes.QUIC:
		return arfeed.TypeQUIC, true
	case tptypes.WT:
		return arfeed.TypeWT, true
	case tptypes.WEBRTC:
		return arfeed.TypeWEBRTC, true
	}
	return "", false
}

// loadReach reads the reach feed. nil when it has not synced: every verdict
// is then Untested, and the phases fall back to their exploration budget.
func (a *autoconnector) loadReach(ctx context.Context, v *Visor) map[cipher.PubKey]*arfeed.Reach {
	mgr := v.CXOSubMgr()
	if mgr == nil {
		return nil
	}
	mgr.AcquireFor(TabAutoconnect)
	defer mgr.ReleaseFor(TabAutoconnect)
	mgr.WaitForFirstSync(ctx, FeedARReach, feedFirstSyncTimeout(FeedARReach))
	var out map[cipher.PubKey]*arfeed.Reach
	mgr.Walk(FeedARReach, arfeed.ReachPathPrefix, func(path string, body []byte) bool {
		peers, err := arfeed.DecodeReachBucket(body)
		if err != nil {
			a.log.WithError(err).WithField("path", path).Debug("Autoconnect: reach bucket did not decode")
			return true
		}
		if out == nil {
			out = make(map[cipher.PubKey]*arfeed.Reach, len(peers)*arfeed.ReachBuckets)
		}
		for pk, r := range peers {
			out[pk] = r
		}
		return true
	})
	return out
}

// selfNAT is this visor's own NAT class, "" when STUN has not classified it.
func (v *Visor) selfNAT() string {
	v.initLock.Lock()
	defer v.initLock.Unlock()
	if v.stun.client == nil {
		return ""
	}
	return natClass(v.stun.client.NATType)
}

// reachPeers returns the peers the feed says serve type t: bound for it at
// the AR (sudph), or declaring they accept it (webrtc).
func reachPeers(reach map[cipher.PubKey]*arfeed.Reach, t string) map[cipher.PubKey]struct{} {
	out := make(map[cipher.PubKey]struct{})
	for pk, r := range reach {
		list := r.Bound
		if t == arfeed.TypeWEBRTC {
			list = r.Accepts
		}
		for _, x := range list {
			if x == t {
				out[pk] = struct{}{}
				break
			}
		}
	}
	return out
}

// reachTiers splits targets by verdict for dialing type t. Unreachable
// targets are returned separately so a tracking phase can still hand them on.
func reachTiers(reach map[cipher.PubKey]*arfeed.Reach, targets []cipher.PubKey, t tptypes.Type, selfNAT string,
	now time.Time) (confirmed, capable, untested, unreachable []cipher.PubKey) {
	name, ok := reachName(t)
	for _, pk := range targets {
		if !ok {
			untested = append(untested, pk)
			continue
		}
		switch reach[pk].Verdict(name, selfNAT, now) {
		case arfeed.Confirmed:
			confirmed = append(confirmed, pk)
		case arfeed.Capable:
			capable = append(capable, pk)
		case arfeed.Untested:
			untested = append(untested, pk)
		default:
			unreachable = append(unreachable, pk)
		}
	}
	return confirmed, capable, untested, unreachable
}

// connectByReach dials targets in verdict order: confirmed, then capable,
// then at most explore untested ones (explore <= 0: no separate cap). maxCount,
// currentCount and trackAll mean what they do to visorcore.ConnectToVisors;
// with trackAll the unreachable targets are reported as handled too, so the
// later phases still see them.
func (a *autoconnector) connectByReach(ctx context.Context, self cipher.PubKey, targets []cipher.PubKey, t tptypes.Type,
	existingByPK map[cipher.PubKey]map[tptypes.Type]bool, reach map[cipher.PubKey]*arfeed.Reach, selfNAT string,
	maxCount, currentCount, explore int, trackAll bool) (visorcore.ConnectPhaseResult, error) {
	confirmed, capable, untested, unreachable := reachTiers(reach, targets, t, selfNAT, time.Now())

	var res visorcore.ConnectPhaseResult
	if trackAll {
		res.Connected = append(res.Connected, unreachable...)
	}
	spent := func() int { return currentCount + res.Count }
	full := func() bool { return maxCount > 0 && spent() >= maxCount }

	for i, tier := range [][]cipher.PubKey{confirmed, capable, untested} {
		if len(tier) == 0 || full() {
			continue
		}
		budget := maxCount
		if i == 2 && explore > 0 {
			if limit := spent() + explore; budget <= 0 || limit < budget {
				budget = limit
			}
		}
		r, err := a.conn.ConnectToVisors(ctx, self, tier, t, existingByPK, nil, budget, spent(), trackAll)
		res.Count += r.Count
		res.Connected = append(res.Connected, r.Connected...)
		if err != nil {
			return res, err
		}
	}
	if len(unreachable) > 0 {
		a.log.WithField("type", string(t)).WithField("skipped", len(unreachable)).
			WithField("confirmed", len(confirmed)).WithField("capable", len(capable)).
			WithField("untested", len(untested)).Debug("Autoconnect: skipping peers the reach feed says cannot be reached")
	}
	return res, nil
}
