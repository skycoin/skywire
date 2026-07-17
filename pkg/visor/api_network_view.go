// Package visor pkg/visor/api_network_view.go c3-vis-core
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
	"fmt"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/visor/netview"
)

// NetworkViewEntry / NetworkViewResponse are the network-view table types. They
// live in the dependency-free pkg/visor/netview leaf (so the wasm-visor can build
// the same view without importing pkg/visor, which doesn't compile for js/wasm)
// and are aliased here for the rest of pkg/visor that consumes them.
type NetworkViewEntry = netview.Entry
type NetworkViewResponse = netview.Response

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
	return netview.Compute(v.FetchServiceData)
}

// formatNetworkViewError is a placeholder for richer error
// reporting if multiple subsystems fail. Unused for now — callers
// just see "service unreachable" via FetchServiceData.
//
//nolint:unused
func formatNetworkViewError(err error) error {
	return fmt.Errorf("network view aggregation failed: %w", err)
}
