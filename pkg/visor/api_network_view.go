// Package visor pkg/visor/api_network_view.go
//
// NetworkView — server-side combination of service-discovery,
// transport-discovery, and uptime-tracker data into a per-PK
// summary, mirroring the table that `skywire cli sd` prints.
//
// Lives at the hypervisor scope (no per-visor binding) because the
// data is network-wide. Cached for shortLivedCacheTTL on the visor
// side to absorb the 30s+ poll the UI does, plus any concurrent
// CLI hits.
package visor

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/servicedisc"
)

// NetworkViewEntry is one row of the combined network table.
// Field names match the JSON tags `cli sd --json` emits so callers
// (UI, scripts) can consume both interchangeably.
type NetworkViewEntry struct {
	PK       string `json:"pk"`
	Country  string `json:"country,omitempty"`
	Version  string `json:"version,omitempty"`
	Services string `json:"services,omitempty"`
	STCPR    int    `json:"stcpr"`
	SUDPH    int    `json:"sudph"`
	DMSG     int    `json:"dmsg"`
	STCP     int    `json:"stcp"`
	Total    int    `json:"total"`
	UTStatus string `json:"ut_status,omitempty"` // "online" | "offline" | "" (not in UT)
}

// NetworkViewResponse is what /api/network-view returns. The
// FetchedAt timestamp lets the UI render "x seconds ago" without
// having to track the request time client-side.
type NetworkViewResponse struct {
	Entries   []NetworkViewEntry `json:"entries"`
	FetchedAt time.Time          `json:"fetched_at"`
}

// networkViewCacheTTL is the freshness window for the combined
// fetch. SD/TPD/UT update at the 30s+ scale; 5 minutes is plenty
// for "is the network healthy" browsing. The UI surfaces a manual
// refresh button (forwarded to NetworkViewRefresh below) for
// callers who need a current sample on demand.
const networkViewCacheTTL = 5 * time.Minute

type networkViewCache struct {
	mu       sync.Mutex
	cachedAt time.Time
	response *NetworkViewResponse
}

var networkViewCacheInstance = &networkViewCache{}

// NetworkView returns the combined SD/TPD/UT view, served from a
// short-lived cache. Cache is invalidated on miss/expiry; concurrent
// callers during a refetch share the new result. The error return
// is reserved for future use (e.g., reporting that *all* upstream
// services were unreachable); today the underlying compute never
// returns an error — partial fetches yield partial tables.
func (v *Visor) NetworkView() (*NetworkViewResponse, error) {
	return v.networkView(false)
}

// NetworkViewRefresh forces re-aggregation regardless of cache age.
// Used by the UI's manual-refresh button.
func (v *Visor) NetworkViewRefresh() (*NetworkViewResponse, error) {
	return v.networkView(true)
}

func (v *Visor) networkView(forceRefresh bool) (*NetworkViewResponse, error) {
	networkViewCacheInstance.mu.Lock()
	defer networkViewCacheInstance.mu.Unlock()

	if !forceRefresh && networkViewCacheInstance.response != nil &&
		time.Since(networkViewCacheInstance.cachedAt) < networkViewCacheTTL {
		return networkViewCacheInstance.response, nil
	}

	resp := v.computeNetworkView()
	networkViewCacheInstance.response = resp
	networkViewCacheInstance.cachedAt = time.Now()
	return resp, nil
}

