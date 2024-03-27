package sdmetrics

// Metrics collects metrics for metrics tracking system.
type Metrics interface {
	SetServiceTypesCount(val uint64)
	SetServicesRegByTypeCount(val uint64)
	SetServiceTypeVPNCount(val uint64)
	SetServiceTypeVisorCount(val uint64)
	SetServiceTypeSkysocksCount(val uint64)
}
