// Package tpviz pkg/tpviz/cxo_standalone.go c4-app-rewards
//
// Standalone CXO wiring. When tp-viz runs OUTSIDE a visor (the reward
// server serving theskywirenetwork.net over dmsg), it has no visor to
// hand it a CXOSubMgr. SetCXOSubMgrFromDmsg builds an intermittent CXO
// subscriber (pkg/cxo/cxosub) over the server's own dmsg client and
// wires it in — so the standalone server sources TPD/SD/DMSG-D data from
// the same CXO feeds the hypervisor uses, instead of HTTP-over-dmsg
// polling. Feed publisher PKs are resolved from the config's dmsg://
// service URLs (TPDURLDmsg / SDURLDmsg / DMSGURLDmsg).
package tpviz

import (
	"fmt"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxosub"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgcurl"
	"github.com/skycoin/skywire/pkg/logging"
)

// SetCXOSubMgrFromDmsg wires an intermittent CXO subscriber over dmsgC so a
// STANDALONE tp-viz sources deployment data from the TPD/SD/DMSG-D CXO feeds.
// No-op when dmsgC is nil. Keep any HTTP-over-dmsg client set via
// SetDmsgHTTPClient as the fallback: tp-viz tries CXO first and falls through
// on a cache miss (feed not yet synced), so the two compose safely.
func (s *Server) SetCXOSubMgrFromDmsg(dmsgC *dmsg.Client) {
	if dmsgC == nil {
		return
	}
	tpd := parseCXOPeer(s.config.TPDURLDmsg)
	sd := parseCXOPeer(s.config.SDURLDmsg)
	dmsgd := parseCXOPeer(s.config.DMSGURLDmsg)

	feedSpec := func(f cxosub.Feed) (cipher.PubKey, uint16, string, error) {
		port, prefix, ok := cxosub.FeedRoute(f)
		if !ok {
			return cipher.PubKey{}, 0, "", fmt.Errorf("unknown feed %d", f)
		}
		var pk cipher.PubKey
		switch f {
		case cxosub.FeedTPDMetrics, cxosub.FeedTPDUptime, cxosub.FeedTPDAllTransports:
			pk = tpd
		case cxosub.FeedSDServices:
			pk = sd
		case cxosub.FeedDMSGDClientsByServer:
			pk = dmsgd
		}
		if (pk == cipher.PubKey{}) {
			return cipher.PubKey{}, 0, "", fmt.Errorf("no publisher PK for feed %d (dmsg service URL unset)", f)
		}
		return pk, port, prefix, nil
	}

	mgr := cxosub.NewManager(cxosub.Deps{
		Dmsg:     func() *dmsg.Client { return dmsgC },
		FeedSpec: feedSpec,
		Log:      logging.MustGetLogger("tpviz-cxosub"),
	}, 0) // 0 → default cycle interval
	s.SetCXOSubMgr(&standaloneCXOAdapter{m: mgr})
}

// standaloneCXOAdapter bridges cxosub.Manager (typed Feed/Tab) to tp-viz's
// int-based CXOSubMgr interface. Values match by construction.
type standaloneCXOAdapter struct{ m *cxosub.Manager }

func (a *standaloneCXOAdapter) AcquireForTab(tab int) { a.m.AcquireFor(cxosub.Tab(tab)) }
func (a *standaloneCXOAdapter) ReleaseForTab(tab int) { a.m.ReleaseFor(cxosub.Tab(tab)) }
func (a *standaloneCXOAdapter) Walk(feed int, prefix string, fn func(path string, body []byte) bool) bool {
	return a.m.Walk(cxosub.Feed(feed), prefix, fn)
}

// parseCXOPeer extracts the publisher PK from a dmsg://<pk>:<port> service URL,
// or the zero PubKey when raw is empty / not a dmsg URL (callers treat the zero
// value as "unset" and skip that feed).
func parseCXOPeer(raw string) cipher.PubKey {
	if raw == "" {
		return cipher.PubKey{}
	}
	var u dmsgcurl.URL
	if err := u.Fill(raw); err != nil || u.Scheme != "dmsg" {
		return cipher.PubKey{}
	}
	return u.Addr.PK
}

// tryCXOUptimes returns the []VisorSummary JSON from the TPD uptime feed's
// local snapshot (any day-window leaf carries the current online status), or
// ok=false when the manager isn't wired / the feed hasn't synced yet — in which
// case the caller falls through to its HTTP fetch. The body is byte-identical to
// what GET /uptimes?v=v2 serves, so it's forwarded to the UI unchanged.
func (s *Server) tryCXOUptimes() ([]byte, bool) {
	mgr := s.cxoMgr()
	if mgr == nil {
		return nil, false
	}
	mgr.AcquireForTab(CXOTabUptime)
	defer mgr.ReleaseForTab(CXOTabUptime)

	var out []byte
	mgr.Walk(CXOFeedTPDUptime, "uptimes/days/", func(_ string, body []byte) bool {
		if len(body) > 0 && string(body) != "null" {
			out = append([]byte(nil), body...)
			return false // first non-empty leaf is enough
		}
		return true
	})
	if out == nil {
		return nil, false
	}
	return out, true
}

// tryCXOTransports returns the all-transports JSON snapshot from the TPD
// all-transports feed's local snapshot (the network-wide "without-self" leaf),
// or ok=false on cache miss. The leaf body is the same JSON GET /all-transports
// serves, so it forwards unchanged.
func (s *Server) tryCXOTransports() ([]byte, bool) {
	mgr := s.cxoMgr()
	if mgr == nil {
		return nil, false
	}
	mgr.AcquireForTab(CXOTabCLITransports)
	defer mgr.ReleaseForTab(CXOTabCLITransports)

	leaves := make(map[string][]byte, 2)
	mgr.Walk(CXOFeedTPDAllTransports, "transports/all/", func(path string, body []byte) bool {
		if len(body) > 0 {
			leaves[path] = append([]byte(nil), body...)
		}
		return true
	})
	if b, ok := leaves["transports/all/without-self"]; ok {
		return b, true
	}
	for _, b := range leaves { // any leaf beats a fallback HTTP round-trip
		return b, true
	}
	return nil, false
}
