// Package rpcgrpc — pkg/visor/rpcgrpc/grpc_test.go: end-to-end coverage
// of the PingService client + server pair. Each test stands up a real
// gRPC server on a loopback listener backed by a configurable fakeVisor,
// connects a PingClient to it, and drives one RPC. This exercises the
// hand-written client/server handlers AND the generated proto
// marshaling on both sides of the wire — the bulk of the package.
//
// The fakeVisor implements the full VisorAPI with working defaults so a
// test only overrides the behavior it cares about. Streaming RPCs that
// loop until the client disconnects are driven from a goroutine and torn
// down by canceling the call context.
package rpcgrpc

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
)

// --- fakeVisor: a fully-overridable VisorAPI implementation ----------

type fakeVisor struct {
	localPK cipher.PubKey

	dialPing               func(PingConf) error
	pingOnce               func(PingConf) (time.Duration, error)
	pingOnceWithEcho       func(PingConf, bool) (uint64, uint64, time.Duration, error)
	stopPing               func(cipher.PubKey) error
	stopPingRoute          func(PingRouteRef) error
	getPingRoute           func(cipher.PubKey) []cipher.PubKey
	getPingRouteDetails    func(cipher.PubKey) []RouteHopInfo
	getPingRouteDetailsAt  func(PingRouteRef) []RouteHopInfo
	getLastRouteCalcTime   func() time.Duration
	dialDmsgPing           func(cipher.PubKey) error
	dialDmsgPingViaServer  func(cipher.PubKey, cipher.PubKey) error
	dmsgPingOnce           func(PingConf) (time.Duration, error)
	dmsgPingOnceWithEcho   func(PingConf, bool) (uint64, uint64, time.Duration, error)
	stopDmsgPing           func(cipher.PubKey) error
	getDmsgPingServerPK    func(cipher.PubKey) (cipher.PubKey, error)
	getRemoteDmsgServers   func(cipher.PubKey) ([]cipher.PubKey, error)
	dialDmsgRPC            func(cipher.PubKey) (net.Conn, error)
	subscribeLogs          func(logging.Filter, int) (<-chan *logrus.Entry, func() uint64)
	subscribeGroupMessages func(int) (<-chan GroupMessageData, func() uint64)
	snapshotGroupAfterNs   func(int64) []GroupMessageData
	snapshotHistoryAfterNs func(string, int64) []GroupMessageData
	fetchAllTransports     func(context.Context) ([]*transport.Entry, error)
	transportLatency       func(cipher.PubKey) float64

	mu             sync.Mutex
	groupSendCount int
}

func newFakeVisor() *fakeVisor {
	localPK, _ := cipher.GenerateKeyPair()
	srvPK, _ := cipher.GenerateKeyPair()
	hop := RouteHopInfo{TpID: "tp-1", From: localPK.String(), To: srvPK.String(), TpType: "stcpr"}
	return &fakeVisor{
		localPK:  localPK,
		dialPing: func(PingConf) error { return nil },
		pingOnce: func(PingConf) (time.Duration, error) { return 5 * time.Millisecond, nil },
		pingOnceWithEcho: func(PingConf, bool) (uint64, uint64, time.Duration, error) {
			return 1024, 1024, 5 * time.Millisecond, nil
		},
		stopPing:              func(cipher.PubKey) error { return nil },
		stopPingRoute:         func(PingRouteRef) error { return nil },
		getPingRoute:          func(cipher.PubKey) []cipher.PubKey { return []cipher.PubKey{srvPK} },
		getPingRouteDetails:   func(cipher.PubKey) []RouteHopInfo { return []RouteHopInfo{hop} },
		getPingRouteDetailsAt: func(PingRouteRef) []RouteHopInfo { return []RouteHopInfo{hop} },
		getLastRouteCalcTime:  func() time.Duration { return time.Millisecond },
		dialDmsgPing:          func(cipher.PubKey) error { return nil },
		dialDmsgPingViaServer: func(cipher.PubKey, cipher.PubKey) error { return nil },
		dmsgPingOnce:          func(PingConf) (time.Duration, error) { return 5 * time.Millisecond, nil },
		dmsgPingOnceWithEcho: func(PingConf, bool) (uint64, uint64, time.Duration, error) {
			return 1024, 1024, 5 * time.Millisecond, nil
		},
		stopDmsgPing:         func(cipher.PubKey) error { return nil },
		getDmsgPingServerPK:  func(cipher.PubKey) (cipher.PubKey, error) { return srvPK, nil },
		getRemoteDmsgServers: func(cipher.PubKey) ([]cipher.PubKey, error) { return []cipher.PubKey{srvPK}, nil },
		dialDmsgRPC:          func(cipher.PubKey) (net.Conn, error) { return nil, errors.New("dmsg rpc not available") },
		subscribeLogs: func(logging.Filter, int) (<-chan *logrus.Entry, func() uint64) {
			return nil, func() uint64 { return 0 }
		},
		subscribeGroupMessages: func(int) (<-chan GroupMessageData, func() uint64) {
			return nil, func() uint64 { return 0 }
		},
		snapshotGroupAfterNs:   func(int64) []GroupMessageData { return nil },
		snapshotHistoryAfterNs: func(string, int64) []GroupMessageData { return nil },
		fetchAllTransports:     func(context.Context) ([]*transport.Entry, error) { return nil, nil },
		transportLatency:       func(cipher.PubKey) float64 { return 0 },
	}
}

