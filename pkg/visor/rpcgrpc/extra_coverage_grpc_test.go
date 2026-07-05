// Package rpcgrpc — pkg/visor/rpcgrpc/extra_coverage_grpc_test.go:
// targeted tests for the meaningful production branches the happy-path
// suite leaves uncovered: the route-finder BFS success path, the
// ping-tree dmsg + ping-failure result branches, a bandwidth dial
// failure, and the StreamRemoteSystemStats DMSG-proxy loop (driven
// against a real second gRPC server reached over an in-memory pipe).
package rpcgrpc

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
)

// --- StreamCalcRoutes happy path -------------------------------------

func TestStreamCalcRoutesFindsRoute(t *testing.T) {
	fv := newFakeVisor()
	mid, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()
	// A direct edge plus a two-hop path through `mid`, so the BFS has
	// at least one route to emit from src (local) to dst.
	fv.fetchAllTransports = func(context.Context) ([]*transport.Entry, error) {
		return []*transport.Entry{
			tpEntry(fv.localPK, dst),
			tpEntry(fv.localPK, mid),
			tpEntry(mid, dst),
		}, nil
	}
	client, cleanup := newTestServer(t, fv)
	defer cleanup()

	var routes int
	err := client.StreamCalcRoutes(context.Background(), "", dst.String(), 0, 3, 0, 0, "", "",
		func(r *CalcRoute) bool {
			if len(r.Hops) > 0 {
				routes++
			}
			return true
		})
	if err != nil {
		t.Fatalf("StreamCalcRoutes: %v", err)
	}
	if routes == 0 {
		t.Error("expected at least one route to be emitted")
	}
}

func TestStreamCalcRoutesEarlyStop(t *testing.T) {
	fv := newFakeVisor()
	mid, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()
	fv.fetchAllTransports = func(context.Context) ([]*transport.Entry, error) {
		return []*transport.Entry{
			tpEntry(fv.localPK, dst),
			tpEntry(fv.localPK, mid),
			tpEntry(mid, dst),
		}, nil
	}
	client, cleanup := newTestServer(t, fv)
	defer cleanup()

	// Returning false after the first route cancels the stream (and the
	// server-side BFS) — exercises the client's early-stop branch.
	got := 0
	err := client.StreamCalcRoutes(context.Background(), fv.localPK.String(), dst.String(), 0, 3, 0, 0, "", "",
		func(*CalcRoute) bool {
			got++
			return false
		})
	if err != nil {
		t.Fatalf("StreamCalcRoutes early-stop: %v", err)
	}
	if got != 1 {
		t.Errorf("callback fired %d times, want exactly 1 before stop", got)
	}
}

// --- pingOneTransport failure + dmsg branches ------------------------

func TestStreamPingTreePingFailure(t *testing.T) {
	fv := newFakeVisor()
	peer, _ := cipher.GenerateKeyPair()
	fv.fetchAllTransports = func(context.Context) ([]*transport.Entry, error) {
		return []*transport.Entry{tpEntry(fv.localPK, peer)}, nil
	}
	// Every ping errors → the result is marked Failed with "no
	// successful pings".
	fv.pingOnce = func(PingConf) (time.Duration, error) { return 0, errors.New("unreachable") }
	client, cleanup := newTestServer(t, fv)
	defer cleanup()

	stream, err := client.StreamPingTree(context.Background(), &PingTreeRequest{MaxLevel: 1, Tries: 1})
	if err != nil {
		t.Fatalf("StreamPingTree: %v", err)
	}
	var failed bool
	for {
		ev, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		if pr, ok := ev.Payload.(*PingTreeEvent_PingResult); ok && pr.PingResult.Failed {
			failed = true
		}
	}
	if !failed {
		t.Error("expected a Failed ping result when PingOnce errors")
	}
}

func TestStreamPingTreeDmsgOnly(t *testing.T) {
	fv := newFakeVisor()
	peer, _ := cipher.GenerateKeyPair()
	fv.fetchAllTransports = func(context.Context) ([]*transport.Entry, error) {
		return []*transport.Entry{tpEntry(fv.localPK, peer)}, nil
	}
	client, cleanup := newTestServer(t, fv)
	defer cleanup()

	// DmsgOnly routes the setup + ping through the dmsg-specific visor
	// calls (DialDmsgPing / DmsgPingOnce / StopDmsgPing).
	stream, err := client.StreamPingTree(context.Background(), &PingTreeRequest{
		MaxLevel: 1,
		Tries:    1,
		DmsgOnly: true,
	})
	if err != nil {
		t.Fatalf("StreamPingTree: %v", err)
	}
	var ok bool
	for {
		ev, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		if pr, isPR := ev.Payload.(*PingTreeEvent_PingResult); isPR && !pr.PingResult.Failed {
			ok = true
		}
	}
	if !ok {
		t.Error("expected a successful dmsg-only ping result")
	}
}

// --- Bandwidth dmsg dial error ---------------------------------------

func TestStreamDmsgBandwidthTestDialError(t *testing.T) {
	fv := newFakeVisor()
	fv.dialDmsgPing = func(cipher.PubKey) error { return errors.New("no dmsg") }
	client, cleanup := newTestServer(t, fv)
	defer cleanup()
	err := client.StreamDmsgBandwidthTest(context.Background(), validPK(t), 50*time.Millisecond, 0,
		func(uint64, uint64, time.Duration, float64, float64, bool, error) {})
	if err == nil {
		t.Fatal("expected dmsg dial error to propagate")
	}
}

// --- StreamRemoteSystemStats proxy loop ------------------------------

// pipeListener hands a single pre-connected net.Conn to grpc.Server.
// Accept blocks (returns the closed error) once the conn is consumed.
type pipeListener struct {
	ch     chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.closed:
		return nil, errors.New("listener closed")
	}
}
func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}
func (l *pipeListener) Addr() net.Addr { return dummyAddr{} }

type dummyAddr struct{}

func (dummyAddr) Network() string { return "pipe" }
func (dummyAddr) String() string  { return "pipe" }

func TestStreamRemoteSystemStatsProxy(t *testing.T) {
	// Remote visor: a real second PingServer reachable only over an
	// in-memory pipe. The local visor's DialDmsgRPC returns the client
	// end of that pipe, so StreamRemoteSystemStats dials a gRPC client
	// over it and proxies the remote's StreamSystemStats back out.
	serverConn, clientConn := net.Pipe()

	lis := &pipeListener{ch: make(chan net.Conn, 1), closed: make(chan struct{})}
	lis.ch <- serverConn
	remoteSrv := grpc.NewServer()
	RegisterPingServiceServer(remoteSrv, NewPingServer(newFakeVisor(), logging.MustGetLogger("remote")))
	go func() { _ = remoteSrv.Serve(lis) }() //nolint
	defer remoteSrv.Stop()

	fv := newFakeVisor()
	fv.dialDmsgRPC = func(cipher.PubKey) (net.Conn, error) { return clientConn, nil }
	client, cleanup := newTestServer(t, fv)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan *SystemStats, 4)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = client.StreamRemoteSystemStats(ctx, validPK(t), 30*time.Millisecond, false, 0, func(s *SystemStats) { //nolint
			got <- s
		})
	}()

	select {
	case s := <-got:
		if s == nil {
			t.Error("received nil stats over the proxy")
		}
		cancel() // one proxied stat is enough to prove the loop works
	case <-time.After(5 * time.Second):
		t.Fatal("no stats proxied from the remote visor")
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("StreamRemoteSystemStats did not return after cancel")
	}
}
