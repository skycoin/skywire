// Package ping — cmd/skywire-cli/commands/visor/ping/mux_bandwidth.go:
// CLI consumer for the StreamMuxBandwidth gRPC RPC.
//
// Operator-visible: `cli visor ping mux-bw <pk>` runs the
// multiplexed-route bandwidth test against a single peer and emits
// per-second NDJSON samples to stdout. Pairs with the same
// treeprobe-friendly envelope shape as `tree-stream` so harness
// consumers don't need a separate parser for this RPC.
//
// Run shapes (the operator's hypothesis matrix):
//
//	# Baseline — single route, no min_hops (picks direct if available)
//	cli visor ping mux-bw <pk> --duration 30s
//
//	# Single non-direct path — forces at least one intermediate
//	cli visor ping mux-bw <pk> --duration 30s --min-hops 2
//
//	# N parallel routes excluding direct — the operator's hypothesis
//	# call: should sum to MORE bandwidth than direct
//	cli visor ping mux-bw <pk> --duration 30s --routes 4 --min-hops 2
//
//	# Queueing-delay measurement: bandwidth + concurrent RTT probe
//	cli visor ping mux-bw <pk> --duration 30s --probe-rtt
package ping

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/visor/rpcgrpc"
)

var (
	muxBwRoutes         int
	muxBwDuration       time.Duration
	muxBwPacketSizeKb   int
	muxBwMinHops        int
	muxBwSetupTimeout   time.Duration
	muxBwProbeRTT       bool
	muxBwProbeInterval  time.Duration
	muxBwSampleInterval time.Duration
	muxBwLocalRoute     bool
)

func init() {
	muxBandwidthCmd.Flags().IntVarP(&muxBwRoutes, "routes", "r", 1,
		"number of parallel route connections (1 = baseline single route)")
	muxBandwidthCmd.Flags().DurationVarP(&muxBwDuration, "duration", "d", 30*time.Second,
		"how long to pump bytes (excludes route-setup time)")
	muxBandwidthCmd.Flags().IntVarP(&muxBwPacketSizeKb, "size", "s", 32,
		"per-write block size in KB")
	muxBandwidthCmd.Flags().IntVar(&muxBwMinHops, "min-hops", 0,
		"route-finder min hops constraint (>=2 excludes the direct transport)")
	muxBandwidthCmd.Flags().DurationVar(&muxBwSetupTimeout, "setup-timeout", 30*time.Second,
		"per-route setup timeout")
	muxBandwidthCmd.Flags().BoolVar(&muxBwProbeRTT, "probe-rtt", false,
		"also send small-packet RTT probes during the load (queueing-delay measurement)")
	muxBandwidthCmd.Flags().DurationVar(&muxBwProbeInterval, "probe-interval", 100*time.Millisecond,
		"interval between RTT probes when --probe-rtt is set")
	muxBandwidthCmd.Flags().DurationVar(&muxBwSampleInterval, "sample-interval", 1*time.Second,
		"interval between MuxBandwidthSample events")
	muxBandwidthCmd.Flags().BoolVar(&muxBwLocalRoute, "local-route", false,
		"use locally-cached TPD data for route calculation (faster setup; may be stale)")

	RootCmd.AddCommand(muxBandwidthCmd)
}

var muxBandwidthCmd = &cobra.Command{
	Use:   "mux-bw <pk>",
	Short: "Multiplexed-route bandwidth + queueing-delay probe to a peer",
	Long: `Pump bytes from this visor to a peer visor across N parallel
routes, optionally probing RTT during the load to capture queueing
delay. Emits per-second NDJSON samples to stdout, plus a terminal
summary event.

The "N parallel routes" can be one of three shapes via flags:

  --routes 1                        # single route, route-finder picks freely (will use direct if available)
  --routes 1 --min-hops 2           # single non-direct path — forces an intermediate
  --routes N --min-hops 2           # N parallel routes excluding direct
                                    #   — the operator's "mux should sum to more bandwidth" hypothesis

Pair with --probe-rtt to capture the loaded-RTT distribution
concurrent with the bulk pump. The loaded RTT minus the unloaded
baseline IS the queueing delay.

Output: NDJSON envelopes, one per stdout line:

  {"ts":"RFC3339Nano","type":"route_established|sample|rtt_probe|done|error","data":{...}}

Examples:

  # Baseline single direct route, 30s, with RTT probing:
  skywire cli visor ping mux-bw <peer-pk> --probe-rtt

  # Mux 4 routes excluding direct, 60s, with RTT probing:
  skywire cli visor ping mux-bw <peer-pk> --routes 4 --min-hops 2 --duration 60s --probe-rtt

  # Filter to just the sample events with jq:
  skywire cli visor ping mux-bw <peer-pk> --routes 4 \
    | jq -c 'select(.type=="sample") | {t: (.data.elapsed_ns|tonumber/1e9), send_mbps: (.data.instant_send_bps/1e6), recv_mbps: (.data.instant_recv_bps/1e6)}'`,
	Args: cobra.ExactArgs(1),
	Run:  runMuxBandwidth,
}

