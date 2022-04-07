package servermetrics

// NewEmpty constructs new empty metrics.
func NewEmpty() Empty {
	return Empty{}
}

// Empty implements Metrics, but does nothing.
type Empty struct{}

// RecordSession implements `Metrics`.
func (Empty) RecordSession(_ DeltaType) {}

// RecordStream implements `Metrics`.
func (Empty) RecordStream(_ DeltaType) {}

// SetPacketsPerMinute implements `Metrics`.
func (Empty) SetPacketsPerMinute(_ uint64) {}

// SetPacketsPerSecond implements `Metrics`.
func (Empty) SetPacketsPerSecond(_ uint64) {}

// SetClientsCount implements `Metrics`.
func (Empty) SetClientsCount(_ int64) {}
