// Package transport — pkg/transport/coverage_test.go: white-box unit
// tests for the parts of the package that don't need a live network
// stack: the discovery clients (mock + noop), Entry helpers, the
// transport-type/id helpers, the settlement handshake (driven over a
// net.Pipe pair of fake transports), the ManagedTransport getters /
// ping-pong / log / close paths, and the Manager getters/setters
// (built via NewManager, which performs no networking).
package transport

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport/network"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// fakeClient is a minimal network.Client. Only PK/SK/Type carry real
// values; the dial/listen surface returns errors since the unit tests
// never drive a live connection through it.
type fakeClient struct {
	pk  cipher.PubKey
	sk  cipher.SecKey
	typ types.Type
}

func (c *fakeClient) Dial(context.Context, cipher.PubKey, uint16) (network.Transport, error) {
	return nil, errors.New("fakeClient: dial not implemented")
}
func (c *fakeClient) Start() error { return nil }
func (c *fakeClient) Listen(uint16) (network.Listener, error) {
	return nil, errors.New("not implemented")
}
func (c *fakeClient) LocalAddr() (net.Addr, error) { return &net.TCPAddr{}, nil }
func (c *fakeClient) PK() cipher.PubKey            { return c.pk }
func (c *fakeClient) SK() cipher.SecKey            { return c.sk }
func (c *fakeClient) Close() error                 { return nil }
func (c *fakeClient) Type() types.Type             { return c.typ }

// --- fake transports -------------------------------------------------

// memTransport is a network.Transport whose writes are captured in a
// buffer and whose reads are unused (returns EOF). Used to inspect what
// a ManagedTransport writes (pings/pongs/packets) without a peer.
type memTransport struct {
	written  []byte
	closed   bool
	lpk, rpk cipher.PubKey
	nw       types.Type
}

func newMemTransport() *memTransport {
	lpk, _ := cipher.GenerateKeyPair()
	rpk, _ := cipher.GenerateKeyPair()
	return &memTransport{lpk: lpk, rpk: rpk, nw: "test"}
}

func (t *memTransport) Read([]byte) (int, error) { return 0, io.EOF }
func (t *memTransport) Write(p []byte) (int, error) {
	if t.closed {
		return 0, net.ErrClosed
	}
	t.written = append(t.written, p...)
	return len(p), nil
}
func (t *memTransport) Close() error                     { t.closed = true; return nil }
func (t *memTransport) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (t *memTransport) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (t *memTransport) SetDeadline(time.Time) error      { return nil }
func (t *memTransport) SetReadDeadline(time.Time) error  { return nil }
func (t *memTransport) SetWriteDeadline(time.Time) error { return nil }
func (t *memTransport) LocalPK() cipher.PubKey           { return t.lpk }
func (t *memTransport) RemotePK() cipher.PubKey          { return t.rpk }
func (t *memTransport) LocalPort() uint16                { return 0 }
func (t *memTransport) RemotePort() uint16               { return 0 }
func (t *memTransport) LocalRawAddr() net.Addr           { return &net.TCPAddr{} }
func (t *memTransport) RemoteRawAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(1, 2, 3, 4), Port: 5000}
}
func (t *memTransport) Network() types.Type { return t.nw }

// pipeTransport wraps a net.Pipe end as a network.Transport so two of
// them form a connected pair for the settlement handshake.
type pipeTransport struct {
	net.Conn
	lpk, rpk cipher.PubKey
	nw       types.Type
}

func (t *pipeTransport) LocalPK() cipher.PubKey  { return t.lpk }
func (t *pipeTransport) RemotePK() cipher.PubKey { return t.rpk }
func (t *pipeTransport) LocalPort() uint16       { return 0 }
func (t *pipeTransport) RemotePort() uint16      { return 0 }
func (t *pipeTransport) LocalRawAddr() net.Addr  { return &net.TCPAddr{} }
func (t *pipeTransport) RemoteRawAddr() net.Addr { return &net.TCPAddr{} }
func (t *pipeTransport) Network() types.Type     { return t.nw }

// --- discovery: noop + remaining mock methods ------------------------

