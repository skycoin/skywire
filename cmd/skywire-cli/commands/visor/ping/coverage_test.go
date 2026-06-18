// Package ping — coverage_test.go: exercises the non-RPC surface of the
// ping subcommands: the human/NDJSON formatters, the per-run stats
// aggregators, the event classifiers, and the two Bubble Tea TUI models
// (mux-bandwidth and ping-tree) driven through Update/View/applyEvent
// with synthetic rpcgrpc events. The cobra RunE bodies and the gRPC
// stream consumers (which need a live visor) are not covered here.
package ping

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/skycoin/skywire/pkg/visor/rpcgrpc"
)

func timeZero() time.Time { return time.Now() }

// --- event builders --------------------------------------------------

func muxRouteEstablished(idx int32, failed bool) *rpcgrpc.MuxBandwidthEvent {
	return &rpcgrpc.MuxBandwidthEvent{TimestampNs: 1, Payload: &rpcgrpc.MuxBandwidthEvent_RouteEstablished{
		RouteEstablished: &rpcgrpc.MuxRouteEstablished{
			RouteIndex: idx, Failed: failed, SetupErr: "boom", SetupLatencyNs: 5_000_000,
			Hops: []*rpcgrpc.RouteHop{{From: "A", To: "B"}, {From: "B", To: "C"}},
		},
	}}
}

func muxSample() *rpcgrpc.MuxBandwidthEvent {
	return &rpcgrpc.MuxBandwidthEvent{TimestampNs: 2, Payload: &rpcgrpc.MuxBandwidthEvent_Sample{
		Sample: &rpcgrpc.MuxBandwidthSample{
			InstantSendBps: 2e6, InstantRecvBps: 3e9, BytesSent: 1 << 20, BytesReceived: 1 << 30,
			ActiveRoutes: 2, AvgSendBps: 1e6, AvgRecvBps: 2e6, ElapsedNs: 1e9,
		},
	}}
}

func muxRttProbe(ok bool) *rpcgrpc.MuxBandwidthEvent {
	p := &rpcgrpc.MuxRttProbe{Sequence: 1, ElapsedNs: 1e9}
	if ok {
		p.LatencyNs = 12_500_000
	} else {
		p.Error = "probe failed"
	}
	return &rpcgrpc.MuxBandwidthEvent{TimestampNs: 3, Payload: &rpcgrpc.MuxBandwidthEvent_RttProbe{RttProbe: p}}
}

func muxDone() *rpcgrpc.MuxBandwidthEvent {
	return &rpcgrpc.MuxBandwidthEvent{TimestampNs: 4, Payload: &rpcgrpc.MuxBandwidthEvent_Done{
		Done: &rpcgrpc.MuxBandwidthDone{
			TotalBytesSent: 1 << 20, TotalBytesReceived: 1 << 21, AvgSendBps: 1e6, AvgRecvBps: 2e6,
			PeakSendBps: 3e6, PeakRecvBps: 4e6, TerminationReason: "duration",
			RoutesRequested: 2, RoutesEstablished: 2, WallTimeNs: 2e9, PumpTimeNs: 1e9, SetupTotalNs: 5e8,
			IdleProbeCount: 3, IdleProbeAvgNs: 1e6, IdleProbeP50Ns: 1e6, IdleProbeP99Ns: 2e6, IdleProbeJitterNs: 1e5,
			ProbeCount: 4, ProbeAvgNs: 2e6, ProbeP50Ns: 2e6, ProbeP99Ns: 4e6, ProbeJitterNs: 2e5,
		},
	}}
}

func muxError() *rpcgrpc.MuxBandwidthEvent {
	return &rpcgrpc.MuxBandwidthEvent{TimestampNs: 5, Payload: &rpcgrpc.MuxBandwidthEvent_Error{
		Error: &rpcgrpc.MuxBandwidthError{Code: "invalid_request", Message: "bad pk"},
	}}
}

func muxRouteFailure() *rpcgrpc.MuxBandwidthEvent {
	return &rpcgrpc.MuxBandwidthEvent{TimestampNs: 6, Payload: &rpcgrpc.MuxBandwidthEvent_RouteFailure{
		RouteFailure: &rpcgrpc.MuxRouteFailure{
			RouteIndex: 1, BytesSentBeforeFailure: 100, BytesReceivedBeforeFailure: 200,
			ErrorMessage: "pump died", ElapsedNs: 1e9,
		},
	}}
}

