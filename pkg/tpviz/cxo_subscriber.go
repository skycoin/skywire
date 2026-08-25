// Package tpviz pkg/tpviz/cxo_subscriber.go c4-app-rewards
//
// Optional integration with the visor's on-demand CXO subscription
// manager. When the hypervisor wires one in via SetCXOSubMgr,
// /api/services and /api/dmsg/entries try the manager's local
// snapshot first and fall through to the existing HTTP fetcher on
// cache miss. AcquireFor / ReleaseFor on each request scopes the
// underlying CXO subscription to active UI traffic (with the
// manager's own 10s grace handling multi-fetch tab loads).
//
// Tab and feed identifiers are int constants here that mirror the
// values defined on the visor side (CXOTab / CXOFeed). The
// hypervisor's wiring is responsible for matching them; this is a
// minimal compromise to keep tpviz independent of pkg/visor (which
// would create an import cycle).
package tpviz

import (
	"encoding/json"
	"strings"

	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	"github.com/skycoin/skywire/pkg/servicedisc"
)

// Tab identifiers used in calls to CXOSubMgr.AcquireForTab /
// ReleaseForTab. Values match pkg/visor.CXOTab / cxosub.Tab; the
// hypervisor adapter and the standalone adapter pass them through.
const (
	CXOTabNetworkVisualizer = 0
	CXOTabUptime            = 2 // TPD uptime feed
	CXOTabCLITransports     = 5 // TPD all-transports feed
)

// Feed identifiers used in calls to CXOSubMgr.Walk. Values match
// pkg/visor.CXOFeed / cxosub.Feed; the adapters pass them through.
const (
	CXOFeedTPDMetrics           = 0 // metrics/days/<n>
	CXOFeedTPDUptime            = 1 // uptimes/days/<n> — []VisorSummary
	CXOFeedSDServices           = 2 // services/<type>/<pk>/{entry,tombstone}
	CXOFeedDMSGDClientsByServer = 3 // clients-by-server/<server> (batched; legacy .../<client>/entry)
	CXOFeedTPDAllTransports     = 4 // transports/all/{with-self,without-self}
)

// CXOSubMgr is the minimal slice of pkg/visor.CXOSubscriptionManager
// tpviz needs. Defined as an interface so tpviz can be built and
// tested without importing pkg/visor (which would close an import
// cycle: visor → tpviz → visor). The hypervisor wires a thin
// adapter in pkg/visor/hypervisor.go.
type CXOSubMgr interface {
	AcquireForTab(tab int)
	ReleaseForTab(tab int)
	Walk(feed int, prefix string, fn func(path string, body []byte) bool) bool
}

// SetCXOSubMgr installs (or replaces, with nil to clear) the CXO
// subscription manager. Idempotent. Safe to call before or after
// the server is serving.
func (s *Server) SetCXOSubMgr(m CXOSubMgr) {
	s.cxoSubMu.Lock()
	defer s.cxoSubMu.Unlock()
	s.cxoSubMgr = m
}

// cxoMgr returns the currently installed manager, or nil when none.
// Hot-path read; takes the read lock so concurrent SetCXOSubMgr
// (rare — only at startup) doesn't race.
func (s *Server) cxoMgr() CXOSubMgr {
	s.cxoSubMu.RLock()
	defer s.cxoSubMu.RUnlock()
	return s.cxoSubMgr
}

// tryCXOServices walks the SD services subscriber tree and builds
// the same map[pk]ServiceInfo that handleServices' HTTP path
// constructs. Returns ok=false when the manager isn't installed,
// the feed isn't subscribed (no AcquireFor live, or dial failed),
// or the subscriber hasn't received any leaves yet — in any of
// those cases the caller falls through to its existing HTTP
// fetcher.
//
// The walk reconstructs the per-type service grouping that the
// HTTP path gets from servicedisc's per-type query: the leaf path
// is services/<type>/<pk>/entry, the body is the JSON-encoded
// servicedisc.Service. We pluck <type> off the path (cheap) so we
// don't have to re-parse it from the body.
func (s *Server) tryCXOServices() (map[string]ServiceInfo, bool) {
	mgr := s.cxoMgr()
	if mgr == nil {
		return nil, false
	}
	services := make(map[string]ServiceInfo)
	any := false
	ok := mgr.Walk(CXOFeedSDServices, "services/", func(path string, body []byte) bool {
		// Skip tombstones; we only want live entries.
		if !strings.HasSuffix(path, "/entry") {
			return true
		}
		// path = services/<type>/<pk>/entry
		parts := strings.Split(path, "/")
		if len(parts) != 4 {
			return true
		}
		svcType := parts[1]
		pk := parts[2]

		// Body is a servicedisc.Service. We want the geo country
		// for the IP-grouping overlay; the address we already
		// have from the path.
		var svc servicedisc.Service
		if err := json.Unmarshal(body, &svc); err != nil {
			return true
		}

		any = true
		if info, exists := services[pk]; exists {
			info.Services = append(info.Services, svcType)
			if info.Country == "" && svc.Geo != nil && svc.Geo.Country != "" {
				info.Country = svc.Geo.Country
			}
			services[pk] = info
			return true
		}
		country := ""
		if svc.Geo != nil {
			country = svc.Geo.Country
		}
		services[pk] = ServiceInfo{
			PK:       pk,
			Services: []string{svcType},
			Country:  country,
		}
		return true
	})
	if !ok || !any {
		return nil, false
	}
	return services, true
}

