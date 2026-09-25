// Package visorapi pkg/visor/visorapi/stats.go c3-vis-core
package visorapi

import (
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/stats"
)

// LoadStats is a lightweight, always-meaningful resource snapshot used by
// `cli visor hv ls --load`: load averages (which, unlike a single CPUPercent
// sample, need no prior baseline to be meaningful) plus instantaneous memory
// and root-disk usage percentages. Cheap to gather (a few /proc reads + one
// statfs), so it's populated on every Summary without a sampling interval.
type LoadStats struct {
	Load1           float64 `json:"load1"`
	Load5           float64 `json:"load5"`
	Load15          float64 `json:"load15"`
	CPUCores        int     `json:"cpu_cores"`
	MemUsedPercent  float64 `json:"mem_used_percent"`
	DiskUsedPercent float64 `json:"disk_used_percent"`
}

// HostStatsInfo carries a snapshot of host system + visor process
// resource utilization. Returned by Visor.HostStats and surfaced
// to the hypervisor UI via /visors/<pk>/host-stats.
//
// All byte counts are bytes (not KB/MB); the UI is responsible for
// scaling for display.
type HostStatsInfo struct {
	// Host identity (mostly static; useful as a header above the
	// graphs so the user knows which box they're looking at).
	Hostname      string `json:"hostname,omitempty"`
	OS            string `json:"os,omitempty"`
	Platform      string `json:"platform,omitempty"`
	Arch          string `json:"arch,omitempty"`
	UptimeSeconds uint64 `json:"uptime_seconds,omitempty"`

	// CPU
	CPUPercent      float64 `json:"cpu_percent"`
	CPUCount        int     `json:"cpu_count"`
	CPULogicalCount int     `json:"cpu_logical_count"`

	// Memory (bytes)
	MemTotal     uint64  `json:"mem_total"`
	MemUsed      uint64  `json:"mem_used"`
	MemAvailable uint64  `json:"mem_available"`
	MemPercent   float64 `json:"mem_percent"`
	SwapTotal    uint64  `json:"swap_total,omitempty"`
	SwapUsed     uint64  `json:"swap_used,omitempty"`
	SwapPercent  float64 `json:"swap_percent,omitempty"`

	// Disk — root filesystem only. Per-mount breakdown can be a
	// follow-up; "/" is the signal users care about for "is the
	// visor about to run out of room for its log buffer / cxo
	// tree-store?"
	DiskTotal   uint64  `json:"disk_total,omitempty"`
	DiskUsed    uint64  `json:"disk_used,omitempty"`
	DiskFree    uint64  `json:"disk_free,omitempty"`
	DiskPercent float64 `json:"disk_percent,omitempty"`

	// Network — cumulative across all interfaces since boot. Client
	// diffs to derive Bps. Per-interface breakdown is omitted to
	// keep the payload small; can be added later as a `Per []…`
	// field if a user wants it.
	NetBytesSent   uint64 `json:"net_bytes_sent"`
	NetBytesRecv   uint64 `json:"net_bytes_recv"`
	NetPacketsSent uint64 `json:"net_packets_sent,omitempty"`
	NetPacketsRecv uint64 `json:"net_packets_recv,omitempty"`

	// Visor process specifics. Process is nil when we can't read
	// our own /proc entry (rare; surface as null in JSON).
	Process *ProcessStatsInfo `json:"process,omitempty"`
}

// ProcessStatsInfo is the visor process's slice of HostStats —
// what process.NewProcess(os.Getpid()) returns from gopsutil.
type ProcessStatsInfo struct {
	PID         int32   `json:"pid"`
	CPUPercent  float64 `json:"cpu_percent"`
	MemRSS      uint64  `json:"mem_rss"`
	MemVMS      uint64  `json:"mem_vms,omitempty"`
	NumThreads  int32   `json:"num_threads,omitempty"`
	NumFDs      int32   `json:"num_fds,omitempty"`
	StartTimeMS int64   `json:"start_time_ms,omitempty"`
	OpenConns   int     `json:"open_conns,omitempty"`
}

// LocalTransportStatsResponse is the wire shape returned by
// LocalTransportStats. Keep this stable independent of the bbolt
// schema — additive fields only on changes.
type LocalTransportStatsResponse struct {
	// Transports is the per-transport rollup, sorted by total
	// bytes (sent+recv) descending so the busiest transports
	// come first.
	Transports []*stats.TransportRecord `json:"transports"`
	// FetchedAt is when the snapshot was assembled. Lets the
	// hvui surface a "last sample" timestamp.
	FetchedAt time.Time `json:"fetched_at"`
}

// LocalUptimeResponse is the wire shape for LocalUptimeStats.
//
// The tier name is whatever the tracker recorded (the visor uses
// "process", "dmsg", "skynet"; future probes may add more — keep
// the renderer name-agnostic). Dates are UTC YYYY-MM-DD; bitmaps
// are 288 chars where '.' = online slot and ' ' = offline slot.
type LocalUptimeResponse struct {
	Tiers     map[string]map[string]string `json:"tiers"`
	Since     time.Time                    `json:"since"`
	Until     time.Time                    `json:"until"`
	FetchedAt time.Time                    `json:"fetched_at"`
}

// LocalUptimeArgs is the request shape. Empty Since/Until mean
// "default 7-day window ending now" — caller-side defaults match
// the logserver's own /stats/uptime behavior so a hypervisor and a
// direct curl agree on the rendered timeline.
type LocalUptimeArgs struct {
	Since time.Time `json:"since,omitempty"`
	Until time.Time `json:"until,omitempty"`
}

// BandwidthTestConfig contains parameters for bandwidth testing
type BandwidthTestConfig struct {
	PK         cipher.PubKey
	Duration   time.Duration // How long to run the test
	PacketSize int           // Size of each packet in KB
	LocalRoute bool          // Use local route calculation
}

// BandwidthResult contains the results of a bandwidth test
type BandwidthResult struct {
	BytesSent     uint64        `json:"bytes_sent"`
	BytesReceived uint64        `json:"bytes_received"`
	Duration      time.Duration `json:"duration"`
	UploadSpeed   float64       `json:"upload_speed_kbps"`   // KB/s
	DownloadSpeed float64       `json:"download_speed_kbps"` // KB/s
}
