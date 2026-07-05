// Package rpcgrpc — pkg/visor/rpcgrpc/stream_handlers_grpc_test.go:
// end-to-end coverage of the two heaviest streaming RPCs, StreamPingTree
// and StreamMuxBandwidth, driven over the loopback gRPC harness. Both
// spawn internal sender/sampler/probe goroutines and a bounded event
// channel; the tests use short durations and small graphs so a full run
// completes in well under a second while still walking every phase.
package rpcgrpc

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

// tpEntry builds a transport entry linking two public keys.
func tpEntry(a, b cipher.PubKey) *transport.Entry {
	return &transport.Entry{ID: uuid.New(), Edges: [2]cipher.PubKey{a, b}, Type: tptypes.STCPR}
}

// --- StreamPingTree --------------------------------------------------

func TestStreamPingTreeLivePing(t *testing.T) {
	fv := newFakeVisor()
	peer, _ := cipher.GenerateKeyPair()
	fv.fetchAllTransports = func(context.Context) ([]*transport.Entry, error) {
		return []*transport.Entry{tpEntry(fv.localPK, peer)}, nil
	}
	client, cleanup := newTestServer(t, fv)
	defer cleanup()

	stream, err := client.StreamPingTree(context.Background(), &PingTreeRequest{MaxLevel: 1, Tries: 1})
	if err != nil {
		t.Fatalf("StreamPingTree: %v", err)
	}

	var discovered, pingResults, runDone int
	for {
		ev, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		switch ev.Payload.(type) {
		case *PingTreeEvent_Discovered:
			discovered++
		case *PingTreeEvent_PingResult:
			pingResults++
		case *PingTreeEvent_RunDone:
			runDone++
		}
	}
	if discovered != 1 {
		t.Errorf("discovered = %d, want 1", discovered)
	}
	if pingResults != 1 {
		t.Errorf("pingResults = %d, want 1", pingResults)
	}
	if runDone != 1 {
		t.Errorf("runDone = %d, want 1", runDone)
	}
}

func TestStreamPingTreeTransportLatencyFastPath(t *testing.T) {
	fv := newFakeVisor()
	peer, _ := cipher.GenerateKeyPair()
	fv.fetchAllTransports = func(context.Context) ([]*transport.Entry, error) {
		return []*transport.Entry{tpEntry(fv.localPK, peer)}, nil
	}
	// Non-zero smoothed RTT → fast path emits a transport_summary result
	// without a live ping.
	fv.transportLatency = func(cipher.PubKey) float64 { return 12.5 }
	client, cleanup := newTestServer(t, fv)
	defer cleanup()

	stream, err := client.StreamPingTree(context.Background(), &PingTreeRequest{
		MaxLevel:            1,
		Tries:               1,
		UseTransportLatency: true,
	})
	if err != nil {
		t.Fatalf("StreamPingTree: %v", err)
	}

	var source string
	for {
		ev, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		if pr, ok := ev.Payload.(*PingTreeEvent_PingResult); ok {
			source = pr.PingResult.LatencySource
		}
	}
	if source != "transport_summary" {
		t.Errorf("LatencySource = %q, want transport_summary (fast path)", source)
	}
}

func TestStreamPingTreeDryRun(t *testing.T) {
	fv := newFakeVisor()
	peer, _ := cipher.GenerateKeyPair()
	fv.fetchAllTransports = func(context.Context) ([]*transport.Entry, error) {
		return []*transport.Entry{tpEntry(fv.localPK, peer)}, nil
	}
	client, cleanup := newTestServer(t, fv)
	defer cleanup()

	stream, err := client.StreamPingTree(context.Background(), &PingTreeRequest{MaxLevel: 1, DryRun: true})
	if err != nil {
		t.Fatalf("StreamPingTree: %v", err)
	}
	var skipped bool
	for {
		ev, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		if pr, ok := ev.Payload.(*PingTreeEvent_PingResult); ok && pr.PingResult.LatencySource == "skipped" {
			skipped = true
		}
	}
	if !skipped {
		t.Error("expected a skipped PingResult in dry-run mode")
	}
}

func TestStreamPingTreeNoNeighbors(t *testing.T) {
	// Default fake returns no transport entries → empty adjacency.
	// MaxLevel unset (0 = unlimited) so the BFS enters the level loop
	// and terminates on the empty frontier rather than the level cap.
	client, cleanup := newTestServer(t, newFakeVisor())
	defer cleanup()
	stream, err := client.StreamPingTree(context.Background(), &PingTreeRequest{})
	if err != nil {
		t.Fatalf("StreamPingTree: %v", err)
	}
	var reason string
	for {
		ev, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		if rd, ok := ev.Payload.(*PingTreeEvent_RunDone); ok {
			reason = rd.RunDone.TerminationReason
		}
	}
	if reason != "no_neighbors" {
		t.Errorf("termination reason = %q, want no_neighbors", reason)
	}
}