func (f *fakeVisor) DialPing(c PingConf) error                  { return f.dialPing(c) }
func (f *fakeVisor) PingOnce(c PingConf) (time.Duration, error) { return f.pingOnce(c) }
func (f *fakeVisor) PingOnceWithEcho(c PingConf, full bool) (uint64, uint64, time.Duration, error) {
	return f.pingOnceWithEcho(c, full)
}
func (f *fakeVisor) StopPing(pk cipher.PubKey) error               { return f.stopPing(pk) }
func (f *fakeVisor) StopPingRoute(ref PingRouteRef) error          { return f.stopPingRoute(ref) }
func (f *fakeVisor) GetPingRoute(pk cipher.PubKey) []cipher.PubKey { return f.getPingRoute(pk) }
func (f *fakeVisor) GetPingRouteDetails(pk cipher.PubKey) []RouteHopInfo {
	return f.getPingRouteDetails(pk)
}
func (f *fakeVisor) GetPingRouteDetailsAt(ref PingRouteRef) []RouteHopInfo {
	return f.getPingRouteDetailsAt(ref)
}
func (f *fakeVisor) GetLastRouteCalcTime() time.Duration { return f.getLastRouteCalcTime() }
func (f *fakeVisor) DialDmsgPing(pk cipher.PubKey) error { return f.dialDmsgPing(pk) }
func (f *fakeVisor) DialDmsgPingViaServer(pk, srv cipher.PubKey) error {
	return f.dialDmsgPingViaServer(pk, srv)
}
func (f *fakeVisor) DmsgPingOnce(c PingConf) (time.Duration, error) { return f.dmsgPingOnce(c) }
func (f *fakeVisor) DmsgPingOnceWithEcho(c PingConf, full bool) (uint64, uint64, time.Duration, error) {
	return f.dmsgPingOnceWithEcho(c, full)
}
func (f *fakeVisor) StopDmsgPing(pk cipher.PubKey) error { return f.stopDmsgPing(pk) }
func (f *fakeVisor) GetDmsgPingServerPK(pk cipher.PubKey) (cipher.PubKey, error) {
	return f.getDmsgPingServerPK(pk)
}
func (f *fakeVisor) GetRemoteDmsgServers(pk cipher.PubKey) ([]cipher.PubKey, error) {
	return f.getRemoteDmsgServers(pk)
}
func (f *fakeVisor) DialDmsgRPC(pk cipher.PubKey) (net.Conn, error) { return f.dialDmsgRPC(pk) }
func (f *fakeVisor) SubscribeLogs(filt logging.Filter, capacity int) (<-chan *logrus.Entry, func() uint64) {
	return f.subscribeLogs(filt, capacity)
}
func (f *fakeVisor) SubscribeGroupMessages(capacity int) (<-chan GroupMessageData, func() uint64) {
	return f.subscribeGroupMessages(capacity)
}
func (f *fakeVisor) SnapshotGroupMessagesAfterNs(ns int64) []GroupMessageData {
	return f.snapshotGroupAfterNs(ns)
}
func (f *fakeVisor) SnapshotGroupHistoryAfterNs(g string, ns int64) []GroupMessageData {
	return f.snapshotHistoryAfterNs(g, ns)
}
func (f *fakeVisor) IncGroupStreamSend() {
	f.mu.Lock()
	f.groupSendCount++
	f.mu.Unlock()
}
func (f *fakeVisor) LocalPK() cipher.PubKey { return f.localPK }
func (f *fakeVisor) FetchAllTransportEntries(ctx context.Context) ([]*transport.Entry, error) {
	return f.fetchAllTransports(ctx)
}
func (f *fakeVisor) GetTransportLatencyByRemotePK(pk cipher.PubKey) float64 {
	return f.transportLatency(pk)
}

