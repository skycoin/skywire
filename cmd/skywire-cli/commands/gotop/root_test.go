//go:build !withoutgotop

// Package cligotop root_test.go: unit tests for the gotop CLI's pure
// formatting helpers, the in-memory log capture, the text-mode stats
// renderer, and the gRPC device data-transform methods.
package cligotop

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/skycoin/skywire/third_party/xxxserxxx/gotop/v4"
	"github.com/skycoin/skywire/third_party/xxxserxxx/gotop/v4/devices"

	"github.com/skycoin/skywire/pkg/visor/rpcgrpc"
)

func TestFormatBytes(t *testing.T) {
	cases := []struct {
		in   uint64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{1024 * 1024 * 1024, "1.0 GB"},
		{1024 * 1024 * 1024 * 1024, "1.0 TB"},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, formatBytes(tc.in), "formatBytes(%d)", tc.in)
	}
}

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{30 * time.Minute, "30m"},
		{90 * time.Minute, "1h 30m"},
		{25*time.Hour + 30*time.Minute, "1d 1h 30m"},
		{0, "0m"},
		{48 * time.Hour, "2d 0h 0m"},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, formatDuration(tc.in), "formatDuration(%v)", tc.in)
	}
}

func TestTruncate(t *testing.T) {
	require.Equal(t, "hello", truncate("hello", 10)) // shorter than max
	require.Equal(t, "hello", truncate("hello", 5))  // exactly max
	require.Equal(t, "hell.", truncate("hello world", 5))
	require.Equal(t, "a.", truncate("abcdef", 2))
}

func TestLogCapture(t *testing.T) {
	t.Run("small write round-trips without marker", func(t *testing.T) {
		c := &logCapture{}
		n, err := c.Write([]byte("hello\n"))
		require.NoError(t, err)
		require.Equal(t, 6, n)
		require.Equal(t, "hello\n", c.String())
		require.NotContains(t, c.String(), "truncated")
	})

	t.Run("partial write past cap sets truncated", func(t *testing.T) {
		c := &logCapture{}
		// Fill to 10 bytes short of the cap.
		head := bytes.Repeat([]byte("x"), logCaptureMaxBytes-10)
		n, err := c.Write(head)
		require.NoError(t, err)
		require.Equal(t, len(head), n)

		// Next write exceeds the remaining room: only 10 bytes are kept,
		// the full length is still reported, and truncated flips on.
		n, err = c.Write(bytes.Repeat([]byte("y"), 100))
		require.NoError(t, err)
		require.Equal(t, 100, n)

		s := c.String()
		require.Contains(t, s, "[gotop log buffer truncated at 256KiB]")

		// A further write when there's no room at all is dropped but still
		// reports its length.
		n, err = c.Write([]byte("zzz"))
		require.NoError(t, err)
		require.Equal(t, 3, n)
	})
}

// captureStdout runs fn with os.Stdout redirected to a pipe and returns
// everything written.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()
	_ = w.Close()
	os.Stdout = orig
	return <-done
}

func TestDisplayStatsText_Error(t *testing.T) {
	out := captureStdout(t, func() {
		displayStatsText(&rpcgrpc.SystemStats{Error: "collection failed"})
	})
	require.Contains(t, out, "Error: collection failed")
}

func TestDisplayStatsText_Full(t *testing.T) {
	stats := &rpcgrpc.SystemStats{
		Host: &rpcgrpc.HostInfo{
			Hostname:        "node1",
			Platform:        "linux",
			PlatformVersion: "12",
			KernelVersion:   "6.1",
			KernelArch:      "x86_64",
			UptimeSec:       int64((25*time.Hour + 30*time.Minute) / time.Second),
			NumCpus:         4,
		},
		CpuAverage: 42.5,
		Cpu: []*rpcgrpc.CpuStat{
			{Cpu: "CPU0", UsagePercent: 10},
			{Cpu: "CPU1", UsagePercent: 75},
		},
		Memory:  &rpcgrpc.MemoryStat{Total: 16 * 1024 * 1024 * 1024, Used: 8 * 1024 * 1024 * 1024, UsedPercent: 50},
		Swap:    &rpcgrpc.MemoryStat{Total: 2 * 1024 * 1024 * 1024, Used: 1024 * 1024 * 1024, UsedPercent: 50},
		Network: &rpcgrpc.NetworkStat{BytesSent: 1024, BytesRecv: 2048, BytesSentRate: 100, BytesRecvRate: 200},
		Disks: []*rpcgrpc.DiskStat{
			{Mountpoint: "/", Total: 100 * 1024 * 1024 * 1024, Used: 40 * 1024 * 1024 * 1024, UsedPercent: 40},
			{Mountpoint: "/empty", Total: 0}, // skipped (Total == 0)
		},
		Temps: []*rpcgrpc.TempStat{
			{SensorKey: "coretemp", Temperature: 55.5},
		},
		Processes: []*rpcgrpc.ProcessStat{
			{Pid: 1, Name: "init", CpuPercent: 1, MemoryPercent: 2, MemoryRss: 1024, Username: "root"},
			{Pid: 2, Name: "averylongprocessnamethatislong", CpuPercent: 99, MemoryPercent: 5, MemoryRss: 2048, Username: "verylongusername"},
		},
	}

	out := captureStdout(t, func() { displayStatsText(stats) })

	require.Contains(t, out, "Host: node1 (linux 12)")
	require.Contains(t, out, "Uptime: 1d 1h 30m | CPUs: 4")
	require.Contains(t, out, "CPU Average: 42.5%")
	require.Contains(t, out, "CPU1: 75.0%")
	require.Contains(t, out, "Memory: 8.0 GB / 16.0 GB (50.0%)")
	require.Contains(t, out, "Swap:")
	require.Contains(t, out, "Network: TX 1.0 KB")
	require.Contains(t, out, "Disks:")
	require.Contains(t, out, "/  ")       // root disk renders
	require.NotContains(t, out, "/empty") // zero-total disk is filtered out entirely
	require.Contains(t, out, "Temperatures:")
	require.Contains(t, out, "coretemp")
	require.Contains(t, out, "Top Processes:")
	// Sorted by CPU desc -> pid 2 (99%) appears before pid 1 (1%).
	require.Less(t, strings.Index(out, "averylongprocessname"), strings.Index(out, "init"))
	// Long name truncated to 20 chars (19 + ".").
	require.Contains(t, out, "averylongprocessnam.")
}

