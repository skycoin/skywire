package router

import (
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
		bandwidthReceivedRecStart: time.Now(),
	}
}

func (s *networkStats) SetLatency(latency time.Duration) {
	atomic.StoreUint32(&s.latency, uint32(latency.Milliseconds()))
}

func (s *networkStats) Latency() time.Duration {
	latencyMs := atomic.LoadUint32(&s.latency)

	return time.Duration(latencyMs)
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
	atomic.AddUint64(&s.bandwidthReceived, amount)
}

func (s *networkStats) RemoteThroughput() int64 {
	s.bandwidthReceivedRecStartMu.Lock()
	timePassed := time.Since(s.bandwidthReceivedRecStart)
	s.bandwidthReceivedRecStart = time.Now()
	s.bandwidthReceivedRecStartMu.Unlock()

	bandwidth := atomic.SwapUint64(&s.bandwidthReceived, 0)

	throughput := float64(bandwidth) / timePassed.Seconds()

	return int64(throughput)
}