func TestStreamPingTreeFetchError(t *testing.T) {
	fv := newFakeVisor()
	fv.fetchAllTransports = func(context.Context) ([]*transport.Entry, error) {
		return nil, io.ErrUnexpectedEOF
	}
	client, cleanup := newTestServer(t, fv)
	defer cleanup()
	stream, err := client.StreamPingTree(context.Background(), &PingTreeRequest{})
	if err != nil {
		t.Fatalf("StreamPingTree: %v", err)
	}
	var gotServerError bool
	for {
		ev, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		if se, ok := ev.Payload.(*PingTreeEvent_ServerError); ok {
			if se.ServerError.Code != "tpd_fetch_failed" {
				t.Errorf("server error code = %q, want tpd_fetch_failed", se.ServerError.Code)
			}
			gotServerError = true
		}
	}
	if !gotServerError {
		t.Error("expected a ServerError event")
	}
}

// --- StreamMuxBandwidth ----------------------------------------------

func TestStreamMuxBandwidthInvalidPK(t *testing.T) {
	client, cleanup := newTestServer(t, newFakeVisor())
	defer cleanup()
	stream, err := client.StreamMuxBandwidth(context.Background(), &MuxBandwidthRequest{TargetPk: "bad"})
	if err != nil {
		t.Fatalf("StreamMuxBandwidth: %v", err)
	}
	var gotErr bool
	for {
		ev, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		if me, ok := ev.Payload.(*MuxBandwidthEvent_Error); ok {
			if me.Error.Code != "invalid_request" {
				t.Errorf("error code = %q, want invalid_request", me.Error.Code)
			}
			gotErr = true
		}
	}
	if !gotErr {
		t.Error("expected an invalid_request error event")
	}
}

func TestStreamMuxBandwidthRun(t *testing.T) {
	fv := newFakeVisor()
	client, cleanup := newTestServer(t, fv)
	defer cleanup()

	// Short run WITHOUT probing so the setup, pump, sampler, and sender
	// goroutines all execute fast (probing forces a fixed 5s warmup).
	// 250ms crosses the 50ms sample ticker several times.
	stream, err := client.StreamMuxBandwidth(context.Background(), &MuxBandwidthRequest{
		TargetPk:         validPK(t),
		Routes:           1,
		DurationNs:       (250 * time.Millisecond).Nanoseconds(),
		PacketSizeKb:     16,
		SampleIntervalNs: (50 * time.Millisecond).Nanoseconds(),
	})
	if err != nil {
		t.Fatalf("StreamMuxBandwidth: %v", err)
	}

	var samples, established, done int
	for {
		ev, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		switch ev.Payload.(type) {
		case *MuxBandwidthEvent_RouteEstablished:
			established++
		case *MuxBandwidthEvent_Sample:
			samples++
		case *MuxBandwidthEvent_Done:
			done++
		}
	}
	if established != 1 {
		t.Errorf("route-established events = %d, want 1", established)
	}
	if samples == 0 {
		t.Error("expected at least one bandwidth sample")
	}
	if done != 1 {
		t.Errorf("done events = %d, want 1", done)
	}
}

// TestStreamMuxBandwidthWithProbe covers the RTT-probe path (probe
// route setup, warm-up, and the probe loop). Enabling ProbeRtt forces a
// fixed ~5s warm-up phase, so this is intentionally the one slow case.
func TestStreamMuxBandwidthWithProbe(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 5s RTT-probe warm-up run in -short mode")
	}
	fv := newFakeVisor()
	client, cleanup := newTestServer(t, fv)
	defer cleanup()

	stream, err := client.StreamMuxBandwidth(context.Background(), &MuxBandwidthRequest{
		TargetPk:         validPK(t),
		Routes:           1,
		DurationNs:       (100 * time.Millisecond).Nanoseconds(),
		PacketSizeKb:     8,
		SampleIntervalNs: (40 * time.Millisecond).Nanoseconds(),
		ProbeRtt:         true,
		ProbeIntervalNs:  (40 * time.Millisecond).Nanoseconds(),
	})
	if err != nil {
		t.Fatalf("StreamMuxBandwidth: %v", err)
	}

	var probes, done int
	for {
		ev, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		switch ev.Payload.(type) {
		case *MuxBandwidthEvent_RttProbe:
			probes++
		case *MuxBandwidthEvent_Done:
			done++
		}
	}
	if probes == 0 {
		t.Error("expected at least one RTT probe event")
	}
	if done != 1 {
		t.Errorf("done events = %d, want 1", done)
	}
}
