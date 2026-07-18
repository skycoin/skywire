// Package metrics pkg/dmsg/dmsg/metrics/delta.go c1-net-dmsg
package metrics

// DeltaType represents a change in metrics gauge.
type DeltaType int

// Delta types.
const (
	DeltaFailed     DeltaType = 0
	DeltaConnect    DeltaType = 1
	DeltaDisconnect DeltaType = -1
)
