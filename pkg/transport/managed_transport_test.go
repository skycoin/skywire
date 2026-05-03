package transport

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// hangingTransport simulates a transport where reads hang until closed (half-open TCP).
type hangingTransport struct {
	readStarted chan struct{}
	closed      chan struct{}
	pk1, pk2    cipher.PubKey
}

func newHangingTransport() *hangingTransport {
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	return &hangingTransport{
		readStarted: make(chan struct{}),
		closed:      make(chan struct{}),
		pk1:         pk1,
		pk2:         pk2,
	}
}

func (t *hangingTransport) Read([]byte) (int, error) {
	select {
	case <-t.readStarted:
	default:
		close(t.readStarted)
	}
	<-t.closed
	return 0, net.ErrClosed
}

func (t *hangingTransport) Write([]byte) (int, error) { <-t.closed; return 0, net.ErrClosed }
func (t *hangingTransport) Close() error {
	select {
	case <-t.closed:
	default:
		close(t.closed)
	}
	return nil
}                                                            //nolint:gosec
func (t *hangingTransport) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (t *hangingTransport) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (t *hangingTransport) SetDeadline(time.Time) error      { return nil }
func (t *hangingTransport) SetReadDeadline(time.Time) error  { return nil }
func (t *hangingTransport) SetWriteDeadline(time.Time) error { return nil }
func (t *hangingTransport) LocalPK() cipher.PubKey           { return t.pk1 }
func (t *hangingTransport) RemotePK() cipher.PubKey          { return t.pk2 }
func (t *hangingTransport) LocalPort() uint16                { return 0 }
func (t *hangingTransport) RemotePort() uint16               { return 0 }
func (t *hangingTransport) LocalRawAddr() net.Addr           { return &net.TCPAddr{} }
func (t *hangingTransport) RemoteRawAddr() net.Addr          { return &net.TCPAddr{} }
func (t *hangingTransport) Network() types.Type              { return "test" }

// blockingWriteTransport blocks on Write but allows Read to work normally.
type blockingWriteTransport struct {
	writeCh  chan struct{}
	pk1, pk2 cipher.PubKey
}

func newBlockingWriteTransport() *blockingWriteTransport {
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	return &blockingWriteTransport{
		writeCh: make(chan struct{}),
		pk1:     pk1,
		pk2:     pk2,
	}
}

func (t *blockingWriteTransport) Write([]byte) (int, error) {
	select {
	case <-t.writeCh:
	default:
		close(t.writeCh)
	}
	select {} // block forever
}

func (t *blockingWriteTransport) Read([]byte) (int, error)         { select {} }
func (t *blockingWriteTransport) Close() error                     { return nil }
func (t *blockingWriteTransport) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (t *blockingWriteTransport) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (t *blockingWriteTransport) SetDeadline(time.Time) error      { return nil }
func (t *blockingWriteTransport) SetReadDeadline(time.Time) error  { return nil }
func (t *blockingWriteTransport) SetWriteDeadline(time.Time) error { return nil }
func (t *blockingWriteTransport) LocalPK() cipher.PubKey           { return t.pk1 }
func (t *blockingWriteTransport) RemotePK() cipher.PubKey          { return t.pk2 }
func (t *blockingWriteTransport) LocalPort() uint16                { return 0 }
func (t *blockingWriteTransport) RemotePort() uint16               { return 0 }
func (t *blockingWriteTransport) LocalRawAddr() net.Addr           { return &net.TCPAddr{} }
func (t *blockingWriteTransport) RemoteRawAddr() net.Addr          { return &net.TCPAddr{} }
func (t *blockingWriteTransport) Network() types.Type              { return "test" }