func (f *fakeVisor) groupSends() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.groupSendCount
}

// --- test harness ----------------------------------------------------

// newTestServer stands up a PingServer over a loopback gRPC listener and
// returns a connected PingClient plus a cleanup func.
func newTestServer(t *testing.T, visor VisorAPI) (*PingClient, func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	RegisterPingServiceServer(srv, NewPingServer(visor, logging.MustGetLogger("test")))
	go func() { _ = srv.Serve(lis) }() //nolint

	client, err := NewPingClient(lis.Addr().String())
	if err != nil {
		srv.Stop()
		t.Fatalf("NewPingClient: %v", err)
	}
	cleanup := func() {
		_ = client.Close() //nolint
		srv.Stop()
	}
	return client, cleanup
}

// validPK returns a freshly generated public key string.
func validPK(t *testing.T) string {
	t.Helper()
	pk, _ := cipher.GenerateKeyPair()
	return pk.String()
}

// --- StreamPing ------------------------------------------------------

func TestStreamPing(t *testing.T) {
	client, cleanup := newTestServer(t, newFakeVisor())
	defer cleanup()

	var got []int32
	err := client.StreamPing(context.Background(), validPK(t), 3, 2, true, 0, 0,
		func(seq int32, _ time.Duration, isSetup bool, _ []RouteHopDetail, _ string, _ time.Duration, perr error) {
			if perr != nil {
				t.Errorf("ping err: %v", perr)
			}
			got = append(got, seq)
			_ = isSetup
		})
	if err != nil {
		t.Fatalf("StreamPing: %v", err)
	}
	// setup (seq 0) + 3 pings.
	if len(got) != 4 {
		t.Errorf("received %d results, want 4 (setup + 3 pings): %v", len(got), got)
	}
}

func TestStreamPingWithTimeouts(t *testing.T) {
	// Non-zero ping + setup timeouts drive the goroutine + select
	// branches in the server handler.
	client, cleanup := newTestServer(t, newFakeVisor())
	defer cleanup()

	n := 0
	err := client.StreamPing(context.Background(), validPK(t), 2, 2, false,
		2*time.Second, 2*time.Second,
		func(int32, time.Duration, bool, []RouteHopDetail, string, time.Duration, error) { n++ })
	if err != nil {
		t.Fatalf("StreamPing with timeouts: %v", err)
	}
	if n != 3 {
		t.Errorf("got %d results, want 3", n)
	}
}

func TestStreamPingInvalidPK(t *testing.T) {
	client, cleanup := newTestServer(t, newFakeVisor())
	defer cleanup()
	err := client.StreamPing(context.Background(), "not-a-pubkey", 1, 2, false, 0, 0,
		func(int32, time.Duration, bool, []RouteHopDetail, string, time.Duration, error) {})
	if err == nil {
		t.Fatal("expected error for invalid public key")
	}
}

func TestStreamPingDialError(t *testing.T) {
	fv := newFakeVisor()
	fv.dialPing = func(PingConf) error { return errors.New("boom") }
	client, cleanup := newTestServer(t, fv)
	defer cleanup()
	err := client.StreamPing(context.Background(), validPK(t), 1, 2, false, 0, 0,
		func(int32, time.Duration, bool, []RouteHopDetail, string, time.Duration, error) {})
	if err == nil {
		t.Fatal("expected dial error to propagate")
	}
}

func TestStreamPingWithRoute(t *testing.T) {
	client, cleanup := newTestServer(t, newFakeVisor())
	defer cleanup()
	hops := []RouteHopDetail{{TpID: "tp-a", From: "x", To: "y", TpType: "stcpr"}}
	n := 0
	err := client.StreamPingWithRoute(context.Background(), validPK(t), 1, 2, false, 0, 0,
		hops, hops,
		func(int32, time.Duration, bool, []RouteHopDetail, string, time.Duration, error) { n++ })
	if err != nil {
		t.Fatalf("StreamPingWithRoute: %v", err)
	}
	if n != 2 {
		t.Errorf("got %d results, want 2 (setup + 1 ping)", n)
	}
}

