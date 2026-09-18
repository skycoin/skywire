// Package transport pkg/transport/managed_transport_writelock_test.go c1-net-tp
package transport

import (
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// gatedWriteTransport reads from its net.Pipe end like pipeTransport, but its
// Write parks until the gate is released — a slow or wedged conn write.
type gatedWriteTransport struct {
	*pipeTransport
	gate    chan struct{} // released to let blocked writes finish
	entered chan struct{} // closed once a write is in flight
	once    sync.Once
}

func (t *gatedWriteTransport) Write(p []byte) (int, error) {
	t.once.Do(func() { close(t.entered) })
	<-t.gate
	return len(p), nil
}

// WritePacket used to hold transportMx for the whole underlying write, and
// readPacket takes that same mutex on every read (through getTransport), so on
// a routed path every read serialized behind every in-flight write. A packet
// arriving while a write is parked must still reach the router.
func TestSlowWriteDoesNotBlockRead(t *testing.T) {
	tm := newTestManager(t)
	remote := mustPK(t)
	fc := &fakeClient{pk: tm.Conf.PubKey, sk: tm.Conf.SecKey, typ: types.STCPR}
	mt := NewManagedTransport(ManagedTransportConfig{
		client:   fc,
		DC:       NewNoopDiscoveryClient(),
		LS:       InMemoryTransportLogStore(),
		RemotePK: remote,
	})

	near, far := net.Pipe()
	defer far.Close() //nolint:errcheck
	conn := &gatedWriteTransport{
		pipeTransport: &pipeTransport{Conn: near, lpk: tm.Conf.PubKey, rpk: remote, nw: types.STCPR},
		gate:          make(chan struct{}),
		entered:       make(chan struct{}),
	}
	mt.setTransport(conn)
	tm.InjectTransportForTest(mt)

	// A write that will not finish until the gate is released. Started before
	// Serve so the read loop reaches getTransport with the write in flight —
	// exactly the ordering the old lock lost.
	out, err := routing.MakeDataPacket(routing.RouteID(1), []byte("parked write"))
	require.NoError(t, err)
	wdone := make(chan error, 1)
	go func() { wdone <- mt.WritePacket(context.Background(), out) }()
	select {
	case <-conn.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("the write never reached the underlying conn")
	}

	readCh := make(chan routing.Packet, 8)
	go mt.Serve(readCh)

	in, err := routing.MakeDataPacket(routing.RouteID(2), []byte("inbound while the write is parked"))
	require.NoError(t, err)
	go func() { _, _ = far.Write(in) }() //nolint:errcheck

	select {
	case got := <-readCh:
		require.Equal(t, in, got)
	case <-time.After(3 * time.Second):
		t.Fatal("an inbound packet never reached readCh: the read serialized behind the parked write")
	}

	close(conn.gate)
	require.NoError(t, <-wdone)
	mt.close()
}

// writeSem is what keeps frames whole now that transportMx is not held across
// the write: two concurrent writers must never interleave their bytes.
func TestConcurrentWritesDoNotInterleave(t *testing.T) {
	tm := newTestManager(t)
	remote := mustPK(t)
	fc := &fakeClient{pk: tm.Conf.PubKey, sk: tm.Conf.SecKey, typ: types.STCPR}
	mt := NewManagedTransport(ManagedTransportConfig{
		client:   fc,
		DC:       NewNoopDiscoveryClient(),
		LS:       InMemoryTransportLogStore(),
		RemotePK: remote,
	})
	conn := &interleaveCheckTransport{t: t, lpk: tm.Conf.PubKey, rpk: remote}
	mt.setTransport(conn)
	tm.InjectTransportForTest(mt)

	pkt, err := routing.MakeDataPacket(routing.RouteID(3), []byte("frame"))
	require.NoError(t, err)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			require.NoError(t, mt.WritePacket(context.Background(), pkt))
		}()
	}
	wg.Wait()
	require.Equal(t, int32(32), conn.writes.Load())
	mt.close()
}

// interleaveCheckTransport fails the test if a second Write starts while one
// is still running.
type interleaveCheckTransport struct {
	t        *testing.T
	inWrite  atomic.Bool
	writes   atomic.Int32
	lpk, rpk cipher.PubKey
}

