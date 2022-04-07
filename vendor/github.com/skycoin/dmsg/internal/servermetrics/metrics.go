package servermetrics

// Metrics collects metrics for metrics tracking system.
type Metrics interface {
	RecordSession(delta DeltaType)
	RecordStream(delta DeltaType)
	SetClientsCount(val int64)
	SetPacketsPerSecond(val uint64)
	SetPacketsPerMinute(val uint64)
}
