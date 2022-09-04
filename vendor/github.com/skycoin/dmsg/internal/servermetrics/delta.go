// Package servermetrics internal/servermetrics/delta.go
package servermetrics

// DeltaType represents a change in metrics gauge.
type DeltaType int

// Delta types.
const (
	DeltaFailed     DeltaType = 0
	DeltaConnect    DeltaType = 1
	DeltaDisconnect DeltaType = -1
)