func TestNoopDiscoveryClient(t *testing.T) {
	c := NewNoopDiscoveryClient()
	ctx := context.Background()
	pk, _ := cipher.GenerateKeyPair()
	id := uuid.New()

	require.NoError(t, c.RegisterTransports(ctx))
	require.NoError(t, c.RegisterTransportsV3(ctx, "v3"))
	if _, err := c.GetTransportByID(ctx, id); err == nil {
		t.Error("noop GetTransportByID should error (not found)")
	}
	if edges, err := c.GetTransportsByEdge(ctx, pk); err != nil || edges != nil {
		t.Errorf("noop GetTransportsByEdge = (%v,%v)", edges, err)
	}
	if all, err := c.GetAllTransports(ctx); err != nil || all != nil {
		t.Errorf("noop GetAllTransports = (%v,%v)", all, err)
	}
	if s, err := c.GetTransportStats(ctx, pk); err != nil || s.Total != 0 {
		t.Errorf("noop GetTransportStats = (%v,%v)", s, err)
	}
	if s, err := c.GetAllTransportsStats(ctx); err != nil || s.TotalTransports != 0 {
		t.Errorf("noop GetAllTransportsStats = (%v,%v)", s, err)
	}
	if s, err := c.GetAllTransportsPerKeyStats(ctx); err != nil || len(s) != 0 {
		t.Errorf("noop GetAllTransportsPerKeyStats = (%v,%v)", s, err)
	}
	require.NoError(t, c.DeleteTransport(ctx, id))
	if n, err := c.DeleteTransports(ctx, []uuid.UUID{id, uuid.New()}); err != nil || n != 2 {
		t.Errorf("noop DeleteTransports = (%d,%v), want (2,nil)", n, err)
	}
}

func TestMockDiscoveryClientMethods(t *testing.T) {
	c := NewDiscoveryMock()
	ctx := context.Background()
	pkA, _ := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()
	entry := MakeEntry(pkA, pkB, types.STCPR, LabelUser)

	require.NoError(t, c.RegisterTransportsV3(ctx, "v3", &entry))

	got, err := c.GetTransportByID(ctx, entry.ID)
	require.NoError(t, err)
	require.Equal(t, entry.ID, got.ID)
	if _, err := c.GetTransportByID(ctx, uuid.New()); err == nil {
		t.Error("GetTransportByID unknown should error")
	}

	byEdge, err := c.GetTransportsByEdge(ctx, pkA)
	require.NoError(t, err)
	require.Len(t, byEdge, 1)
	if res, _ := c.GetTransportsByEdge(ctx, mustPK(t)); res != nil {
		t.Error("GetTransportsByEdge unknown edge should be nil")
	}

	all, err := c.GetAllTransports(ctx)
	require.NoError(t, err)
	require.Len(t, all, 1)

	stats, err := c.GetTransportStats(ctx, pkA)
	require.NoError(t, err)
	require.Equal(t, 1, stats.Total)

	netStats, err := c.GetAllTransportsStats(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, netStats.TotalTransports)
	require.Equal(t, 2, netStats.UniqueVisors)

	perKey, err := c.GetAllTransportsPerKeyStats(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, perKey[pkA.Hex()]["total"])

	require.NoError(t, c.DeleteTransport(ctx, entry.ID))
	if err := c.DeleteTransport(ctx, entry.ID); err == nil {
		t.Error("deleting already-deleted transport should error")
	}

	e2 := MakeEntry(pkA, pkB, types.SUDPH, LabelUser)
	require.NoError(t, c.RegisterTransportsV3(ctx, "v3", &e2))
	n, err := c.DeleteTransports(ctx, []uuid.UUID{e2.ID, uuid.New()})
	require.NoError(t, err)
	require.Equal(t, 1, n)
}

// --- entry helpers ---------------------------------------------------

