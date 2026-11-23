// Package clirewards cmd/skywire-cli/commands/rewards/tpdlocal.go c4-vis-cli
//
// The bandwidth collection reads transport discovery's records directly when
// transport discovery runs in the same process, as the transport-discovery
// and rewards blocks of one visor do (rewardsserver.TransportDiscoveryLocal).
package clirewards

import (
	"cmp"
	"context"

	tpdstore "github.com/skycoin/skywire/pkg/deployment/tpd/store"
)

// tpdMetricsReader reads transport discovery's metrics in this process.
type tpdMetricsReader interface {
	TransportMetricsRead(ctx context.Context, query tpdstore.MetricsQuery) ([]tpdstore.TransportMetric, error)
}

// tpdMetricsLookup returns transport discovery in this process, or nil.
type tpdMetricsLookup func() tpdMetricsReader

// reader returns transport discovery in this process, or nil.
func (l tpdMetricsLookup) reader() tpdMetricsReader {
	if l == nil {
		return nil
	}
	return l()
}

// eachLocalTPDTransport passes fn each transport of what GET
// /metrics?days=<days>&bandwidth=true&latency=false&edges=true answers, read
// from local without the JSON body, and returns how many it passed.
func eachLocalTPDTransport(ctx context.Context, local tpdMetricsReader, days int, fn func(*tpdTransport)) (int, error) {
	// The query parseMetricsQuery (pkg/deployment/tpd/api) makes of that URL.
	recs, err := local.TransportMetricsRead(ctx, tpdstore.MetricsQuery{
		Days:      days,
		Live:      "all",
		Edges:     true,
		Bandwidth: true,
	})
	if err != nil {
		return 0, err
	}
	for i := range recs {
		m := &recs[i]
		fn(&tpdTransport{ID: m.ID, Type: m.Type, Live: m.Live, Edges: m.Edges, Daily: m.Daily})
		recs[i] = tpdstore.TransportMetric{} // a reduced record need not stay
	}
	return len(recs), nil
}

// compareTransportBW orders the transports of a bandwidth file.
func compareTransportBW(x, y TransportBW) int {
	return cmp.Or(cmp.Compare(x.A, y.A), cmp.Compare(x.B, y.B), cmp.Compare(x.SentA, y.SentA), cmp.Compare(x.SentB, y.SentB))
}