func treePingResult(level int32, failed bool, source string) *rpcgrpc.PingTreeEvent {
	return &rpcgrpc.PingTreeEvent{TimestampNs: 1, Payload: &rpcgrpc.PingTreeEvent_PingResult{
		PingResult: &rpcgrpc.PingTreeResult{
			TpId: "tp", TpType: "stcpr", RemotePk: "pk", ParentPk: "ppk", Level: level,
			Failed: failed, LatencySource: source, PingAvgNs: 5e6, PingP50Ns: 5e6, PingP99Ns: 9e6,
			JitterNs: 1e6, SampleCount: 3, SetupLatencyNs: 2e6, PingErr: "perr",
		},
	}}
}

func treeDiscovered() *rpcgrpc.PingTreeEvent {
	return &rpcgrpc.PingTreeEvent{TimestampNs: 1, Payload: &rpcgrpc.PingTreeEvent_Discovered{
		Discovered: &rpcgrpc.PingTreeDiscovered{TpId: "tp", TpType: "stcpr", RemotePk: "pk", ParentPk: "ppk", Level: 1},
	}}
}

func treeLevelDone(level int32) *rpcgrpc.PingTreeEvent {
	return &rpcgrpc.PingTreeEvent{TimestampNs: 1, Payload: &rpcgrpc.PingTreeEvent_LevelDone{
		LevelDone: &rpcgrpc.PingTreeLevelDone{Level: level, Attempted: 3, Succeeded: 2, Failed: 1, SkippedCached: 0},
	}}
}

func treeRunDone() *rpcgrpc.PingTreeEvent {
	return &rpcgrpc.PingTreeEvent{TimestampNs: 1, Payload: &rpcgrpc.PingTreeEvent_RunDone{
		RunDone: &rpcgrpc.PingTreeRunDone{
			TotalDiscovered: 5, TotalPinged: 4, TotalSucceeded: 3, TotalFailed: 1,
			TotalSkippedCached: 1, WallTimeNs: 2e9, PeakInFlight: 2, TerminationReason: "max_level",
		},
	}}
}

func treeStatus() *rpcgrpc.PingTreeEvent {
	return &rpcgrpc.PingTreeEvent{TimestampNs: 1, Payload: &rpcgrpc.PingTreeEvent_StatusUpdate{
		StatusUpdate: &rpcgrpc.PingTreeStatusUpdate{Phase: "pinging_level_1", InFlight: 2, Pending: 1, Message: "msg"},
	}}
}

func treeServerError() *rpcgrpc.PingTreeEvent {
	return &rpcgrpc.PingTreeEvent{TimestampNs: 1, Payload: &rpcgrpc.PingTreeEvent_ServerError{
		ServerError: &rpcgrpc.PingTreeServerError{Code: "tpd_fetch_failed", Message: "down"},
	}}
}

// --- mux_bandwidth.go formatters + tracker ---------------------------

func TestMuxBwFormatters(t *testing.T) {
	require.Equal(t, "1.50Gbps", fmtBps(1.5e9))
	require.Equal(t, "2.00Mbps", fmtBps(2e6))
	require.Equal(t, "3.00Kbps", fmtBps(3e3))
	require.Equal(t, "5bps", fmtBps(5))

	require.Equal(t, "1.00GiB", fmtBytes(1<<30))
	require.Equal(t, "1.00MiB", fmtBytes(1<<20))
	require.Equal(t, "1.00KiB", fmtBytes(1<<10))
	require.Equal(t, "5B", fmtBytes(5))

	require.Contains(t, fmtNs(2e9), "s")
	require.Contains(t, fmtNs(5e6), "ms")
	require.Contains(t, fmtNs(5e3), "µs")
	require.Equal(t, "5ns", fmtNs(5))
}

func TestMuxBwRenderHopPath(t *testing.T) {
	require.Equal(t, "", muxBwRenderHopPath(nil))
	require.Equal(t, "A→B→C", muxBwRenderHopPath([]*rpcgrpc.RouteHop{{From: "A", To: "B"}, {From: "B", To: "C"}}))
}

func TestMuxBwHumanHeaderAndRow(t *testing.T) {
	req := &rpcgrpc.MuxBandwidthRequest{Routes: 3, DurationNs: 1e9, MinHops: 2, ProbeRtt: true, IdleBaselineDurationNs: 5e8}
	require.Contains(t, muxBwHumanHeader("PK", req), "routes=3")
	require.Contains(t, muxBwHumanHeader("PK", req), "min-hops=2")

	var buf bytes.Buffer
	for _, ev := range []*rpcgrpc.MuxBandwidthEvent{
		muxRouteEstablished(0, false), muxRouteEstablished(1, true), muxSample(),
		muxRttProbe(true), muxRttProbe(false), muxDone(), muxError(), muxRouteFailure(),
	} {
		muxBwEmitHumanRow(&buf, ev, timeZero())
	}
	require.NotEmpty(t, buf.String())
}