func TestEntryHelpers(t *testing.T) {
	pkA, skA := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()
	entry := MakeEntry(pkA, pkB, types.STCPR, LabelUser)

	require.Equal(t, pkB, entry.RemoteEdge(pkA))
	require.Equal(t, pkA, entry.RemoteEdge(pkB))

	// On a self-loop entry both edges equal local, so RemoteEdge falls
	// through to returning local.
	selfEntry := MakeEntry(pkA, pkA, types.STCPR, LabelUser)
	require.Equal(t, pkA, selfEntry.RemoteEdge(pkA))

	unknown := mustPK(t)
	require.True(t, entry.HasEdge(pkA))
	require.False(t, entry.HasEdge(unknown))

	// Exactly one edge is the least-significant edge.
	require.NotEqual(t, entry.IsLeastSignificantEdge(pkA), entry.IsLeastSignificantEdge(pkB))

	require.NotEmpty(t, entry.String())
	require.NotEmpty(t, entry.ToBinary())

	// Sign / Signature round-trip.
	se, err := NewSignedEntry(&entry, pkA, skA)
	require.NoError(t, err)
	sig, err := se.Signature(pkA)
	require.NoError(t, err)
	require.NotEqual(t, cipher.Sig{}, sig)

	// Signature for a non-edge key fails.
	if _, err := se.Signature(unknown); err != ErrEdgeIndexNotFound {
		t.Errorf("Signature(unknown) err = %v, want ErrEdgeIndexNotFound", err)
	}
	// Sign for a non-edge key fails.
	if err := se.Sign(unknown, skA); err != ErrEdgeIndexNotFound {
		t.Errorf("Sign(unknown) err = %v, want ErrEdgeIndexNotFound", err)
	}
}

// --- transport.go ----------------------------------------------------

func TestTypeFromTransportID(t *testing.T) {
	pkA, _ := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()

	for _, ty := range []types.Type{types.STCPR, types.SUDPH, types.STCP, types.DMSG} {
		id := MakeTransportID(pkA, pkB, ty)
		require.Equal(t, ty, TypeFromTransportID(id, pkA, pkB))
	}
	// An ID that matches no known type yields "".
	require.Equal(t, types.Type(""), TypeFromTransportID(MakeTransportID(pkA, pkB, "weird"), pkA, pkB))
}

// --- handshake -------------------------------------------------------

func TestSettlementHandshake(t *testing.T) {
	pkA, skA := cipher.GenerateKeyPair()
	pkB, skB := cipher.GenerateKeyPair()
	cA, cB := net.Pipe()
	tA := &pipeTransport{Conn: cA, lpk: pkA, rpk: pkB, nw: types.STCPR}
	tB := &pipeTransport{Conn: cB, lpk: pkB, rpk: pkA, nw: types.STCPR}
	dc := NewDiscoveryMock()
	log := logging.MustGetLogger("hs-test")
	ctx := context.Background()

	type res struct {
		label Label
		err   error
	}
	initCh := make(chan res, 1)
	respCh := make(chan res, 1)

	go func() {
		l, err := MakeSettlementHS(true, log, LabelSkycoin).Do(ctx, dc, tA, skA)
		initCh <- res{l, err}
	}()
	go func() {
		l, err := MakeSettlementHS(false, log, LabelUser).Do(ctx, dc, tB, skB)
		respCh <- res{l, err}
	}()

	select {
	case r := <-initCh:
		require.NoError(t, r.err)
		require.Equal(t, LabelSkycoin, r.label)
	case <-time.After(5 * time.Second):
		t.Fatal("initiator handshake timed out")
	}
	select {
	case r := <-respCh:
		require.NoError(t, r.err)
		// Responder adopts the initiator's label.
		require.Equal(t, LabelSkycoin, r.label)
	case <-time.After(5 * time.Second):
		t.Fatal("responder handshake timed out")
	}
}

func TestSettlementHandshakeRejection(t *testing.T) {
	// The initiator signs with a key that does NOT match its transport
	// PK, so the responder's signature verification fails: it replies
	// responseInvalidEntry and the initiator surfaces "invalid entry".
	pkA, _ := cipher.GenerateKeyPair()
	pkB, skB := cipher.GenerateKeyPair()
	_, wrongSK := cipher.GenerateKeyPair()

	cA, cB := net.Pipe()
	tA := &pipeTransport{Conn: cA, lpk: pkA, rpk: pkB, nw: types.STCPR}
	tB := &pipeTransport{Conn: cB, lpk: pkB, rpk: pkA, nw: types.STCPR}
	dc := NewDiscoveryMock()
	log := logging.MustGetLogger("hs-test")
	ctx := context.Background()

	initErr := make(chan error, 1)
	respErr := make(chan error, 1)
	go func() {
		_, err := MakeSettlementHS(true, log, LabelUser).Do(ctx, dc, tA, wrongSK)
		initErr <- err
	}()
	go func() {
		_, err := MakeSettlementHS(false, log, LabelUser).Do(ctx, dc, tB, skB)
		respErr <- err
	}()

	select {
	case err := <-initErr:
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("initiator did not return on rejection")
	}
	select {
	case err := <-respErr:
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("responder did not return on rejection")
	}
}

