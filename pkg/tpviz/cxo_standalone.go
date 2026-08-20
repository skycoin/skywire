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
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxosub"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgcurl"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport-discovery/store"
)

// compactVisorSummaryLeaf mirrors the per-visor uptime leaf the TPD publisher
// writes at uptimes/days/<n>/<pkHex> (see cxo_uptime_publisher.go). Re-declared
// here to keep the dependency direction one-way; the reward-server UI only
// needs the v2 fields (pk/on/version/daily), so the bitmap timeline is decoded
// but not carried forward.
type compactVisorSummaryLeaf struct {
	PK      cipher.PubKey     `json:"pk"`
	Online  bool              `json:"on"`
	Version string            `json:"v,omitempty"`
	Daily   map[string]string `json:"d,omitempty"`
}

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

// tryCXOUptimes returns the []VisorSummary (v2: pk/on/version/daily) JSON from
// the TPD uptime feed's local snapshot, or ok=false when the manager isn't
// wired / the feed hasn't synced yet — in which case the caller falls through
// to its HTTP fetch.
//
// Dual-parse for the publisher rollout: the current TPD publishes one small
// per-visor leaf at uptimes/days/<n>/<pkHex> (a compactVisorSummaryLeaf), so
// the snapshot is reassembled into a []VisorSummary; a legacy publisher's
// monolithic array leaf (parses as a JSON array) is forwarded verbatim.
func (s *Server) tryCXOUptimes() ([]byte, bool) {
	mgr := s.cxoMgr()
	if mgr == nil {
		return nil, false
	}
	mgr.AcquireForTab(CXOTabUptime)
	defer mgr.ReleaseForTab(CXOTabUptime)

	// The snapshot holds every published day-window (1/7/30). The UI needs only
	// the current online status, which is identical across windows, so
	// reassemble a single window to avoid emitting each visor 1x per window.
	const window = "uptimes/days/1"
	var (
		monolith  []byte
		summaries []store.VisorSummary
	)
	mgr.Walk(CXOFeedTPDUptime, "uptimes/days/", func(path string, body []byte) bool {
		if len(body) == 0 || string(body) == "null" {
			return true
		}
		// Legacy monolith: a whole []VisorSummary array in one leaf at exactly
		// uptimes/days/<n>. Any window's array carries the online status.
		if body[0] == '[' {
			monolith = append([]byte(nil), body...)
			return false
		}
		// Current per-visor leaf: only the chosen window's children.
		if len(path) <= len(window)+1 || path[:len(window)+1] != window+"/" {
			return true
		}
		var c compactVisorSummaryLeaf
		if err := json.Unmarshal(body, &c); err != nil {
			return true // skip a malformed leaf; keep reassembling the rest
		}
		summaries = append(summaries, store.VisorSummary{
			PK:      c.PK,
			Online:  c.Online,
			Version: c.Version,
			Daily:   c.Daily,
		})
		return true
	})
	if monolith != nil {
		return monolith, true
	}
	if len(summaries) == 0 {
		return nil, false
	}
	out, err := json.Marshal(summaries)
	if err != nil {
		return nil, false
	}
	return out, true
}

// tryCXOTransports returns the all-transports JSON snapshot from the TPD
// all-transports feed's local snapshot (the network-wide "without-self" leaf),
// or ok=false on cache miss. The publisher gzips the leaf and drops each
// entry's derivable t_id, so the body is gunzipped and its t_id recomputed
// here — yielding the same plain JSON GET /all-transports serves.
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
		return restoreAllTransportsBody(b), true
	}
	for _, b := range leaves { // any leaf beats a fallback HTTP round-trip
		return restoreAllTransportsBody(b), true
	}
	return nil, false
}

// restoreAllTransportsBody gunzips the published all-transports leaf (a no-op
// on an already-plain body) and recomputes any derivable t_id the publisher
// dropped, so the UI sees the same shape GET /all-transports returns. On any
// decode error the gunzipped bytes are returned as-is.
func restoreAllTransportsBody(body []byte) []byte {
	body = cxoutils.Gunzip(body)
	if len(body) == 0 {
		return body
	}
	var entries []*transport.Entry
	if err := json.Unmarshal(body, &entries); err != nil {
		return body
	}
	changed := false
	for _, e := range entries {
		if e != nil && e.ID == (uuid.UUID{}) {
			e.ID = transport.MakeTransportID(e.Edges[0], e.Edges[1], e.Type)
			changed = true
		}
	}
	if !changed {
		return body
	}
	if out, err := json.Marshal(entries); err == nil {
		return out
	}
	return body
}
