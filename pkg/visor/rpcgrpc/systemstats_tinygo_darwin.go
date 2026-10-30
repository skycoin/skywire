//go:build tinygo && darwin

package rpcgrpc

import (
	"context"
	"errors"
)

// SystemStatsCollector reports no stats in a TinyGo build for macOS, where
// gopsutil needs the macOS SDK headers through cgo.
type SystemStatsCollector struct{}

// NewSystemStatsCollector creates a new system stats collector
func NewSystemStatsCollector() *SystemStatsCollector {
	return &SystemStatsCollector{}
}

// Collect returns an error, see SystemStatsCollector.
func (c *SystemStatsCollector) Collect(ctx context.Context, includeProcesses bool, processLimit int) (*SystemStats, error) {
	return nil, errors.New("system stats are not available in a TinyGo build for macOS")
}
