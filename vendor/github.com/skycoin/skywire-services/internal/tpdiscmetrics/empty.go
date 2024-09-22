// Package tpdiscmetrics internal/tpdiscmetrics/empty.go
package tpdiscmetrics

import "github.com/skycoin/skywire/pkg/transport/network"

// NewEmpty constructs new empty metrics.
func NewEmpty() Empty {
	return Empty{}
}

// Empty implements Metrics, but does nothing.
type Empty struct{}

// SetTPCounts implements `Metrics`.
func (Empty) SetTPCounts(_ map[network.Type]int) {}
