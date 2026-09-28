//go:build mobile

// Package rpcgrpc pkg/visor/rpcgrpc/systemstats_mobile.go c3-vis-core
package rpcgrpc

import (
	"context"
	"errors"
)

// errSystemStatsUnavailable is the mobile build's answer to every system-stats
// request. The gopsutil collector (systemstats.go) is desktop-only: its darwin
// side is cgo against libproc.h and IOKit, which the iOS SDK does not ship.
// Remote management keeps the rest of this service; StreamSystemStats and
// GetSystemStats already turn a collector error into SystemStats.Error.
var errSystemStatsUnavailable = errors.New("system stats are not available on this build")

// SystemStatsCollector collects nothing on the mobile build.
type SystemStatsCollector struct{}

// NewSystemStatsCollector creates the mobile build's collector.
func NewSystemStatsCollector() *SystemStatsCollector {
	return &SystemStatsCollector{}
}

// Collect always reports errSystemStatsUnavailable.
func (c *SystemStatsCollector) Collect(_ context.Context, _ bool, _ int) (*SystemStats, error) {
	return nil, errSystemStatsUnavailable
}
