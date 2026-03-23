package utmetrics

// Metrics collects metrics for metrics tracking system.
type Metrics interface {
	SetEntriesCount(val int64)
}