func runMuxBandwidth(cmd *cobra.Command, args []string) {
	targetPK := args[0]

	ctx, cancel := muxBwSignalContext()
	defer cancel()

	client, err := rpcgrpc.NewPingClient(clirpc.Addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mux-bw: gRPC client connect: %v\n", err)
		os.Exit(1)
	}
	defer client.Close() //nolint:errcheck

	req := &rpcgrpc.MuxBandwidthRequest{
		TargetPk:         targetPK,
		Routes:           int32(muxBwRoutes), //nolint:gosec
		DurationNs:       muxBwDuration.Nanoseconds(),
		PacketSizeKb:     int32(muxBwPacketSizeKb), //nolint:gosec
		MinHops:          int32(muxBwMinHops),      //nolint:gosec
		SetupTimeoutNs:   muxBwSetupTimeout.Nanoseconds(),
		ProbeRtt:         muxBwProbeRTT,
		ProbeIntervalNs:  muxBwProbeInterval.Nanoseconds(),
		SampleIntervalNs: muxBwSampleInterval.Nanoseconds(),
		LocalRoute:       muxBwLocalRoute,
	}

	stream, err := client.StreamMuxBandwidth(ctx, req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mux-bw: StreamMuxBandwidth call: %v\n", err)
		os.Exit(1)
	}

	enc := json.NewEncoder(os.Stdout)
	marshaler := protojson.MarshalOptions{
		UseProtoNames:   true,
		EmitUnpopulated: false,
	}

	for {
		ev, recvErr := stream.Recv()
		if recvErr != nil {
			if recvErr == io.EOF || recvErr == context.Canceled {
				return
			}
			fmt.Fprintf(os.Stderr, "mux-bw: stream recv: %v\n", recvErr)
			os.Exit(1)
		}
		if err := emitMuxBwOne(enc, marshaler, ev); err != nil {
			fmt.Fprintf(os.Stderr, "mux-bw: emit: %v\n", err)
			os.Exit(1)
		}
	}
}

func emitMuxBwOne(enc *json.Encoder, m protojson.MarshalOptions, ev *rpcgrpc.MuxBandwidthEvent) error {
	typ, payload := classifyMuxBwEvent(ev)
	rawData, err := m.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", typ, err)
	}
	envelope := struct {
		TS   string          `json:"ts"`
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}{
		TS:   time.Unix(0, ev.TimestampNs).UTC().Format(time.RFC3339Nano),
		Type: typ,
		Data: json.RawMessage(rawData),
	}
	return enc.Encode(envelope)
}

func classifyMuxBwEvent(ev *rpcgrpc.MuxBandwidthEvent) (string, proto.Message) {
	switch p := ev.Payload.(type) {
	case *rpcgrpc.MuxBandwidthEvent_RouteEstablished:
		return "route_established", p.RouteEstablished
	case *rpcgrpc.MuxBandwidthEvent_Sample:
		return "sample", p.Sample
	case *rpcgrpc.MuxBandwidthEvent_RttProbe:
		return "rtt_probe", p.RttProbe
	case *rpcgrpc.MuxBandwidthEvent_Done:
		return "done", p.Done
	case *rpcgrpc.MuxBandwidthEvent_Error:
		return "error", p.Error
	}
	return "unknown", &rpcgrpc.MuxBandwidthError{}
}

func muxBwSignalContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()
	return ctx, cancel
}