// --- StreamDmsgPing --------------------------------------------------

func TestStreamDmsgPing(t *testing.T) {
	client, cleanup := newTestServer(t, newFakeVisor())
	defer cleanup()

	t.Run("auto server select", func(t *testing.T) {
		n := 0
		err := client.StreamDmsgPing(context.Background(), validPK(t), 2, 2, 0, "",
			func(int32, time.Duration, bool, []RouteHopDetail, string, time.Duration, error) { n++ })
		if err != nil {
			t.Fatalf("StreamDmsgPing: %v", err)
		}
		if n != 3 {
			t.Errorf("got %d, want 3 (setup + 2)", n)
		}
	})

	t.Run("via specific server with ping timeout", func(t *testing.T) {
		n := 0
		err := client.StreamDmsgPing(context.Background(), validPK(t), 1, 2, time.Second, validPK(t),
			func(int32, time.Duration, bool, []RouteHopDetail, string, time.Duration, error) { n++ })
		if err != nil {
			t.Fatalf("StreamDmsgPing via server: %v", err)
		}
		if n != 2 {
			t.Errorf("got %d, want 2", n)
		}
	})
}

func TestStreamDmsgPingInvalidPK(t *testing.T) {
	client, cleanup := newTestServer(t, newFakeVisor())
	defer cleanup()
	err := client.StreamDmsgPing(context.Background(), "bad", 1, 2, 0, "",
		func(int32, time.Duration, bool, []RouteHopDetail, string, time.Duration, error) {})
	if err == nil {
		t.Fatal("expected error for invalid pk")
	}
}

// --- Bandwidth -------------------------------------------------------

func TestStreamBandwidthTest(t *testing.T) {
	client, cleanup := newTestServer(t, newFakeVisor())
	defer cleanup()
	// 600ms duration crosses the 500ms progress-emit boundary so both
	// the periodic and the final BandwidthProgress branches run.
	var finals int
	err := client.StreamBandwidthTest(context.Background(), validPK(t), 600*time.Millisecond, 16, true,
		func(_, _ uint64, _ time.Duration, _, _ float64, isFinal bool, perr error) {
			if perr != nil {
				t.Errorf("bw err: %v", perr)
			}
			if isFinal {
				finals++
			}
		})
	if err != nil {
		t.Fatalf("StreamBandwidthTest: %v", err)
	}
	if finals != 1 {
		t.Errorf("got %d final updates, want exactly 1", finals)
	}
}

func TestStreamDmsgBandwidthTest(t *testing.T) {
	client, cleanup := newTestServer(t, newFakeVisor())
	defer cleanup()
	got := false
	err := client.StreamDmsgBandwidthTest(context.Background(), validPK(t), 50*time.Millisecond, 16,
		func(_, _ uint64, _ time.Duration, _, _ float64, isFinal bool, _ error) {
			if isFinal {
				got = true
			}
		})
	if err != nil {
		t.Fatalf("StreamDmsgBandwidthTest: %v", err)
	}
	if !got {
		t.Error("expected a final progress update")
	}
}

func TestStreamBandwidthTestDialError(t *testing.T) {
	fv := newFakeVisor()
	fv.dialPing = func(PingConf) error { return errors.New("no dial") }
	client, cleanup := newTestServer(t, fv)
	defer cleanup()
	err := client.StreamBandwidthTest(context.Background(), validPK(t), 50*time.Millisecond, 0, false,
		func(uint64, uint64, time.Duration, float64, float64, bool, error) {})
	if err == nil {
		t.Fatal("expected dial error")
	}
}

// --- GetRemoteDmsgServers --------------------------------------------

