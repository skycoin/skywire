// Package tpviz pkg/tpviz/cxo_subscriber.go
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

	"github.com/skycoin/skywire/pkg/servicedisc"
)

// Tab identifiers used in calls to CXOSubMgr.AcquireForTab /
// ReleaseForTab. Values match pkg/visor.CXOTab; the hypervisor
// adapter passes them through unchanged.
const (
	CXOTabNetworkVisualizer = 0
)

// Feed identifiers used in calls to CXOSubMgr.Walk. Values match
// pkg/visor.CXOFeed; the hypervisor adapter passes them through.
const (
	CXOFeedSDServices           = 2 // services/<type>/<pk>/{entry,tombstone}
	CXOFeedDMSGDClientsByServer = 3 // clients-by-server/<server>/<client>/{entry,tombstone}
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

// (DMSG-D clients-by-server consumer — future PR. The publisher tree
// lives at clients-by-server/<server>/<client>/{entry,tombstone}; a
// helper that walks it and produces map[serverPK][]clientPK can be
// added next to tryCXOServices once a tpviz handler needs it.
// CXOFeedDMSGDClientsByServer is reserved here for that consumer.)
var _ = CXOFeedDMSGDClientsByServer // silence unused-const linter