func TestMuxBwTracker(t *testing.T) {
	tr := newMuxBwTracker()
	for _, ev := range []*rpcgrpc.MuxBandwidthEvent{
		muxRouteEstablished(1, false), muxRouteEstablished(0, true), muxRouteFailure(), muxDone(),
	} {
		tr.record(ev)
	}
	var buf bytes.Buffer
	tr.printSummary(&buf)
	require.Contains(t, buf.String(), "Summary")
	require.Contains(t, buf.String(), "routes:")

	// No-Done path with a server error.
	tr2 := newMuxBwTracker()
	tr2.record(muxError())
	var buf2 bytes.Buffer
	tr2.printSummary(&buf2)
	require.Contains(t, buf2.String(), "server error")

	// No-Done, no-error path.
	var buf3 bytes.Buffer
	newMuxBwTracker().printSummary(&buf3)
	require.Contains(t, buf3.String(), "interrupted")
}

func TestMuxBwClassifyAndEmit(t *testing.T) {
	enc := json.NewEncoder(&bytes.Buffer{})
	mo := protojson.MarshalOptions{}
	for _, ev := range []*rpcgrpc.MuxBandwidthEvent{
		muxRouteEstablished(0, false), muxSample(), muxRttProbe(true), muxDone(), muxError(), muxRouteFailure(),
	} {
		typ, _ := classifyMuxBwEvent(ev)
		require.NotEqual(t, "unknown", typ)
		require.NoError(t, emitMuxBwOne(enc, mo, ev))
	}
	// Empty payload → "unknown".
	typ, _ := classifyMuxBwEvent(&rpcgrpc.MuxBandwidthEvent{})
	require.Equal(t, "unknown", typ)
}

func TestMuxBwSignalContext(t *testing.T) {
	ctx, cancel := muxBwSignalContext()
	require.NotNil(t, ctx)
	cancel()
	<-ctx.Done()
}

// --- mux_bandwidth_tui.go model --------------------------------------

func TestMuxBwTUIModel(t *testing.T) {
	origProbe := muxBwProbeRTT
	muxBwProbeRTT = true // exercise the RTT render block + fixedRows branch
	defer func() { muxBwProbeRTT = origProbe }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := &rpcgrpc.MuxBandwidthRequest{Routes: 2, DurationNs: 1e9}
	m := newMuxBwTUIModel(ctx, cancel, "targetPK", req)

	require.NotNil(t, m.Init())
	require.Equal(t, 12, m.fixedRows()) // 9 + 3 (probe-rtt)

	// Window size makes the model ready and builds the viewport.
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	// Feed every event variant through Update → applyEvent.
	for _, ev := range []*rpcgrpc.MuxBandwidthEvent{
		muxRouteEstablished(0, false), muxRouteEstablished(1, true), muxSample(),
		muxRttProbe(true), muxRttProbe(false), muxDone(), muxError(), muxRouteFailure(),
	} {
		m.Update(muxBwEventMsg{ev: ev})
	}

	// Ticks + spinner + scroll keys + auto-scroll toggle.
	m.Update(muxBwTickMsg{})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	for _, kt := range []tea.KeyType{tea.KeyUp, tea.KeyDown, tea.KeyPgUp, tea.KeyPgDown, tea.KeyHome, tea.KeyEnd} {
		m.Update(tea.KeyMsg{Type: kt})
	}

	require.NotEmpty(t, m.View())
	require.NotEmpty(t, m.renderDone())

	// Resize after ready (the else branch).
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	// Stream-end + error messages.
	m.Update(muxBwStreamDoneMsg{})
	m.Update(muxBwStreamErrMsg{err: context.Canceled})
	require.NotEmpty(t, m.View())
}

func TestMuxBwTUIQuit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newMuxBwTUIModel(ctx, cancel, "pk", &rpcgrpc.MuxBandwidthRequest{Routes: 1, DurationNs: 1e9})
	_, c := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	require.NotNil(t, c)
	require.Equal(t, "Shutting down...\n", m.View())
}

// --- tree_stream.go helpers ------------------------------------------

func TestTreeStreamHumanHeaderAndRow(t *testing.T) {
	req := &rpcgrpc.PingTreeRequest{Hops: 2, MaxLevel: 3, Tries: 5, DryRun: true}
	require.Contains(t, treeStreamHumanHeader(req), "hops=2")
	require.Contains(t, treeStreamHumanHeader(req), "dry-run")

	require.EqualValues(t, 3, hopsFromLevel(3))

	var buf bytes.Buffer
	for _, ev := range []*rpcgrpc.PingTreeEvent{
		treePingResult(1, false, "live_ping"), treePingResult(2, false, "transport_summary"),
		treePingResult(1, true, "live_ping"), treeLevelDone(1), treeRunDone(), treeServerError(),
	} {
		emitHumanRow(&buf, ev, timeZero())
	}
	require.NotEmpty(t, buf.String())
}

