// Package api pkg/deployment/tpd/api/routing_entries.go c4-net-discovery
package api

import (
	"context"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/deployment/tpd/store"
	"github.com/skycoin/skywire/pkg/transport"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// allTransportsWireEntry is the published shape of one transport. It is
// transport.Entry without t_id, which the reader recomputes from the edges
// and type (transport.MakeTransportID).
type allTransportsWireEntry struct {
	Edges         [2]cipher.PubKey `json:"edges"`
	Type          types.Type       `json:"type"`
	Label         transport.Label  `json:"label"`
	Latency       float64          `json:"latency_ms,omitempty"`
	ThroughputBps float64          `json:"throughput_bps,omitempty"`
}

// routingEntries is what routers need: the transports that exist now, with
// the latency and throughput routes are weighed by.
func routingEntries(ctx context.Context, st store.Store) ([]*transport.Entry, error) {
	if qs, ok := st.(interface {
		GetAllTransportsWithLatency(context.Context, bool) ([]*transport.Entry, error)
	}); ok {
		return qs.GetAllTransportsWithLatency(ctx, false)
	}
	return st.GetAllTransports(ctx, false)
}