func TestGetRemoteDmsgServers(t *testing.T) {
	client, cleanup := newTestServer(t, newFakeVisor())
	defer cleanup()

	servers, err := client.GetRemoteDmsgServers(context.Background(), validPK(t))
	if err != nil {
		t.Fatalf("GetRemoteDmsgServers: %v", err)
	}
	if len(servers) != 1 {
		t.Errorf("got %d servers, want 1", len(servers))
	}

	if _, err := client.GetRemoteDmsgServers(context.Background(), "bad-pk"); err == nil {
		t.Error("expected error for invalid pk")
	}

	fv := newFakeVisor()
	fv.getRemoteDmsgServers = func(cipher.PubKey) ([]cipher.PubKey, error) { return nil, errors.New("offline") }
	client2, cleanup2 := newTestServer(t, fv)
	defer cleanup2()
	if _, err := client2.GetRemoteDmsgServers(context.Background(), validPK(t)); err == nil {
		t.Error("expected visor error to propagate")
	}
}

// --- System stats ----------------------------------------------------

func TestGetSystemStats(t *testing.T) {
	client, cleanup := newTestServer(t, newFakeVisor())
	defer cleanup()
	stats, err := client.GetSystemStats(context.Background(), true, 5)
	if err != nil {
		t.Fatalf("GetSystemStats: %v", err)
	}
	if stats == nil || stats.TimestampNs == 0 {
		t.Error("expected populated system stats")
	}
}

func TestStreamSystemStats(t *testing.T) {
	client, cleanup := newTestServer(t, newFakeVisor())
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	count := 0
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = client.StreamSystemStats(ctx, 30*time.Millisecond, false, 0, func(*SystemStats) { //nolint
			count++
			if count >= 2 {
				cancel()
			}
		})
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("StreamSystemStats did not return after cancel")
	}
	if count < 2 {
		t.Errorf("received %d stats, want >= 2", count)
	}
}

// --- App logs --------------------------------------------------------

func TestStreamAppLogsRequiresFilter(t *testing.T) {
	client, cleanup := newTestServer(t, newFakeVisor())
	defer cleanup()
	err := client.StreamAppLogs(context.Background(), "", false, "debug", nil, nil, func(*AppLogEntry) {})
	if err == nil {
		t.Fatal("expected error when neither app_name nor modules given")
	}
}

func TestStreamAppLogsNoBroadcaster(t *testing.T) {
	// Default fake returns a nil channel from SubscribeLogs.
	client, cleanup := newTestServer(t, newFakeVisor())
	defer cleanup()
	err := client.StreamAppLogs(context.Background(), "myapp", false, "info", nil, nil, func(*AppLogEntry) {})
	if err == nil {
		t.Fatal("expected 'broadcaster not available' error")
	}
}

func TestStreamAppLogsDelivers(t *testing.T) {
	logCh := make(chan *logrus.Entry, 4)
	fv := newFakeVisor()
	fv.subscribeLogs = func(logging.Filter, int) (<-chan *logrus.Entry, func() uint64) {
		return logCh, func() uint64 { return 0 }
	}
	client, cleanup := newTestServer(t, fv)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	subscribed := make(chan struct{})
	got := make(chan *AppLogEntry, 4)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = client.StreamAppLogs(ctx, "*", true, "debug", []string{"mod"}, subscribed, func(e *AppLogEntry) { //nolint
			got <- e
		})
	}()

	select {
	case <-subscribed:
	case <-time.After(3 * time.Second):
		t.Fatal("never received subscribed sentinel")
	}

	logCh <- &logrus.Entry{Time: time.Now(), Level: logrus.InfoLevel, Message: "hello", Data: logrus.Fields{"_module": "mod"}}
	select {
	case e := <-got:
		if e.Message != "hello" {
			t.Errorf("got message %q, want hello", e.Message)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("log entry never delivered")
	}

	cancel()
	<-done
}

// --- Calc routes -----------------------------------------------------

func TestStreamCalcRoutesNoTransports(t *testing.T) {
	client, cleanup := newTestServer(t, newFakeVisor())
	defer cleanup()
	// Default fake returns no transport entries → "no transport entries".
	err := client.StreamCalcRoutes(context.Background(), "", validPK(t), 0, 3, 0, 0, "", "",
		func(*CalcRoute) bool { return true })
	if err == nil {
		t.Fatal("expected error when no transport entries available")
	}
}

func TestStreamCalcRoutesInvalidDst(t *testing.T) {
	client, cleanup := newTestServer(t, newFakeVisor())
	defer cleanup()
	err := client.StreamCalcRoutes(context.Background(), "", "bad-dst", 0, 3, 0, 0, "", "",
		func(*CalcRoute) bool { return true })
	if err == nil {
		t.Fatal("expected error for invalid dst pk")
	}
}

