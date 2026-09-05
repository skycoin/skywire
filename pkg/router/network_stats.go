// Package router pkg/router/network_stats.go c2-net-routing
package router

import (
	"math"
	"sync"
	"sync/atomic"
	"time"
)

type networkStats struct {
	totalBandwidthSent     uint64
	totalBandwidthReceived uint64
	bandwidthReceived      uint64
	latency                uint32
	uploadSpeed            uint32
	downloadSpeed          uint32

	bandwidthReceivedRecStartMu sync.Mutex
	bandwidthReceivedRecStart   time.Time
}

func newNetworkStats() *networkStats {
	return &networkStats{
		bandwidthReceivedRecStart: time.Now().UTC(),
	}
}

func (s *networkStats) SetLatency(latency uint32) {
	atomic.StoreUint32(&s.latency, latency)
}

func (s *networkStats) Latency() time.Duration {
	latencyMs := atomic.LoadUint32(&s.latency)
	// the latency is store in uint32 of millisecond but time.Duration takes nanosecond
	return time.Duration(latencyMs * uint32(time.Millisecond.Nanoseconds())) //nolint: gosec
}

func (s *networkStats) SetUploadSpeed(speed uint32) {
	atomic.StoreUint32(&s.uploadSpeed, speed)
}

func (s *networkStats) UploadSpeed() uint32 {
	return atomic.LoadUint32(&s.uploadSpeed)
}

func (s *networkStats) SetDownloadSpeed(speed uint32) {
	atomic.StoreUint32(&s.downloadSpeed, speed)
}

func (s *networkStats) DownloadSpeed() uint32 {
	return atomic.LoadUint32(&s.downloadSpeed)
}

func (s *networkStats) BandwidthSent() uint64 {
	return atomic.LoadUint64(&s.totalBandwidthSent)
}

func (s *networkStats) AddBandwidthSent(amount uint64) {
	atomic.AddUint64(&s.totalBandwidthSent, amount)
}

func (s *networkStats) BandwidthReceived() uint64 {
	return atomic.LoadUint64(&s.totalBandwidthReceived)
}

func (s *networkStats) AddBandwidthReceived(amount uint64) {
	atomic.AddUint64(&s.bandwidthReceived, amount)
	atomic.AddUint64(&s.totalBandwidthReceived, amount)
}

func (s *networkStats) RemoteThroughput() int64 {
	return s.remoteThroughputAt(time.Now().UTC())
}

// remoteThroughputAt is RemoteThroughput with the sampling instant injected, so
// the zero-width and backwards-clock windows can be tested deterministically
// instead of racing the real clock.
func (s *networkStats) remoteThroughputAt(now time.Time) int64 {

	s.bandwidthReceivedRecStartMu.Lock()
	timePassed := now.Sub(s.bandwidthReceivedRecStart)
	// Only open a new sampling window when this one had measurable width. Two
	// calls inside a single clock tick — routine on Windows, whose timer
	// granularity is coarse — would otherwise both divide by zero AND discard
	// the bytes counted so far. Leaving the start time and the accumulator
	// alone lets the next call measure the wider window instead.
	if timePassed > 0 {
		s.bandwidthReceivedRecStart = now
	}
	s.bandwidthReceivedRecStartMu.Unlock()

	if timePassed <= 0 {
		return 0
	}

	bandwidth := atomic.SwapUint64(&s.bandwidthReceived, 0)
	throughput := float64(bandwidth) / timePassed.Seconds()

	// Go leaves float64->int64 UNDEFINED for NaN and for values outside int64's
	// range; on amd64 it yields math.MinInt64. A zero-width window therefore used
	// to report a large NEGATIVE throughput, and this value does not stay local:
	// it is written into the ping packet sent to the peer (MakePingPacket), so
	// the garbage propagated into the remote's view of the leg.
	if math.IsNaN(throughput) || throughput <= 0 {
		return 0
	}
	if throughput >= math.MaxInt64 {
		return math.MaxInt64
	}

	return int64(throughput)
}
