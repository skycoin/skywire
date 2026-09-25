// Package visorapi pkg/visor/visorapi/hypervisor.go c3-vis-core
package visorapi

import (
	"github.com/skycoin/skywire/pkg/cipher"
)

// HVVisorEntry is a summary of a remote visor connected to this hypervisor.
type HVVisorEntry struct {
	PK             cipher.PubKey `json:"pk"`
	Online         bool          `json:"online"`
	IsLocal        bool          `json:"is_local,omitempty"`
	Version        string        `json:"version,omitempty"`
	BuildTag       string        `json:"build_tag,omitempty"`
	Uptime         float64       `json:"uptime_seconds,omitempty"`
	LocalIP        string        `json:"local_ip,omitempty"`
	PublicIP       string        `json:"public_ip,omitempty"`
	CountryCode    string        `json:"country_code,omitempty"`
	IsSymmetricNAT bool          `json:"symmetric_nat,omitempty"`
	Transports     int           `json:"transports"`
	// TransportSummaries is the full per-transport detail (type,
	// remote PK, sent/recv counters, etc.) the hvui's node-list
	// table needs to render its Transports column. Populated by
	// populateEntryFromSummary from summary.Overview.Transports.
	// omitempty so older sub-hypervisor binaries that don't fill
	// this field don't blow the JSON shape.
	TransportSummaries []*TransportSummary `json:"transport_summaries,omitempty"`
	Apps               int                 `json:"apps"`
	RewardAddress      string              `json:"reward_address,omitempty"`
	ConfigVersion      string              `json:"config_version,omitempty"`
	// ServicesHealth is the remote visor's most recent
	// HealthInfo.ServicesHealth ("healthy", "unhealthy",
	// "connecting", ""). Carried so the hvui's tree-summary
	// sub-section can render the same green / yellow / gray status
	// dot that the local section's main table does — without this,
	// every sub-section row falls through to nodeStatusClass's
	// "online but health unknown" branch and renders as a gray
	// outline circle even when the visor is fully healthy.
	ServicesHealth string `json:"services_health,omitempty"`
	// Hostname is the visor's os.Hostname(). The hvui's main node
	// list uses this as the default Label when no explicit label is
	// set; without it, sub-section rows render with an empty Label
	// column even though the visor itself reports a valid hostname.
	// Carried separately from PK because Overview.Hostname doesn't
	// survive the HVVisorEntry round-trip otherwise.
	Hostname string `json:"hostname,omitempty"`
	Error    string `json:"error,omitempty"`
	// ProxiedVia is set when this entry was discovered through a connected
	// sub-hypervisor rather than a direct connection. The value is the PK
	// of the sub-hypervisor that proxies operations on this visor.
	ProxiedVia *cipher.PubKey `json:"proxied_via,omitempty"`
	// Load is the visor's resource snapshot (load average, mem %, disk %),
	// from summary.Load. Rendered by `hv ls --load`. omitempty so older
	// sub-hypervisor binaries that don't carry it don't break the JSON shape.
	Load *LoadStats `json:"load,omitempty"`
	// Hypervisors is the set of hypervisors THIS visor is configured to be
	// managed by (its conf.Hypervisors), reported by the visor itself. An
	// attached visor commonly answers to more than one hypervisor, and until
	// now the only way to learn the others was to ask that visor directly:
	// the tree sections describe hypervisors THIS one can reach, not the ones
	// its peers have set. omitempty so an older visor that does not report
	// them is absent rather than empty.
	Hypervisors []cipher.PubKey `json:"hypervisors,omitempty"`
}

// HVVisorTreeNode is one hypervisor's section in the tree response:
// the hypervisor's PK, the chain of hypervisors from the local one
// down to this one (empty for the local hypervisor itself), and the
// visors directly connected to this hypervisor (NOT transitively
// merged from any sub-hypervisors below it).
//
// Sub-hypervisors that fail to respond surface their error via the
// SubError field instead of being silently dropped — the UI can
// render a placeholder row with the error text, and operators can
// tell "no sub-visors" from "query failed."
type HVVisorTreeNode struct {
	HypervisorPK cipher.PubKey   `json:"hypervisor_pk"`
	ViaChain     []cipher.PubKey `json:"via_chain,omitempty"`
	Visors       []HVVisorEntry  `json:"visors"`
	SubError     string          `json:"sub_error,omitempty"`
}

// HVVisorTree is the structured response of HVListVisorsTree — a
// flat list of hypervisor sections. The first entry is the local
// hypervisor; subsequent entries are sub-hypervisors reachable from
// it. Each section's `via_chain` documents the path from the local
// hypervisor down to that sub-hypervisor, so the UI can render a
// breadcrumb without recomputing.
//
// Visor entries replicate across sections: a visor V that's connected
// to both the local hypervisor and a sub-hypervisor will appear in
// BOTH sections — the semantics are "every table shows every visor
// directly connected to that hypervisor," intentionally. Tables
// themselves dedup: two paths to the same sub-hypervisor render once.
type HVVisorTree struct {
	Sections []HVVisorTreeNode `json:"sections"`
}
