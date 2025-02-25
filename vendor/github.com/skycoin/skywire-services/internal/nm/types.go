// Package nm internal/nm/types.go
package nm

// VisorSummary summary of a visor connection
type VisorSummary struct {
	Timestamp int64 `json:"timestamp"`
	Sudph     bool  `json:"sudph"`
	Stcpr     bool  `json:"stcpr"`
}

// Summary of visors
type Summary struct {
	Visor *VisorSummary `json:"visor,omitempty"`
}