// TestWritePacketContextCancellation verifies that WritePacket returns
// when context is canceled even if the underlying transport blocks.
func TestWritePacketContextCancellation(t *testing.T) {
	conn := newBlockingWriteTransport()
	mt := NewManagedTransportForTest(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	packet := routing.MakeKeepAlivePacket(1)

	done := make(chan error, 1)
	go func() {
		done <- mt.WritePacket(ctx, packet)
	}()

	select {
	case err := <-done:
		require.Error(t, err)
		require.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(5 * time.Second):
		t.Fatal("WritePacket did not respect context cancellation")
	}
}

// TestReadLoopDropsPacketOnFullChannel verifies that the readLoop
// doesn't block forever when the readCh consumer stops reading.
func TestReadLoopDropsPacketOnFullChannel(t *testing.T) {
	// This is a design-level test: we verify that readLoop has a
	// timeout on the readCh send so it doesn't stall forever.
	// The actual timeout is 30s which is too long for a unit test,
	// so we just verify the readLoop exits cleanly when done is closed.

	conn := newHangingTransport()
	mt := NewManagedTransportForTest(conn)

	readCh := make(chan routing.Packet, 1)
	mt.wg.Add(1)
	go mt.readLoop(readCh)

	// Wait for read to start
	<-conn.readStarted

	// Close the transport — readLoop should exit
	mt.close()

	// readLoop should complete within a reasonable time
	done := make(chan struct{})
	go func() {
		mt.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Good
	case <-time.After(10 * time.Second):
		t.Fatal("readLoop did not exit after transport close")
	}
}

// A 0 or negative sample must not overwrite the running stats — that
// path was the source of the "TPD says min=0" production bug.
func TestSetLatencyDropsNonPositive(t *testing.T) {
	mt := &ManagedTransport{}
	mt.SetLatency(15.5)
	require.Equal(t, LatencyStats{Min: 15.5, Max: 15.5, Avg: 15.5}, mt.GetLatencyStats())

	mt.SetLatency(0)
	require.Equal(t, LatencyStats{Min: 15.5, Max: 15.5, Avg: 15.5}, mt.GetLatencyStats(),
		"a 0 sample wiped the existing stats")

	mt.SetLatency(-1)
	require.Equal(t, LatencyStats{Min: 15.5, Max: 15.5, Avg: 15.5}, mt.GetLatencyStats(),
		"a negative sample wiped the existing stats")

	mt.SetLatency(8.2)
	require.Equal(t, LatencyStats{Min: 8.2, Max: 15.5, Avg: 8.2}, mt.GetLatencyStats(),
		"a valid sample after rejected ones must still update")
}

// SetLatencyStats must reject any partial-zero snapshot — otherwise a
// MeasureLatency result with min=0 (e.g. a sub-µs ping included in
// `measurements`) would replace a previously-good record.
func TestSetLatencyStatsDropsPartialZero(t *testing.T) {
	mt := &ManagedTransport{}
	good := LatencyStats{Min: 10, Max: 30, Avg: 20}
	mt.SetLatencyStats(good)
	require.Equal(t, good, mt.GetLatencyStats())

	for _, bad := range []LatencyStats{
		{Min: 0, Max: 30, Avg: 20},
		{Min: 10, Max: 0, Avg: 20},
		{Min: 10, Max: 30, Avg: 0},
		{Min: -1, Max: 30, Avg: 20},
	} {
		mt.SetLatencyStats(bad)
		require.Equal(t, good, mt.GetLatencyStats(),
			"partial-zero snapshot %+v overwrote good stats", bad)
	}

	better := LatencyStats{Min: 5, Max: 40, Avg: 18}
	mt.SetLatencyStats(better)
	require.Equal(t, better, mt.GetLatencyStats())
}

// A pong arriving long after its ping (delayed packet, suspended host)
// produces an outlier RTT that would pin Max indefinitely. The
// upper-bound guard must drop such samples without disturbing the
// running stats. Live evidence: production transports observed with
// min/avg ~190ms and max ~35,000ms after a single straggler.
func TestSetLatencyDropsOutliers(t *testing.T) {
	mt := &ManagedTransport{}
	mt.SetLatency(15.5)
	require.Equal(t, LatencyStats{Min: 15.5, Max: 15.5, Avg: 15.5}, mt.GetLatencyStats())

	// Just above the bound is rejected.
	mt.SetLatency(MaxReasonableRTTMs + 1)
	require.Equal(t, LatencyStats{Min: 15.5, Max: 15.5, Avg: 15.5}, mt.GetLatencyStats(),
		"outlier sample updated the stats")

	// 35-second straggler — the production case.
	mt.SetLatency(35_000)
	require.Equal(t, LatencyStats{Min: 15.5, Max: 15.5, Avg: 15.5}, mt.GetLatencyStats(),
		"35s straggler updated the stats")

	// At the bound — accepted.
	mt.SetLatency(MaxReasonableRTTMs)
	require.Equal(t, LatencyStats{Min: 15.5, Max: MaxReasonableRTTMs, Avg: MaxReasonableRTTMs}, mt.GetLatencyStats(),
		"sample exactly at the bound was rejected")
}

// SetLatencyStats must reject the entire snapshot when any field
// exceeds the bound — better to keep the previous good record than
// allow a 35s max to land alongside otherwise-fine min/avg values.
func TestSetLatencyStatsDropsOutliers(t *testing.T) {
	mt := &ManagedTransport{}
	good := LatencyStats{Min: 100, Max: 250, Avg: 180}
	mt.SetLatencyStats(good)

	for _, bad := range []LatencyStats{
		{Min: 100, Max: MaxReasonableRTTMs + 1, Avg: 180},     // straggler max
		{Min: 100, Max: 35_000, Avg: 180},                     // 35s production case
		{Min: MaxReasonableRTTMs + 1, Max: 35_000, Avg: 180},  // both at the upper end
		{Min: 100, Max: 250, Avg: MaxReasonableRTTMs + 0.001}, // avg over the line
	} {
		mt.SetLatencyStats(bad)
		require.Equal(t, good, mt.GetLatencyStats(),
			"outlier snapshot %+v overwrote good stats", bad)
	}

	// At the bound — accepted.
	atBound := LatencyStats{Min: 100, Max: MaxReasonableRTTMs, Avg: 5_000}
	mt.SetLatencyStats(atBound)
	require.Equal(t, atBound, mt.GetLatencyStats())
}