// tryCXOClientsByServer walks the DMSG-D clients-by-server snapshot
// and rebuilds a map[server-pk-hex][]disc.Entry — same shape the
// upstream DMSG-D /dmsg-discovery/servers/clients HTTP endpoint
// returns. Returns ok=false when the manager isn't installed, the
// snapshot is empty, or every entry leaf fails to parse — caller
// falls through to its existing HTTP path.
//
// The current publisher writes ONE batched leaf per server at
// clients-by-server/<server> (a version-framed, gzipped JSON []Entry);
// the server segment is the map key and the array is that server's whole
// client set. Older publishers wrote one leaf per pair at
// clients-by-server/<server>/<client>/entry — both shapes are read here.
func (s *Server) tryCXOClientsByServer() (map[string][]*discEntry, bool) {
	mgr := s.cxoMgr()
	if mgr == nil {
		return nil, false
	}
	out := make(map[string][]*discEntry)
	any := false
	mgr.Walk(CXOFeedDMSGDClientsByServer, "clients-by-server/", func(path string, body []byte) bool {
		if len(body) == 0 {
			return true
		}
		parts := strings.Split(path, "/")
		// Batched per-server leaf: ["clients-by-server", "<server>"].
		if len(parts) == 2 && parts[1] != "" {
			entries := decodeClientsBatch(body)
			if len(entries) == 0 {
				return true
			}
			out[parts[1]] = append(out[parts[1]], entries...)
			any = true
			return true
		}
		// Legacy per-item leaf: clients-by-server/<server>/<client>/entry.
		if len(parts) != 4 || !strings.HasSuffix(path, "/entry") {
			return true
		}
		var entry discEntry
		if err := json.Unmarshal(body, &entry); err != nil {
			return true
		}
		out[parts[1]] = append(out[parts[1]], &entry)
		any = true
		return true
	})
	if !any {
		return nil, false
	}
	return out, true
}

// clientsByServerBatchVersion is the wire-format version byte of the
// batched per-server leaf (must match the dmsg-discovery publisher's
// clientsByServerBatchVersion). A leaf with any other version is skipped.
const clientsByServerBatchVersion = 1

// decodeClientsBatch decodes a batched per-server leaf body into its
// client entries (tpviz's local discEntry shape). Returns nil on any
// framing/version/JSON error so a bad leaf is skipped, not misparsed.
func decodeClientsBatch(body []byte) []*discEntry {
	version, payload, ok := cxoutils.UnframeGzip(body)
	if !ok || version != clientsByServerBatchVersion {
		return nil
	}
	var entries []*discEntry
	if err := json.Unmarshal(payload, &entries); err != nil {
		return nil
	}
	return entries
}

// discEntry is a tpviz-local mirror of the disc.Entry JSON shape.
// Defined here (rather than imported from pkg/dmsg/disc) so tpviz
// stays decoupled from the dmsg-disc package's full type surface —
// we only need the JSON-serializable shell for the response body.
// The fields match the disc.Entry public layout; unknown fields in
// the wire data are tolerated by encoding/json.
type discEntry struct {
	Static    string         `json:"static"`
	Sequence  uint64         `json:"sequence,omitempty"`
	Timestamp int64          `json:"timestamp,omitempty"`
	Client    *discClient    `json:"client,omitempty"`
	Server    *discServerRec `json:"server,omitempty"`
	Signature string         `json:"signature,omitempty"`
	Version   string         `json:"version,omitempty"`
}

type discClient struct {
	DelegatedServers []string `json:"delegated_servers,omitempty"`
}

type discServerRec struct {
	Address           string `json:"address,omitempty"`
	AvailableSessions int    `json:"availableSessions,omitempty"`
	ServerType        string `json:"serverType,omitempty"`
}
