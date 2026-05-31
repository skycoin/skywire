//go:build !withoutgotop

// Package cligotop grpcdevice_integration_test.go: integration tests that
// drive the gRPC device extension end-to-end against a real (localhost)
// gRPC server implementing the system-stats streams. This covers the
// Setup*/stream*/shutdown paths that the pure unit tests can't reach
// without a live PingClient stream.
//
// The TUI paths (eventLoop / runDirectGotop / runGotopWithConfig) are
// deliberately NOT exercised here: they call termui's ui.Init(), which
// requires a real terminal and is not testable in a headless CI run.
package cligotop

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/skycoin/skywire/third_party/xxxserxxx/gotop/v4/devices"
	"google.golang.org/grpc"

	"github.com/skycoin/skywire/pkg/visor/rpcgrpc"
)

// fakeStatsServer is a minimal PingService server that streams a fixed set
// of SystemStats snapshots and then closes the stream.
type fakeStatsServer struct {
	rpcgrpc.UnimplementedPingServiceServer
	stats []*rpcgrpc.SystemStats
}

func (s *fakeStatsServer) StreamSystemStats(_ *rpcgrpc.SystemStatsRequest, stream grpc.ServerStreamingServer[rpcgrpc.SystemStats]) error {
	for _, st := range s.stats {
		if err := stream.Send(st); err != nil {
			return err
		}
	}
	return nil
}

func (s *fakeStatsServer) StreamRemoteSystemStats(_ *rpcgrpc.RemoteSystemStatsRequest, stream grpc.ServerStreamingServer[rpcgrpc.SystemStats]) error {
	for _, st := range s.stats {
		if err := stream.Send(st); err != nil {
			return err
		}
	}
	return nil
}

// startFakeServer spins up the fake PingService on a localhost port and
// returns a connected PingClient. Server + client are torn down via t.Cleanup.
func startFakeServer(t *testing.T, stats []*rpcgrpc.SystemStats) *rpcgrpc.PingClient {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	srv := grpc.NewServer()
	rpcgrpc.RegisterPingServiceServer(srv, &fakeStatsServer{stats: stats})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	client, err := rpcgrpc.NewPingClient(lis.Addr().String())
	require.NoError(t, err)
	return client
}

func sampleStats() []*rpcgrpc.SystemStats {
	return []*rpcgrpc.SystemStats{
		{
			Cpu: []*rpcgrpc.CpuStat{
				{Cpu: "CPU0", UsagePercent: 25},
				{Cpu: "CPU1", UsagePercent: 80},
			},
			Memory: &rpcgrpc.MemoryStat{Total: 100, Used: 60, UsedPercent: 60},
			Swap:   &rpcgrpc.MemoryStat{Total: 50, Used: 5, UsedPercent: 10},
			Temps: []*rpcgrpc.TempStat{
				{SensorKey: "coretemp", Temperature: 48.0},
			},
		},
	}
}

func TestSetupGRPCDevice_EndToEnd(t *testing.T) {
	t.Cleanup(func() { globalGRPCDevice = nil })

	client := startFakeServer(t, sampleStats())

	// Drives SetupGRPCDevice -> streamStats -> the initialized handshake.
	err := SetupGRPCDevice(client, "", 50*time.Millisecond, true, 5)
	require.NoError(t, err)
	require.NotNil(t, globalGRPCDevice)

	// The streamed snapshot must have been stored, so the device update
	// callbacks (registered with gotop) see real data.
	require.Eventually(t, func() bool {
		globalGRPCDevice.mu.RLock()
		defer globalGRPCDevice.mu.RUnlock()
		return globalGRPCDevice.stats != nil
	}, 5*time.Second, 10*time.Millisecond)

	cpus := map[string]int{}
	require.Nil(t, globalGRPCDevice.updateCPU(cpus, false))
	require.Equal(t, 25, cpus["CPU0"])
	require.Equal(t, 80, cpus["CPU1"])

	// ShutdownGRPCDevice cancels the device context (idempotent with the
	// stream having already ended).
	ShutdownGRPCDevice()

	// shutdown() closes the underlying client connection.
	require.NoError(t, globalGRPCDevice.shutdown())
}

func TestSetupRemoteGRPCDevice_EndToEnd(t *testing.T) {
	t.Cleanup(func() { globalGRPCDevice = nil })

	client := startFakeServer(t, sampleStats())

	err := SetupRemoteGRPCDevice(client, "remote-pk", 50*time.Millisecond, false, 0)
	require.NoError(t, err)
	require.NotNil(t, globalGRPCDevice)

	require.Eventually(t, func() bool {
		globalGRPCDevice.mu.RLock()
		defer globalGRPCDevice.mu.RUnlock()
		return globalGRPCDevice.stats != nil
	}, 5*time.Second, 10*time.Millisecond)

	// Memory + temperature update callbacks see the streamed snapshot.
	mems := map[string]devices.MemoryInfo{}
	require.Nil(t, globalGRPCDevice.updateMem(mems))
	require.Equal(t, uint64(100), mems["Main"].Total)
	require.Equal(t, uint64(50), mems["Swap"].Total)

	temps := map[string]int{}
	require.Nil(t, globalGRPCDevice.updateTemp(temps))
	require.Equal(t, 48, temps["coretemp"])

	ShutdownGRPCDevice()
	require.NoError(t, globalGRPCDevice.shutdown())
}
