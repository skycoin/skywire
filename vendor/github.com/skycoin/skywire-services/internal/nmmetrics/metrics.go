package nmmetrics

// Metrics collects metrics for metrics tracking system.
type Metrics interface {
	SetTotalVpnServerCount(val int64)
	SetTotalVisorCount(val int64)
	SetTpCount(stcprCount int64, sudphCount int64)
}