func (t *interleaveCheckTransport) Read([]byte) (int, error) { return 0, io.EOF }
func (t *interleaveCheckTransport) Write(p []byte) (int, error) {
	if !t.inWrite.CompareAndSwap(false, true) {
		t.t.Error("two writes were in flight on the same conn at once")
	}
	time.Sleep(time.Millisecond)
	t.writes.Add(1)
	t.inWrite.Store(false)
	return len(p), nil
}
func (t *interleaveCheckTransport) Close() error                     { return nil }
func (t *interleaveCheckTransport) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (t *interleaveCheckTransport) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (t *interleaveCheckTransport) SetDeadline(time.Time) error      { return nil }
func (t *interleaveCheckTransport) SetReadDeadline(time.Time) error  { return nil }
func (t *interleaveCheckTransport) SetWriteDeadline(time.Time) error { return nil }
func (t *interleaveCheckTransport) LocalPK() cipher.PubKey           { return t.lpk }
func (t *interleaveCheckTransport) RemotePK() cipher.PubKey          { return t.rpk }
func (t *interleaveCheckTransport) LocalPort() uint16                { return 0 }
func (t *interleaveCheckTransport) RemotePort() uint16               { return 0 }
func (t *interleaveCheckTransport) LocalRawAddr() net.Addr           { return &net.TCPAddr{} }
func (t *interleaveCheckTransport) RemoteRawAddr() net.Addr          { return &net.TCPAddr{} }
func (t *interleaveCheckTransport) Network() types.Type              { return types.STCPR }

// The write slot's acquisition is ctx-aware, and a caller's deadline is
// charged to the conn.
//
// The deadline used to be set only AFTER the slot was won ("time spent waiting
// for another writer is not charged against this write"), and the wait itself
// was a plain mutex, so a caller behind a writer parked on a wedged conn had
// no way out short of that writer's own one-minute deadline. That is how a
// single stuck transit peer parked the router's inbound packet loop on
// 2026-09-18. A caller that says how long it will wait now gets exactly that.
func TestWriteWaiterGivesUpOnItsOwnDeadline(t *testing.T) {
	tm := newTestManager(t)
	remote := mustPK(t)
	fc := &fakeClient{pk: tm.Conf.PubKey, sk: tm.Conf.SecKey, typ: types.STCPR}
	mt := NewManagedTransport(ManagedTransportConfig{
		client:   fc,
		DC:       NewNoopDiscoveryClient(),
		LS:       InMemoryTransportLogStore(),
		RemotePK: remote,
	})

	near, far := net.Pipe()
	defer far.Close() //nolint:errcheck
	conn := &gatedWriteTransport{
		pipeTransport: &pipeTransport{Conn: near, lpk: tm.Conf.PubKey, rpk: remote, nw: types.STCPR},
		gate:          make(chan struct{}),
		entered:       make(chan struct{}),
	}
	mt.setTransport(conn)
	tm.InjectTransportForTest(mt)

	pkt, err := routing.MakeDataPacket(routing.RouteID(1), []byte("parked write"))
	require.NoError(t, err)
	wdone := make(chan error, 1)
	go func() { wdone <- mt.WritePacket(context.Background(), pkt) }()
	select {
	case <-conn.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("the first write never reached the underlying conn")
	}

	// A second writer with 150 ms of patience must get its own answer back in
	// about that long, not in a minute.
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = mt.WritePacket(ctx, pkt)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(start), 5*time.Second,
		"a waiter inherited the parked writer's deadline instead of its own")

	close(conn.gate)
	require.NoError(t, <-wdone)
	mt.close()
}

// A caller's deadline is the one charged to the conn when it is the shorter of
// the two: writeDeadline never hands out more time than the caller asked for,
// and never more than the transport's own ceiling.
func TestWriteDeadlineTakesTheSmaller(t *testing.T) {
	require.WithinDuration(t, time.Now().Add(writeTimeout), writeDeadline(context.Background()), time.Second)

	short, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.WithinDuration(t, time.Now().Add(2*time.Second), writeDeadline(short), 500*time.Millisecond)

	long, cancelLong := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancelLong()
	require.WithinDuration(t, time.Now().Add(writeTimeout), writeDeadline(long), time.Second)
}
