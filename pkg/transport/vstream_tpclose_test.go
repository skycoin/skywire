// Package transport pkg/transport/vstream_tpclose_test.go c2-net-transport
package transport

import (
	"io"
	"testing"

	"github.com/google/uuid"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// TestVStreamMux_TransportCloseClosesItsStreams is the regression for the
// app_direct mux reporting dozens of live streams on a visor running no direct
// app at all.
//
// A stream leaves m.streams only through VStream.Close: the local reader calls
// it, or the peer's FIN does. A transport that goes away produces neither — the
// peer cannot send a FIN over a dead link, and the local reader is parked in
// Read on a queue nothing will ever write to again. So every stream that rode a
// closed transport stayed registered for the life of the visor, holding its
// 1024-frame queue, and its reader never saw EOF.
func TestVStreamMux_TransportCloseClosesItsStreams(t *testing.T) {
	tm := newTestManager(t)
	mt, _ := servingTransport(t, tm, mustPK(t), types.STCPR)
	tm.track(mt)                          // what SaveTransport/acceptTransport do for a real transport
	mt.queueDeletion = func(uuid.UUID) {} // batch-delete mode: no TPD round-trip in a unit test
	mux := NewVStreamMux(tm, routing.AppDirectPacket, logging.MustGetLogger("vstream-tpclose-test"))

	s, err := mux.DialOnTransportAs(mt, "skysocks-client")
	require.NoError(t, err)
	require.Equal(t, 1, mux.Stats().Streams)

	mt.closeWith("test: the link went away")

	require.Zero(t, mux.Stats().Streams, "a stream over a closed transport must not stay registered")
	require.Empty(t, mux.StreamInfo(""))

	// The reader has to be released too: without the EOF the app above never
	// learns the session is over and holds its own resources forever.
	n, err := s.Read(make([]byte, 8))
	require.Zero(t, n)
	require.ErrorIs(t, err, io.EOF)

	// Closing the stream afterwards is still safe (shared sync.Once).
	require.NoError(t, s.Close())
}

// Only the closed transport's streams go: a mux carries streams over many
// transports and one dying must not take the rest with it.
func TestVStreamMux_TransportCloseSparesOtherTransports(t *testing.T) {
	tm := newTestManager(t)
	dying, _ := servingTransport(t, tm, mustPK(t), types.STCPR)
	survivor, _ := servingTransport(t, tm, mustPK(t), types.STCPR)
	tm.track(dying)
	tm.track(survivor)
	dying.queueDeletion = func(uuid.UUID) {}
	mux := NewVStreamMux(tm, routing.AppDirectPacket, logging.MustGetLogger("vstream-tpclose-test"))

	_, err := mux.DialOnTransportAs(dying, "skysocks-client")
	require.NoError(t, err)
	kept, err := mux.DialOnTransportAs(survivor, "skysocks-client")
	require.NoError(t, err)
	require.Equal(t, 2, mux.Stats().Streams)

	dying.closeWith("test: one link of two")

	infos := mux.StreamInfo("")
	require.Len(t, infos, 1)
	require.Equal(t, survivor.Entry.ID, infos[0].TpID)
	require.Equal(t, kept.id, infos[0].StreamID)
}

// The read-buffer occupancy is what says whether a slow direct session is the
// path or the reader: deliver() blocks the whole transport's read loop once a
// stream's queue fills, and gives the stream up after vstreamStallTimeout.
func TestVStreamMux_ReadBufOccupancyIsReported(t *testing.T) {
	tm := newTestManager(t)
	mt, _ := servingTransport(t, tm, mustPK(t), types.STCPR)
	mux := NewVStreamMux(tm, routing.AppDirectPacket, logging.MustGetLogger("vstream-readbuf-test"))

	s, err := mux.DialOnTransportAs(mt, "skysocks-client")
	require.NoError(t, err)
	defer s.Close() //nolint:errcheck

	info := mux.StreamInfo("")[0]
	require.Zero(t, info.ReadBufLen, "a fresh stream has drained nothing because nothing arrived")
	require.Equal(t, vstreamReadBuf, info.ReadBufCap)

	stats := mux.Stats()
	require.Equal(t, vstreamReadBuf, stats.ReadBufCap)
	require.Zero(t, stats.ReadBufMaxLen)
	require.Zero(t, stats.ReadBufNearFull)

	// Queue frames the reader does not take.
	// Round UP to the first depth that actually crosses the 90% line.
	const queued = (vstreamReadBuf*readBufNearFullNum + readBufNearFullDen - 1) / readBufNearFullDen
	for i := 0; i < queued; i++ {
		mux.deliver(s, []byte("frame"))
	}

	require.Equal(t, queued, mux.StreamInfo("")[0].ReadBufLen)
	stats = mux.Stats()
	require.Equal(t, queued, stats.ReadBufMaxLen)
	require.Equal(t, 1, stats.ReadBufNearFull, "a queue at 90% is what the aggregate is for")

	// Draining one frame takes it back under the threshold, so the aggregate
	// tracks the live depth rather than a high-water mark.
	_, err = s.Read(make([]byte, 5))
	require.NoError(t, err)
	stats = mux.Stats()
	require.Equal(t, queued-1, stats.ReadBufMaxLen)
	require.Zero(t, stats.ReadBufNearFull)
}