func TestStreamCalcRoutesFetchError(t *testing.T) {
	fv := newFakeVisor()
	fv.fetchAllTransports = func(context.Context) ([]*transport.Entry, error) {
		return nil, errors.New("tpd down")
	}
	client, cleanup := newTestServer(t, fv)
	defer cleanup()
	err := client.StreamCalcRoutes(context.Background(), "", validPK(t), 0, 3, 0, 0, "", "",
		func(*CalcRoute) bool { return true })
	if err == nil {
		t.Fatal("expected fetch error to propagate")
	}
}

// --- Remote system stats ---------------------------------------------

func TestStreamRemoteSystemStatsDialError(t *testing.T) {
	// Default fake's DialDmsgRPC returns an error.
	client, cleanup := newTestServer(t, newFakeVisor())
	defer cleanup()
	err := client.StreamRemoteSystemStats(context.Background(), validPK(t), 0, false, 0, func(*SystemStats) {})
	if err == nil {
		t.Fatal("expected dial-over-dmsg error")
	}
}

func TestStreamRemoteSystemStatsInvalidPK(t *testing.T) {
	client, cleanup := newTestServer(t, newFakeVisor())
	defer cleanup()
	err := client.StreamRemoteSystemStats(context.Background(), "bad", 0, false, 0, func(*SystemStats) {})
	if err == nil {
		t.Fatal("expected error for invalid remote pk")
	}
}

// --- Group messages --------------------------------------------------

func TestStreamGroupMessagesNoInbox(t *testing.T) {
	// Default fake returns a nil group channel.
	client, cleanup := newTestServer(t, newFakeVisor())
	defer cleanup()
	err := client.StreamGroupMessages(context.Background(), "g1", 0, nil, func(*GroupMessageEvent) {})
	if err == nil {
		t.Fatal("expected 'group inbox not available' error")
	}
}

func TestStreamGroupMessagesDelivers(t *testing.T) {
	groupCh := make(chan GroupMessageData, 8)
	fv := newFakeVisor()
	fv.subscribeGroupMessages = func(int) (<-chan GroupMessageData, func() uint64) {
		return groupCh, func() uint64 { return 0 }
	}
	// Backlog replay: history + inbox snapshots merged for the since cursor.
	fv.snapshotHistoryAfterNs = func(string, int64) []GroupMessageData {
		return []GroupMessageData{{GroupID: "g1", SenderPK: "p1", TimestampNs: 50, Body: "old-hist"}}
	}
	fv.snapshotGroupAfterNs = func(int64) []GroupMessageData {
		return []GroupMessageData{{GroupID: "g1", SenderPK: "p1", TimestampNs: 60, Body: "old-inbox"}}
	}
	client, cleanup := newTestServer(t, fv)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	subscribed := make(chan struct{})
	got := make(chan *GroupMessageEvent, 8)
	done := make(chan struct{})
	go func() {
		defer close(done)
		// since=10 triggers the backlog replay branch.
		_ = client.StreamGroupMessages(ctx, "g1", 10, subscribed, func(e *GroupMessageEvent) { //nolint
			got <- e
		})
	}()

	select {
	case <-subscribed:
	case <-time.After(3 * time.Second):
		t.Fatal("never got subscribed sentinel")
	}

	// Expect the two replayed backlog messages first.
	for i := range 2 {
		select {
		case <-got:
		case <-time.After(3 * time.Second):
			t.Fatalf("missing backlog message %d", i)
		}
	}

	// Now a live message on the channel (newer than the replayed ones).
	groupCh <- GroupMessageData{GroupID: "g1", SenderPK: "p2", TimestampNs: 100, Body: "live"}
	select {
	case e := <-got:
		if e.Body != "live" {
			t.Errorf("live message body = %q, want live", e.Body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("live message never delivered")
	}

	// A message for a different group must be filtered out; cancel ends it.
	groupCh <- GroupMessageData{GroupID: "other", SenderPK: "p3", TimestampNs: 110, Body: "nope"}
	cancel()
	<-done

	if fv.groupSends() < 3 {
		t.Errorf("IncGroupStreamSend called %d times, want >= 3", fv.groupSends())
	}
}