// computeNetworkView does the actual aggregation. Mirrors the
// structure of `cli sd`: fetch SD (3 service types) + TPD all-
// transports + UT, build a PK-keyed map, count transports by type.
//
// SD is queried via the visor's existing FetchServiceData plumbing
// (the visor knows its configured discovery URLs); a partial
// failure on one of the three SD types or on UT/TPD doesn't fail
// the whole call — the missing slice is treated as empty so the
// table still renders with what we got.
func (v *Visor) computeNetworkView() *NetworkViewResponse {
	type sdEntry struct {
		Address string `json:"address"`
		Geo     struct {
			Country string `json:"country"`
		} `json:"geo"`
		Version string `json:"version"`
	}

	fetchSD := func(serviceType string) []sdEntry {
		body, err := v.FetchServiceData("sd", "/api/services?type="+serviceType)
		if err != nil {
			return nil
		}
		var es []sdEntry
		if err := json.Unmarshal(body, &es); err != nil {
			return nil
		}
		return es
	}

	proxyEntries := fetchSD(servicedisc.ServiceTypeProxy)
	vpnEntries := fetchSD(servicedisc.ServiceTypeVPN)
	visorEntries := fetchSD(servicedisc.ServiceTypeVisor)

	// Service map by PK
	type serviceInfo struct {
		PK        string
		DisplayPK string
		Country   string
		Version   string
		Services  []string
	}
	serviceMap := make(map[string]*serviceInfo)
	mergeIn := func(entries []sdEntry, kind string, withDisplayPK bool) {
		for _, e := range entries {
			pk := strings.Split(e.Address, ":")[0]
			s := serviceMap[pk]
			if s == nil {
				s = &serviceInfo{PK: pk, Country: e.Geo.Country, Version: e.Version}
				if withDisplayPK {
					s.DisplayPK = e.Address
				}
				serviceMap[pk] = s
			} else {
				if s.Country == "" {
					s.Country = e.Geo.Country
				}
				if s.Version == "" {
					s.Version = e.Version
				}
				if withDisplayPK && s.DisplayPK == "" {
					s.DisplayPK = e.Address
				}
			}
			s.Services = append(s.Services, kind)
		}
	}
	mergeIn(proxyEntries, "proxy", false)
	mergeIn(vpnEntries, "vpn", false)
	mergeIn(visorEntries, "visor", true)

	// UT — uptimes?v=v2 returns [{pk, on}, ...]
	type utEntry struct {
		PK string `json:"pk"`
		On bool   `json:"on"`
	}
	utStatus := make(map[string]string)
	if body, err := v.FetchServiceData("ut", "/uptimes?v=v2"); err == nil {
		var es []utEntry
		if err := json.Unmarshal(body, &es); err == nil {
			for _, ut := range es {
				if ut.On {
					utStatus[ut.PK] = "online"
				} else {
					utStatus[ut.PK] = "offline"
				}
			}
		}
	}

	// TPD — all-transports
	type tpEntry struct {
		Edges []string `json:"edges"`
		Type  string   `json:"type"`
	}
	type tpCount struct {
		STCPR, SUDPH, DMSG, STCP, Total int
	}
	tpMap := make(map[string]*tpCount)
	if body, err := v.FetchServiceData("tpd", "/all-transports"); err == nil {
		var tps []tpEntry
		if err := json.Unmarshal(body, &tps); err == nil {
			for _, tp := range tps {
				for _, edge := range tp.Edges {
					if tpMap[edge] == nil {
						tpMap[edge] = &tpCount{}
					}
					switch tp.Type {
					case "stcpr":
						tpMap[edge].STCPR++
					case "sudph":
						tpMap[edge].SUDPH++
					case "dmsg":
						tpMap[edge].DMSG++
					case "stcp":
						tpMap[edge].STCP++
					}
					tpMap[edge].Total++
				}
			}
		}
	}

	// Combine
	out := make([]NetworkViewEntry, 0, len(serviceMap)+16)
	for pk, info := range serviceMap {
		c := tpMap[pk]
		if c == nil {
			c = &tpCount{}
		}
		display := pk
		if info.DisplayPK != "" {
			display = info.DisplayPK
		}
		out = append(out, NetworkViewEntry{
			PK:       display,
			Country:  info.Country,
			Version:  info.Version,
			Services: strings.Join(info.Services, ","),
			STCPR:    c.STCPR,
			SUDPH:    c.SUDPH,
			DMSG:     c.DMSG,
			STCP:     c.STCP,
			Total:    c.Total,
			UTStatus: utStatus[pk],
		})
	}
	// PKs that have transports but aren't in SD (uncommon — visors
	// without SD registration but with active TPD entries).
	for pk, c := range tpMap {
		if serviceMap[pk] != nil {
			continue
		}
		out = append(out, NetworkViewEntry{
			PK:       pk,
			STCPR:    c.STCPR,
			SUDPH:    c.SUDPH,
			DMSG:     c.DMSG,
			STCP:     c.STCP,
			Total:    c.Total,
			UTStatus: utStatus[pk],
		})
	}

	// Sort: highest total transports first; tiebreak alphabetical
	// to keep a stable order across cached refetches.
	sortNetworkEntries(out)

	return &NetworkViewResponse{
		Entries:   out,
		FetchedAt: time.Now(),
	}
}

func sortNetworkEntries(entries []NetworkViewEntry) {
	// Tiny inlined sort — O(n²) is fine for hundreds of network
	// entries and avoids pulling in a comparator type.
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && less(entries[j], entries[j-1]); j-- {
			entries[j], entries[j-1] = entries[j-1], entries[j]
		}
	}
}

func less(a, b NetworkViewEntry) bool {
	if a.Total != b.Total {
		return a.Total > b.Total
	}
	return a.PK < b.PK
}

// formatNetworkViewError is a placeholder for richer error
// reporting if multiple subsystems fail. Unused for now — callers
// just see "service unreachable" via FetchServiceData.
//
//nolint:unused
func formatNetworkViewError(err error) error {
	return fmt.Errorf("network view aggregation failed: %w", err)
}