func TestGetLayout(t *testing.T) {
	for _, layout := range []string{"default", "minimal", "battery", "procs", "kitchensink", "remote", "unknown-falls-back"} {
		r := getLayout(gotop.Config{Layout: layout})
		require.NotNil(t, r, layout)
		data, err := io.ReadAll(r)
		require.NoError(t, err, layout)
		require.NotEmpty(t, data, layout)
	}
}

func TestSetDefaultTermuiColors(t *testing.T) {
	// Just needs to apply the colorscheme to the global termui theme
	// without panicking.
	require.NotPanics(t, func() {
		setDefaultTermuiColors(gotop.Config{})
	})
}

// ---- grpcDevice update methods --------------------------------------------

func TestGrpcDevice_UpdateCPU(t *testing.T) {
	t.Run("nil stats is a no-op", func(t *testing.T) {
		g := &grpcDevice{}
		cpus := map[string]int{}
		require.Nil(t, g.updateCPU(cpus, false))
		require.Empty(t, cpus)
	})

	t.Run("populates and clamps", func(t *testing.T) {
		g := &grpcDevice{stats: &rpcgrpc.SystemStats{
			Cpu: []*rpcgrpc.CpuStat{
				{UsagePercent: 50},
				{UsagePercent: 150}, // clamped to 100
			},
		}}
		cpus := map[string]int{}
		require.Nil(t, g.updateCPU(cpus, false))
		require.Equal(t, 50, cpus["CPU0"])
		require.Equal(t, 100, cpus["CPU1"])
	})

	t.Run("wide format for >10 cpus", func(t *testing.T) {
		cpu := make([]*rpcgrpc.CpuStat, 11)
		for i := range cpu {
			cpu[i] = &rpcgrpc.CpuStat{UsagePercent: 1}
		}
		g := &grpcDevice{stats: &rpcgrpc.SystemStats{Cpu: cpu}}
		cpus := map[string]int{}
		g.updateCPU(cpus, false) //nolint:errcheck
		_, ok := cpus["CPU00"]
		require.True(t, ok, "expected zero-padded keys for >10 cpus")
		require.Len(t, cpus, 11)
	})
}

func TestGrpcDevice_UpdateMem(t *testing.T) {
	t.Run("nil stats is a no-op", func(t *testing.T) {
		g := &grpcDevice{}
		mems := map[string]devices.MemoryInfo{}
		require.Nil(t, g.updateMem(mems))
		require.Empty(t, mems)
	})

	t.Run("main and swap", func(t *testing.T) {
		g := &grpcDevice{stats: &rpcgrpc.SystemStats{
			Memory: &rpcgrpc.MemoryStat{Total: 100, Used: 40, UsedPercent: 40},
			Swap:   &rpcgrpc.MemoryStat{Total: 50, Used: 10, UsedPercent: 20},
		}}
		mems := map[string]devices.MemoryInfo{}
		g.updateMem(mems) //nolint:errcheck
		require.Equal(t, devices.MemoryInfo{Total: 100, Used: 40, UsedPercent: 40}, mems["Main"])
		require.Equal(t, devices.MemoryInfo{Total: 50, Used: 10, UsedPercent: 20}, mems["Swap"])
	})

	t.Run("zero-total swap omitted", func(t *testing.T) {
		g := &grpcDevice{stats: &rpcgrpc.SystemStats{
			Memory: &rpcgrpc.MemoryStat{Total: 100, Used: 40},
			Swap:   &rpcgrpc.MemoryStat{Total: 0},
		}}
		mems := map[string]devices.MemoryInfo{}
		g.updateMem(mems) //nolint:errcheck
		_, hasSwap := mems["Swap"]
		require.False(t, hasSwap)
		_, hasMain := mems["Main"]
		require.True(t, hasMain)
	})
}

func TestGrpcDevice_UpdateTemp(t *testing.T) {
	t.Run("nil stats is a no-op", func(t *testing.T) {
		g := &grpcDevice{}
		temps := map[string]int{}
		require.Nil(t, g.updateTemp(temps))
		require.Empty(t, temps)
	})

	t.Run("populates from sensor keys", func(t *testing.T) {
		g := &grpcDevice{stats: &rpcgrpc.SystemStats{
			Temps: []*rpcgrpc.TempStat{
				{SensorKey: "coretemp", Temperature: 55.9},
				{SensorKey: "gpu", Temperature: 70.1},
			},
		}}
		temps := map[string]int{}
		g.updateTemp(temps)                     //nolint:errcheck
		require.Equal(t, 55, temps["coretemp"]) // truncated to int
		require.Equal(t, 70, temps["gpu"])
	})
}
