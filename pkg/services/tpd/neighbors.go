// Package tpd pkg/services/tpd/neighbors.go c3-vis-appsvc
//
// What a service in the same process reads from transport discovery without
// asking it over dmsg (services.Neighbors). The reward system is the reader:
// its bandwidth collection and its metrics digest.
package tpd

import (
	"context"
	"errors"

	"github.com/skycoin/skywire/pkg/deployment/tpd/store"
)

// errNotStarted is returned before the store and API are built.
var errNotStarted = errors.New("transport-discovery has not started")

// TransportMetricsRead returns what GET /metrics answers for query.
func (s *service) TransportMetricsRead(ctx context.Context, query store.MetricsQuery) ([]store.TransportMetric, error) {
	a := s.runningAPI.Load()
	if a == nil {
		return nil, errNotStarted
	}
	return a.TransportMetrics(ctx, query)
}

// MetricsDaysRead returns the dates of the metrics feed's day leaves, newest
// first, or nil when the feed is not published.
func (s *service) MetricsDaysRead() []string {
	if mp := s.metricsPub.Load(); mp != nil {
		return mp.PublishedDays()
	}
	return nil
}

// MetricsDayPartsRead returns the parts of one day leaf of the metrics feed,
// the bytes a subscriber to the feed receives.
func (s *service) MetricsDayPartsRead(date string) ([][]byte, bool) {
	if mp := s.metricsPub.Load(); mp != nil {
		return mp.PublishedDayParts(date)
	}
	return nil, false
}
