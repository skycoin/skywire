// Package visorapi pkg/visor/visorapi/cxo.go c3-vis-core
package visorapi

import (
	"time"

	"github.com/skycoin/skywire/pkg/cxo/cxosub"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
)

// TPDLeafPublisherState reports whether the transport manager holds the
// CXO transport-list snapshot publisher (transport.Manager.
// SetTPDLeafPublisher). Wired=false means transport registration runs
// over the HTTP/dmsg-HTTP re-register path instead, and Reason carries
// why the publisher was never installed — the treestore construction
// error, "dmsg client absent", "stats disabled by config", or
// a generic "not completed init" note during the startup window.
type TPDLeafPublisherState struct {
	Wired  bool   `json:"wired"`
	Reason string `json:"reason,omitempty"`
}

// CXOFeedState pairs a feed's identity (name + dmsg port) with its live
// publish-health snapshot. Surfaced by StateSnapshot under .cxo.
type CXOFeedState struct {
	Name string `json:"name"`
	Port uint16 `json:"port,omitempty"`
	treestore.PublishState
	// CurrentLeaves is populated only for the "stats" telemetry feed: it
	// reports the count of per-transport telemetry rows the feed carries
	// across its ≤16 compact sharded leaves (transports/telemetry/<sh>).
	// With the sharded shape every packed row is a LIVE transport (the
	// sampler re-encodes each shard from the live set every tick), so Live
	// == Total and Dead is structurally 0 — the stale-leaf bloat the old
	// per-transport `current` format accumulated no longer exists.
	CurrentLeaves *CurrentLeafStats `json:"current_leaves,omitempty"`
	// Allowlist is the set of PKs currently permitted to subscribe to this
	// feed (nil = OPEN to all). For a service-consumed feed (stats,
	// tp-list, registration) this MUST contain the consuming service's PK
	// or that service can't fill — its subscribe is rejected and it shows
	// up under Denied below.
	Allowlist []string `json:"allowlist,omitempty"`
	// Denied lists would-be subscribers the allowlist turned away
	// (most-recent first). A denied PK that isn't a known peer is the
	// direct signature of a gating misconfiguration — e.g. TPD dialing in
	// under a CXO node key that differs from the transport_discovery_dmsg
	// PK the visor allowlisted.
	Denied []DeniedSubscriber `json:"denied,omitempty"`
}

// DeniedSubscriber is one rejected-subscriber record for CXOFeedState.
type DeniedSubscriber struct {
	PK     string    `json:"pk"`
	Count  int       `json:"count"`
	LastAt time.Time `json:"last_at"`
}

// CurrentLeafStats is the live/dead breakdown of the telemetry feed's
// per-transport telemetry rows, decoded from the compact sharded leaves
// (transports/telemetry/<sh>). Each row is classified by whether its
// transport is in the visor's live set; with the sharded shape the
// sampler only ever packs live transports, so Dead is normally 0.
type CurrentLeafStats struct {
	Total int `json:"total"`
	Live  int `json:"live"`
	Dead  int `json:"dead"`
}

// RegisterCXOFeedRequest is the input to RPC.RegisterCXOFeed.
type RegisterCXOFeedRequest struct {
	Name        string `json:"name"`
	DmsgPort    uint16 `json:"dmsg_port"`
	Description string `json:"description,omitempty"`
}

// Re-exported from cxosub because API methods return it.
type (
	// FeedStatus is one row of CXOSubscriptionManager.Status.
	FeedStatus = cxosub.FeedStatus
)
