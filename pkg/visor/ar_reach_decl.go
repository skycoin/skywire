// Package visor pkg/visor/ar_reach_decl.go c3-vis-core
//
// The visor's reachability declaration: the "reach" leaf of its AR-bind feed,
// which the address resolver folds into its reach feed (see
// pkg/deployment/ar/arfeed/reach.go). It states what the visor alone can
// know: its STUN-measured NAT class, which types it serves inbound, and when
// it last accepted an inbound transport of each.
//
// It is rebuilt on a timer and Put only when its bytes change. Inbound times
// are rounded to an hour, so a visor accepting transports all day rewrites
// the leaf at most hourly; between changes the feed heartbeat carries it
// unchanged.
package visor

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/ccding/go-stun/stun"

	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/deployment/ar/arfeed"
	"github.com/skycoin/skywire/pkg/logging"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

// reachDeclInterval is how often the declaration is rebuilt.
const reachDeclInterval = time.Minute

// reachLeaf is the AR-bind feed leaf the declaration is published under.
const reachLeaf = "reach"

// reachTypes maps the transport types a reach record speaks of to their names.
var reachTypes = map[tptypes.Type]string{
	tptypes.STCPR:  arfeed.TypeSTCPR,
	tptypes.SUDPH:  arfeed.TypeSUDPH,
	tptypes.QUIC:   arfeed.TypeQUIC,
	tptypes.WT:     arfeed.TypeWT,
	tptypes.WEBRTC: arfeed.TypeWEBRTC,
}

// natClass maps a STUN verdict to a reach NAT class. A failed or inconclusive
// probe is "" (unknown), not blocked: go-stun reports NATBlocked when the STUN
// servers are merely unreachable, and calling that "no UDP" would exclude the
// visor from every punch on the strength of one bad server.
func natClass(n stun.NATType) string {
	switch n {
	case stun.NATNone:
		return arfeed.NATOpen
	case stun.NATFull:
		return arfeed.NATFullCone
	case stun.NATRestricted:
		return arfeed.NATRestricted
	case stun.NATPortRestricted:
		return arfeed.NATPortRestricted
	case stun.NATSymmetric:
		return arfeed.NATSymmetric
	case stun.NATSymmetricUDPFirewall:
		return arfeed.NATUDPFirewall
	}
	return ""
}

// reachDecl builds the visor's current declaration.
func (v *Visor) reachDecl() arfeed.ReachDecl {
	var d arfeed.ReachDecl
	v.initLock.Lock()
	sc := v.stun.client
	tm := v.tpM
	v.initLock.Unlock()
	if sc != nil {
		d.NAT = natClass(sc.NATType)
	}
	if tm == nil {
		return d
	}
	for _, t := range tm.Networks() {
		if name, ok := reachTypes[tptypes.NormalizeType(t)]; ok {
			d.Accepts = append(d.Accepts, name)
		}
	}
	sort.Strings(d.Accepts)
	g := int64(arfeed.InboundGranularity / time.Second)
	for t, at := range tm.LastInbound() {
		name, ok := reachTypes[t]
		if !ok {
			continue
		}
		if d.Inbound == nil {
			d.Inbound = make(map[string]int64)
		}
		u := at.Unix()
		d.Inbound[name] = u - u%g
	}
	return d
}

// runReachDeclLoop keeps the reach leaf current until ctx ends.
func runReachDeclLoop(ctx context.Context, v *Visor, pub *treestore.Publisher, log *logging.Logger) {
	var last []byte
	put := func() {
		b, err := json.Marshal(v.reachDecl())
		if err != nil || bytes.Equal(b, last) {
			return
		}
		if err := pub.Put(reachLeaf, b); err != nil {
			log.WithError(err).Debug("AR-bind-CXO: reach Put failed")
			return
		}
		last = b
	}
	put()
	t := time.NewTicker(reachDeclInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			put()
		}
	}
}