func TestPingTreeStats(t *testing.T) {
	s := newPingTreeStats()
	for _, ev := range []*rpcgrpc.PingTreeEvent{
		treePingResult(1, false, "live_ping"), treePingResult(1, false, "transport_summary"),
		treePingResult(2, true, "live_ping"), treePingResult(2, false, "skipped"), treeRunDone(),
	} {
		s.record(ev)
	}
	var buf bytes.Buffer
	s.printSummary(&buf)
	require.Contains(t, buf.String(), "Per-hop summary")
	require.Contains(t, buf.String(), "totals:")

	// Empty stats path.
	var empty bytes.Buffer
	newPingTreeStats().printSummary(&empty)
	require.Contains(t, empty.String(), "no ping results")
}

func TestTreeStreamClassifyAndEmit(t *testing.T) {
	enc := json.NewEncoder(&bytes.Buffer{})
	mo := protojson.MarshalOptions{}
	for _, ev := range []*rpcgrpc.PingTreeEvent{
		treeDiscovered(), treePingResult(1, false, "live_ping"), treeLevelDone(1),
		treeRunDone(), treeStatus(), treeServerError(),
	} {
		typ, _ := classifyPayload(ev)
		require.NotEqual(t, "unknown", typ)
		require.NoError(t, emitOne(enc, mo, ev))
	}
	typ, _ := classifyPayload(&rpcgrpc.PingTreeEvent{})
	require.Equal(t, "unknown", typ)
}

func TestTreeStreamSignalContext(t *testing.T) {
	ctx, cancel := signalContext()
	require.NotNil(t, ctx)
	cancel()
	<-ctx.Done()
}

// --- tree.go helpers + model -----------------------------------------

func TestBuildPingTreeRequest(t *testing.T) {
	req := buildPingTreeRequest()
	require.NotNil(t, req)
}

func TestTreeClassifyEvent(t *testing.T) {
	for _, ev := range []*rpcgrpc.PingTreeEvent{
		treeDiscovered(), treePingResult(1, false, "live_ping"), treeLevelDone(1),
		treeRunDone(), treeStatus(), treeServerError(),
	} {
		typ, _ := classifyEvent(ev)
		require.NotEqual(t, "unknown", typ)
	}
	typ, _ := classifyEvent(&rpcgrpc.PingTreeEvent{})
	require.Equal(t, "unknown", typ)
}

func TestWriteNDJSONLine(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "ndjson")
	require.NoError(t, err)
	defer f.Close() //nolint:errcheck
	for _, ev := range []*rpcgrpc.PingTreeEvent{
		treeDiscovered(), treePingResult(1, false, "live_ping"), treeLevelDone(1),
		treeRunDone(), treeStatus(), treeServerError(),
	} {
		writeNDJSONLine(f, ev)
	}
	info, err := os.Stat(filepath.Clean(f.Name()))
	require.NoError(t, err)
	require.Positive(t, info.Size())
}

func TestPingTreeModel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newPingTreeModel(ctx, cancel)

	require.NotNil(t, m.Init())
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	for _, ev := range []*rpcgrpc.PingTreeEvent{
		treeDiscovered(), treePingResult(1, false, "live_ping"), treePingResult(2, true, "live_ping"),
		treePingResult(1, false, "transport_summary"), treeLevelDone(1), treeStatus(),
		treeRunDone(), treeServerError(),
	} {
		m.Update(eventMsg{ev: ev})
	}

	m.Update(tickMsg{})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	for _, kt := range []tea.KeyType{tea.KeyUp, tea.KeyDown, tea.KeyPgUp, tea.KeyPgDown, tea.KeyHome, tea.KeyEnd} {
		m.Update(tea.KeyMsg{Type: kt})
	}

	require.NotEmpty(t, m.View())
	require.NotEmpty(t, m.statsLine())
	require.NotEmpty(t, m.renderTree())

	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.Update(streamDoneMsg{})
	m.Update(streamErrMsg{err: context.Canceled})
	require.NotEmpty(t, m.View())
}

func TestPingTreeModelQuit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newPingTreeModel(ctx, cancel)
	_, c := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	require.NotNil(t, c)
	require.NotEmpty(t, m.View()) // "Shutting down" path
}
