// Package cligotop cmd/skywire-cli/commands/gotop/grpcdevice.go
package cligotop

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/xxxserxxx/gotop/v4/devices"

	"github.com/skycoin/skywire/pkg/visor/rpcgrpc"
)

// grpcDevice is a gotop device extension that fetches stats from a remote visor via gRPC.
// It registers with gotop's device system and provides remote data using the same
// device names as local gopsutil devices, so the data overwrites local values.
type grpcDevice struct {
	client *rpcgrpc.PingClient
	ctx    context.Context
	cancel context.CancelFunc

	mu    sync.RWMutex
	stats *rpcgrpc.SystemStats

	// Track if initial data has been received
	initialized chan struct{}
}

var globalGRPCDevice *grpcDevice

// SetupGRPCDevice initializes the gRPC device extension.
// The device functions use the same names as local gopsutil devices (e.g., "CPU0", "Main")
// so that remote data overwrites local data in gotop's widget maps.
func SetupGRPCDevice(client *rpcgrpc.PingClient, _ string, updateInterval time.Duration, includeProcs bool, procLimit int32) error {
	ctx, cancel := context.WithCancel(context.Background())

	globalGRPCDevice = &grpcDevice{
		client:      client,
		ctx:         ctx,
		cancel:      cancel,
		initialized: make(chan struct{}),
	}

	// Register device functions with gotop
	// These are registered AFTER the local gopsutil devices (which register in init()),
	// so they run last and their values overwrite the local values in the map.
	devices.RegisterCPU(globalGRPCDevice.updateCPU)
	devices.RegisterMem(globalGRPCDevice.updateMem)
	devices.RegisterTemp(globalGRPCDevice.updateTemp)
	devices.RegisterShutdown(globalGRPCDevice.shutdown)

	// Start background streaming
	go globalGRPCDevice.streamStats(updateInterval, includeProcs, procLimit)

	// Wait for initial data (with timeout)
	select {
	case <-globalGRPCDevice.initialized:
		return nil
	case <-time.After(10 * time.Second):
		cancel()
		return nil // Proceed anyway, we'll show empty data
	}
}

// ShutdownGRPCDevice shuts down the gRPC device and connection.
func ShutdownGRPCDevice() {
	if globalGRPCDevice != nil {
		globalGRPCDevice.cancel()
	}
}

// SetupRemoteGRPCDevice initializes a gRPC device that fetches stats from a remote visor via DMSG.
// The local visor proxies the connection to the remote visor.
func SetupRemoteGRPCDevice(client *rpcgrpc.PingClient, remotePK string, updateInterval time.Duration, includeProcs bool, procLimit int32) error {
	ctx, cancel := context.WithCancel(context.Background())

	globalGRPCDevice = &grpcDevice{
		client:      client,
		ctx:         ctx,
		cancel:      cancel,
		initialized: make(chan struct{}),
	}

	// Register device functions with gotop
	devices.RegisterCPU(globalGRPCDevice.updateCPU)
	devices.RegisterMem(globalGRPCDevice.updateMem)
	devices.RegisterTemp(globalGRPCDevice.updateTemp)
	devices.RegisterShutdown(globalGRPCDevice.shutdown)

	// Start background streaming from remote visor
	go globalGRPCDevice.streamRemoteStats(remotePK, updateInterval, includeProcs, procLimit)

	// Wait for initial data (with timeout)
	select {
	case <-globalGRPCDevice.initialized:
		return nil
	case <-time.After(30 * time.Second):
		cancel()
		return fmt.Errorf("timeout waiting for remote visor stats")
	}
}

func (g *grpcDevice) streamRemoteStats(remotePK string, updateInterval time.Duration, includeProcs bool, procLimit int32) {
	initialized := false
	err := g.client.StreamRemoteSystemStats(g.ctx, remotePK, updateInterval, includeProcs, procLimit, func(stats *rpcgrpc.SystemStats) {
		g.mu.Lock()
		g.stats = stats
		g.mu.Unlock()

		if !initialized {
			initialized = true
			close(g.initialized)
		}
	})
	if err != nil && g.ctx.Err() == nil {
		// Stream ended unexpectedly
		_ = err
	}
}

func (g *grpcDevice) streamStats(updateInterval time.Duration, includeProcs bool, procLimit int32) {
	initialized := false
	err := g.client.StreamSystemStats(g.ctx, updateInterval, includeProcs, procLimit, func(stats *rpcgrpc.SystemStats) {
		g.mu.Lock()
		g.stats = stats
		g.mu.Unlock()

		if !initialized {
			initialized = true
			close(g.initialized)
		}
	})
	if err != nil && g.ctx.Err() == nil {
		// Stream ended unexpectedly, could retry here
		_ = err
	}
}

func (g *grpcDevice) shutdown() error {
	g.cancel()
	return g.client.Close()
}

func (g *grpcDevice) updateCPU(cpus map[string]int, _ bool) map[string]error {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if g.stats == nil {
		return nil
	}

	// Use the same naming format as local gopsutil device: "CPU0", "CPU1", etc.
	// This matches cpu_cpu.go's format string "CPU%1d" or "CPU%02d"
	cpuCount := len(g.stats.Cpu)
	formatString := "CPU%d"
	if cpuCount > 10 {
		formatString = "CPU%02d"
	}

	for i, cpu := range g.stats.Cpu {
		key := fmt.Sprintf(formatString, i)
		v := cpu.UsagePercent
		if v > 100 {
			v = 100
		}
		cpus[key] = int(v)
	}

	return nil
}

func (g *grpcDevice) updateMem(mems map[string]devices.MemoryInfo) map[string]error {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if g.stats == nil {
		return nil
	}

	// Use the same naming as local gopsutil device: "Main" and "Swap"
	// This matches mem_mem.go and mem_swap_other.go
	if g.stats.Memory != nil {
		mems["Main"] = devices.MemoryInfo{
			Total:       g.stats.Memory.Total,
			Used:        g.stats.Memory.Used,
			UsedPercent: g.stats.Memory.UsedPercent,
		}
	}

	// Add swap
	if g.stats.Swap != nil && g.stats.Swap.Total > 0 {
		mems["Swap"] = devices.MemoryInfo{
			Total:       g.stats.Swap.Total,
			Used:        g.stats.Swap.Used,
			UsedPercent: g.stats.Swap.UsedPercent,
		}
	}

	return nil
}

func (g *grpcDevice) updateTemp(temps map[string]int) map[string]error {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if g.stats == nil {
		return nil
	}

	// Use the sensor keys directly from the remote system
	// These won't match local sensor names (different hardware),
	// but that's expected - we're showing remote temps, not local ones
	for _, t := range g.stats.Temps {
		temps[t.SensorKey] = int(t.Temperature)
	}

	return nil
}
