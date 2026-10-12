// Package services pkg/services/state.go c2-vis-appsvc
package services

import (
	"github.com/skycoin/skywire/pkg/cxo/node"
	"github.com/skycoin/skywire/pkg/cxo/storeconfig"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/deployment/charts"
)

// State is what a running service reports about itself: `skywire cli visor
// state` lists it for every service the visor embeds. Every service fills
// the same fields, so one query reads the whole deployment host.
type State struct {
	// Store is the service's data store: "redis" or "memory".
	Store string `json:"store"`
	// NonceStore is where the auth nonces live: "redis", "memory", or
	// "none" (test mode, requests are not authenticated).
	NonceStore string `json:"nonce_store,omitempty"`
	// Aggregators are the CXO fan-in nodes: visors publish a feed and the
	// service subscribes to each (registration, telemetry).
	Aggregators []AggregatorState `json:"cxo_aggregators,omitempty"`
	// Publishers are the CXO feeds the service publishes to the fleet.
	Publishers []PublisherState `json:"cxo_publishers,omitempty"`
}

// AggregatorState describes one CXO aggregator.
type AggregatorState struct {
	// Name says what the visors publish on this port.
	Name string `json:"name"`
	Port uint16 `json:"port"`
	// Shared is true when the aggregator runs on the host visor's own
	// publisher node for this port instead of a node of its own.
	Shared bool `json:"shared"`
	// Conns are the visor connections on the node; Subscribed of them
	// carry a subscription to the visor's feed.
	Conns      int `json:"conns"`
	Subscribed int `json:"subscribed"`
	// Feeds held in the node's store, the host's own included.
	Feeds int `json:"feeds"`
	// LocalRoots counts Roots taken from the host's own publisher.
	LocalRoots uint64 `json:"local_roots,omitempty"`
	// Error is why the aggregator is not running, if it is not.
	Error string `json:"error,omitempty"`
}

// PublisherState describes one CXO feed the service publishes.
type PublisherState struct {
	Name string `json:"name"`
	Port uint16 `json:"port"`
	treestore.PublishState
	node.PublisherStats
}

// ChartsPager is a service with a charts page. A host shows the pages of all
// its services together as one status page.
type ChartsPager interface {
	ChartsPage() *charts.Page
}

// Stater is implemented by services that report their State.
type Stater interface {
	State() State
}

// StoreKind names a store type for State.
func StoreKind(t storeconfig.Type) string {
	if t == storeconfig.Redis {
		return "redis"
	}
	return "memory"
}