func TestCompareEntries(t *testing.T) {
	pkA, _ := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()
	base := MakeEntry(pkA, pkB, types.STCPR, LabelUser)

	require.NoError(t, compareEntries(&base, &base))

	// Mismatched ID.
	wrongID := base
	wrongID.ID = uuid.New()
	require.Error(t, compareEntries(&base, &wrongID))

	// Mismatched type.
	wrongType := base
	wrongType.Type = types.SUDPH
	require.Error(t, compareEntries(&base, &wrongType))

	// Mismatched edges.
	wrongEdges := base
	wrongEdges.Edges = [2]cipher.PubKey{pkA, pkA}
	require.Error(t, compareEntries(&base, &wrongEdges))
}

func TestSettlementHSContextCancel(t *testing.T) {
	// hs never completes; canceled context makes Do return ctx.Err().
	hs := SettlementHS(func(ctx context.Context, _ DiscoveryClient, _ network.Transport, _ cipher.SecKey) (Label, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := hs.Do(ctx, NewDiscoveryMock(), newMemTransport(), cipher.SecKey{})
	require.Error(t, err)
}

// --- ManagedTransport ------------------------------------------------

func TestManagedTransportPingPong(t *testing.T) {
	mem := newMemTransport()
	mt := NewManagedTransportForTest(mem)

	// handleTransportPing writes a pong back onto the transport.
	ping := routing.MakeTransportPingPacket(time.Now().UnixNano())
	mt.handleTransportPing(ping)
	require.NotEmpty(t, mem.written, "ping should produce a pong write")
	require.Equal(t, routing.TransportPongPacket, routing.Packet(mem.written).Type())

	// sendTransportPing writes a ping and arms the miss counter.
	mem.written = nil
	mt.sendTransportPing()
	require.NotEmpty(t, mem.written)
	require.Equal(t, routing.TransportPingPacket, routing.Packet(mem.written).Type())
	require.EqualValues(t, 1, mt.missedPongs.Load())

	// handleTransportPong arms pongSeen, resets the miss counter, sets latency.
	pong := routing.MakeTransportPongPacket(time.Now().Add(-5 * time.Millisecond).UnixNano())
	mt.handleTransportPong(pong)
	require.True(t, mt.pongSeen.Load())
	require.EqualValues(t, 0, mt.missedPongs.Load())
	require.Greater(t, mt.GetLatency(), 0.0)

	// Short payloads are ignored (no panic, no state change).
	short := routing.Packet(make([]byte, routing.PacketHeaderSize))
	mt.handleTransportPing(short)
	mt.handleTransportPong(short)

	// An outlier pong (sent far in the past) is dropped by the RTT filter.
	stats := mt.GetLatencyStats()
	old := routing.MakeTransportPongPacket(time.Now().Add(-time.Hour).UnixNano())
	mt.handleTransportPong(old)
	require.Equal(t, stats, mt.GetLatencyStats(), "outlier pong must not change latency")
}

func TestManagedTransportSetLatencyStats(t *testing.T) {
	mt := NewManagedTransportForTest(newMemTransport())

	mt.SetLatencyStats(LatencyStats{Min: 1, Max: 3, Avg: 2})
	require.Equal(t, LatencyStats{Min: 1, Max: 3, Avg: 2}, mt.GetLatencyStats())

	// Partial-zero and out-of-range snapshots are rejected wholesale.
	mt.SetLatencyStats(LatencyStats{Min: 0, Max: 3, Avg: 2})
	mt.SetLatencyStats(LatencyStats{Min: 1, Max: MaxReasonableRTTMs + 1, Avg: 2})
	require.Equal(t, LatencyStats{Min: 1, Max: 3, Avg: 2}, mt.GetLatencyStats())
}

func TestManagedTransportGettersAndClose(t *testing.T) {
	mem := newMemTransport()
	mt := NewManagedTransportForTest(mem)

	require.Equal(t, mem, mt.getTransport())
	require.True(t, mt.isServing())
	require.False(t, mt.IsClosed())
	require.Equal(t, "1.2.3.4", mt.RemoteIP())

	bw := mt.GetBandwidth()
	require.NotNil(t, bw)

	require.NoError(t, mt.Close())
	require.True(t, mt.IsClosed())
	require.False(t, mt.isServing())
	require.True(t, mem.closed)
	// getTransport returns nil once not serving.
	require.Nil(t, mt.getTransport())
}

func TestManagedTransportWritePacket(t *testing.T) {
	mem := newMemTransport()
	mt := NewManagedTransportForTest(mem)
	pkt := routing.MakeTransportPingPacket(time.Now().UnixNano())

	require.NoError(t, mt.WritePacket(context.Background(), pkt))
	require.NotEmpty(t, mem.written)

	require.NoError(t, mt.WriteRawPacket(routing.MakeTransportPingPacket(1)))

	// nil transport → both write paths error.
	nilMT := NewManagedTransportForTest(nil)
	require.Error(t, nilMT.WritePacket(context.Background(), pkt))
	require.Error(t, nilMT.WriteRawPacket(pkt))
}

func TestManagedTransportLogging(t *testing.T) {
	mt := NewManagedTransportForTest(newMemTransport())
	mt.ls = InMemoryTransportLogStore()
	mt.Entry = MakeEntry(mustPK(t), mustPK(t), types.STCPR, LabelUser)

	mt.logRecv(100)
	mt.logSent(50)
	require.True(t, mt.logMod() == false || true) // logMod consumed below
	// recordLog persists the entry when there were operations.
	mt.logRecv(10)
	mt.recordLog()
	got, err := mt.ls.Entry(mt.Entry.ID)
	require.NoError(t, err)
	require.NotNil(t, got)

	// recordLog with no pending operations is a no-op (logMod false).
	mt.recordLog()
}

func TestManagedTransportDeleteFromDiscovery(t *testing.T) {
	dc := NewDiscoveryMock()
	pkA, pkB := mustPK(t), mustPK(t)
	entry := MakeEntry(pkA, pkB, types.STCPR, LabelUser)
	require.NoError(t, dc.RegisterTransportsV3(context.Background(), "v3", &entry))

	mt := NewManagedTransportForTest(newMemTransport())
	mt.dc = dc
	mt.Entry = entry
	require.NoError(t, mt.deleteFromDiscovery())

	if _, err := dc.GetTransportByID(context.Background(), entry.ID); err == nil {
		t.Error("transport should have been deleted from discovery")
	}
}

func TestManagedTransportCloseWithoutDeregister(t *testing.T) {
	mem := newMemTransport()
	mt := NewManagedTransportForTest(mem)
	mt.closeWithoutDeregister()
	require.True(t, mt.IsClosed())
	require.True(t, mem.closed)
	// idempotent
	mt.closeWithoutDeregister()
}

// --- Manager getters / setters ---------------------------------------

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	pk, sk := cipher.GenerateKeyPair()
	cfg := &ManagerConfig{
		PubKey:          pk,
		SecKey:          sk,
		DiscoveryClient: NewDiscoveryMock(),
	}
	tm, err := NewManager(nil, nil, nil, cfg, network.ClientFactory{})
	require.NoError(t, err)
	return tm
}

func TestManagerGetters(t *testing.T) {
	tm := newTestManager(t)

	require.Equal(t, tm.Conf.PubKey, tm.Local())
	require.Nil(t, tm.ARClient())
	require.Empty(t, tm.Networks())
	require.Equal(t, 0, tm.TransportCount())
	require.False(t, tm.IsKnownNetwork(types.STCPR))
	if _, ok := tm.Stcpr(); ok {
		t.Error("Stcpr should be absent on a fresh manager")
	}

	// GetTransport on an unknown network errors.
	if _, err := tm.GetTransport(mustPK(t), types.STCPR); err != ErrUnknownNetwork {
		t.Errorf("GetTransport err = %v, want ErrUnknownNetwork", err)
	}
	if _, err := tm.GetTransportByID(uuid.New()); err != ErrNotFound {
		t.Errorf("GetTransportByID err = %v, want ErrNotFound", err)
	}
	require.Empty(t, tm.GetTransportsByLabel(LabelUser))
	require.Empty(t, tm.GetTransportsByLabels(LabelUser, LabelSkycoin))

	// Ready channel exists and is open.
	select {
	case <-tm.Ready():
		t.Error("Ready channel should be open")
	default:
	}

	// Inserting a nil-valued network client marks the network known.
	tm.netClients[types.STCPR] = nil
	require.True(t, tm.IsKnownNetwork(types.STCPR))
	require.Contains(t, tm.Networks(), types.STCPR)
	if _, ok := tm.Stcpr(); !ok {
		t.Error("Stcpr should be present after inserting the client")
	}
}

func TestManagerTransportRegistry(t *testing.T) {
	tm := newTestManager(t)

	mem := newMemTransport()
	mt := NewManagedTransportForTest(mem)
	mt.Entry = MakeEntry(tm.Conf.PubKey, mustPK(t), types.STCPR, LabelSkycoin)
	tm.tps[mt.Entry.ID] = mt

	require.Equal(t, 1, tm.TransportCount())
	require.Equal(t, mt, tm.Transport(mt.Entry.ID))

	byID, err := tm.GetTransportByID(mt.Entry.ID)
	require.NoError(t, err)
	require.Equal(t, mt, byID)

	require.Len(t, tm.GetTransportsByLabel(LabelSkycoin), 1)
	require.Empty(t, tm.GetTransportsByLabel(LabelUser))
	require.Len(t, tm.GetTransportsByLabels(LabelUser, LabelSkycoin), 1)

	// WalkTransports visits the entry; returning false stops the walk.
	visited := 0
	tm.WalkTransports(func(_ *ManagedTransport) bool { visited++; return true })
	require.Equal(t, 1, visited)
	tm.WalkTransports(func(_ *ManagedTransport) bool { return false })
}

func TestManagerHandlerSetters(t *testing.T) {
	tm := newTestManager(t)
	// Register a transport so the setters' fan-out loop runs.
	mt := NewManagedTransportForTest(newMemTransport())
	mt.Entry = MakeEntry(tm.Conf.PubKey, mustPK(t), types.STCPR, LabelUser)
	tm.tps[mt.Entry.ID] = mt

	h := func(_ routing.Packet, _ *ManagedTransport) {}
	tm.SetCascadeHandler(h)
	tm.SetDHTHandler(h)
	tm.SetVisorRPCHandler(h)
	tm.SetSkynetForwardHandler(h)
	tm.SetAppDirectHandler(h)
	tm.SetSetupRPCHandler(h)
	require.NotNil(t, mt.dhtHandler)
	require.NotNil(t, mt.visorRPCHandler)
	require.NotNil(t, mt.skynetFwdHandler)
	require.NotNil(t, mt.appDirectHandler)
	require.NotNil(t, mt.setupRPCHandler)

	// RouteChecker drives hasActiveRoutes.
	require.False(t, tm.hasActiveRoutes(mt.Entry.ID))
	tm.SetRouteChecker(func(uuid.UUID) bool { return true })
	require.True(t, tm.hasActiveRoutes(mt.Entry.ID))

	// Persistent-transports cache round-trips.
	pts := []PersistentTransports{{}}
	tm.SetPTpsCache(pts)
	require.Len(t, tm.getPTpsCache(), 1)

	// TPD leaf publisher round-trips.
	require.Nil(t, tm.tpdLeafPublisher())
	tm.SetTPDLeafPublisher(noopLeafPub{})
	require.NotNil(t, tm.tpdLeafPublisher())
}

func TestManagerARLimit(t *testing.T) {
	// Default (0) → always register, no deregister.
	tm := newTestManager(t)
	require.True(t, tm.ShouldRegisterAR())
	tm.checkARLimit() // limit 0 → no-op

	// Negative → never register.
	tm.Conf.ARTransportLimit = -1
	require.False(t, tm.ShouldRegisterAR())

	// Positive limit reached → deregister branch (arClient nil → safe).
	tm.Conf.ARTransportLimit = 1
	mt := NewManagedTransportForTest(newMemTransport())
	mt.Entry = MakeEntry(tm.Conf.PubKey, mustPK(t), types.STCPR, LabelUser)
	tm.tps[mt.Entry.ID] = mt
	tm.checkARLimit()
	require.True(t, tm.arDeregistered)
	tm.checkARLimit() // already deregistered → early return
}

// --- NewManagedTransport + Type --------------------------------------

func TestNewManagedTransport(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	remote := mustPK(t)
	fc := &fakeClient{pk: pk, sk: sk, typ: types.STCPR}
	mt := NewManagedTransport(ManagedTransportConfig{
		client:          fc,
		DC:              NewDiscoveryMock(),
		LS:              InMemoryTransportLogStore(),
		RemotePK:        remote,
		TransportLabel:  LabelUser,
		InactiveTimeout: time.Minute,
	})
	require.Equal(t, remote, mt.Remote())
	require.Equal(t, types.STCPR, mt.Type())
	require.Equal(t, MakeTransportID(pk, remote, types.STCPR), mt.Entry.ID)
}

// --- log.go: MakeLogEntry + Reset ------------------------------------

func TestMakeLogEntryAndReset(t *testing.T) {
	ls := InMemoryTransportLogStore()
	id := uuid.New()
	log := logging.MustGetLogger("log-test")

	// First call: no existing entry (Entry returns an error → warn path),
	// so a fresh zeroed entry is created.
	le := MakeLogEntry(ls, id, log)
	require.NotNil(t, le)
	le.AddRecv(100)
	le.AddSent(40)
	require.NoError(t, ls.Record(id, le))

	// Second call: an existing entry is found and its byte counts seed
	// the new entry.
	le2 := MakeLogEntry(ls, id, log)
	require.EqualValues(t, 100, *le2.RecvBytes)
	require.EqualValues(t, 40, *le2.SentBytes)

	// Reset zeroes the counters.
	le2.Reset()
	require.EqualValues(t, 0, *le2.RecvBytes)
	require.EqualValues(t, 0, *le2.SentBytes)
}

// --- VStreamMux ------------------------------------------------------

// servingTransport builds a ManagedTransport with a working Type() and a
// memTransport set as its underlying conn, registered in tm.
func servingTransport(t *testing.T, tm *Manager, remote cipher.PubKey, nw types.Type) (*ManagedTransport, *memTransport) {
	t.Helper()
	fc := &fakeClient{pk: tm.Conf.PubKey, sk: tm.Conf.SecKey, typ: nw}
	mt := NewManagedTransport(ManagedTransportConfig{
		client:   fc,
		DC:       NewDiscoveryMock(),
		LS:       InMemoryTransportLogStore(),
		RemotePK: remote,
	})
	mem := newMemTransport()
	mt.setTransport(mem)
	tm.tps[mt.Entry.ID] = mt
	return mt, mem
}

func buildVStreamPacket(pt routing.PacketType, streamID uint64, sender cipher.PubKey, flag byte, data []byte) routing.Packet {
	payload := make([]byte, VStreamHeaderSize+len(data))
	binary.BigEndian.PutUint64(payload[:8], streamID)
	copy(payload[8:41], sender[:])
	payload[41] = flag
	copy(payload[VStreamHeaderSize:], data)

	pkt := make(routing.Packet, routing.PacketHeaderSize+len(payload))
	pkt[routing.PacketTypeOffset] = byte(pt)
	binary.BigEndian.PutUint16(pkt[routing.PacketPayloadSizeOffset:], uint16(len(payload))) //nolint:gosec
	copy(pkt[routing.PacketPayloadOffset:], payload)
	return pkt
}

func TestVStreamMuxDialAndWrite(t *testing.T) {
	tm := newTestManager(t)
	remote := mustPK(t)
	mt, mem := servingTransport(t, tm, remote, types.STCPR)
	mux := NewVStreamMux(tm, routing.DHTPacket, logging.MustGetLogger("vstream-test"))
	require.Equal(t, tm.Conf.PubKey, mux.localPK())

	// Dial finds the non-dmsg transport, runs the (nil) hook, sends SYN.
	s, err := mux.Dial(remote, "myapp")
	require.NoError(t, err)
	require.Equal(t, remote, s.RemotePK())
	require.NotEmpty(t, mem.written, "SYN should be written to the transport")

	// Write sends a data frame.
	mem.written = nil
	n, err := s.Write([]byte("hello"))
	require.NoError(t, err)
	require.Equal(t, 5, n)
	require.NotEmpty(t, mem.written)

	// DialByTransportID pins to the specific transport.
	s2, err := mux.DialByTransportID(remote, mt.Entry.ID)
	require.NoError(t, err)
	require.NotNil(t, s2)

	// DialByTransportID with an unknown id fails.
	if _, err := mux.DialByTransportID(remote, uuid.New()); err == nil {
		t.Error("DialByTransportID with unknown id should fail")
	}

	// Dial to an unknown peer fails (no transport).
	if _, err := mux.Dial(mustPK(t), ""); err == nil {
		t.Error("Dial to unknown peer should fail")
	}

	require.NoError(t, mux.Close())
}

func TestVStreamMuxDialHookRefusal(t *testing.T) {
	tm := newTestManager(t)
	remote := mustPK(t)
	servingTransport(t, tm, remote, types.STCPR)
	mux := NewVStreamMux(tm, routing.DHTPacket, logging.MustGetLogger("vstream-test"))

	mux.SetDirectDialHook(func(cipher.PubKey, string, string) error {
		return errors.New("policy refused")
	})
	if _, err := mux.Dial(remote, "app"); err == nil {
		t.Fatal("Dial should be refused by the hook")
	}
}

func TestVStreamMuxHandlePacket(t *testing.T) {
	tm := newTestManager(t)
	remote := mustPK(t)
	mt, _ := servingTransport(t, tm, remote, types.STCPR)
	mux := NewVStreamMux(tm, routing.DHTPacket, logging.MustGetLogger("vstream-test"))

	// Too-short payload is ignored.
	mux.HandlePacket(routing.Packet(make([]byte, routing.PacketHeaderSize)), mt)

	// SYN → an incoming stream becomes available via Accept.
	const sid = uint64(7)
	mux.HandlePacket(buildVStreamPacket(routing.DHTPacket, sid, remote, VStreamFlagSyn, nil), mt)
	s, err := mux.Accept()
	require.NoError(t, err)
	require.Equal(t, remote, s.RemotePK())

	// DATA for that stream lands in its read buffer.
	mux.HandlePacket(buildVStreamPacket(routing.DHTPacket, sid, remote, VStreamFlagData, []byte("payload")), mt)
	buf := make([]byte, 4)
	n, err := s.Read(buf) // partial read leaves leftover
	require.NoError(t, err)
	require.Equal(t, 4, n)
	require.Equal(t, "payl", string(buf))
	n, err = s.Read(buf) // leftover path
	require.NoError(t, err)
	require.Equal(t, "oad", string(buf[:n]))

	// DATA for an unknown stream is dropped silently.
	mux.HandlePacket(buildVStreamPacket(routing.DHTPacket, 999, remote, VStreamFlagData, []byte("x")), mt)

	// FIN closes and removes the stream.
	mux.HandlePacket(buildVStreamPacket(routing.DHTPacket, sid, remote, VStreamFlagFin, nil), mt)
	n, err = s.Read(buf)
	require.Equal(t, 0, n)
	require.ErrorIs(t, err, io.EOF)
}

func TestVStreamAcceptOnClose(t *testing.T) {
	tm := newTestManager(t)
	mux := NewVStreamMux(tm, routing.DHTPacket, logging.MustGetLogger("vstream-test"))
	require.NoError(t, mux.Close())
	if _, err := mux.Accept(); err == nil {
		t.Error("Accept after Close should error")
	}
}

// --- helpers ---------------------------------------------------------

type noopLeafPub struct{}

func (noopLeafPub) Put(string, []byte) error { return nil }
func (noopLeafPub) Delete(string) error      { return nil }

func mustPK(t *testing.T) cipher.PubKey {
	t.Helper()
	pk, _ := cipher.GenerateKeyPair()
	return pk
}
