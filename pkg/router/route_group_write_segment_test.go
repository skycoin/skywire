//go:build !tinygo || (js && wasm)

package router

import (
	"math"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

// capturedFrames returns every sequenced data frame written to any leg, ordered
// by sequence number, so a segmented write can be reassembled and checked.
func capturedFrames(conns []*capturingTransport) []routing.Packet {
	var frames []routing.Packet
	for _, c := range conns {
		c.mu.Lock()
		for _, p := range c.written {
			if p.Type() == routing.DataPacket {
				frames = append(frames, p)
			}
		}
		c.mu.Unlock()
	}
	sort.Slice(frames, func(i, j int) bool { return frames[i].SequenceNumber() < frames[j].SequenceNumber() })
	return frames
}

func patterned(n int) []byte {
	p := make([]byte, n)
	for i := range p {
		p[i] = byte(i*7 + i>>8)
	}
	return p
}

// A write larger than one routing packet can carry (uint16 payload length) must
// be split into consecutive frames, not rejected. net/rpc hands a >64 KiB gob
// reply to Write in one call once per-frame noise bypasses the stream-noise
// chunking, and the rejection stalled hypervisor RPC over skynet (#4484).
func TestRouteGroupWriteSegmentsOversizedPayload(t *testing.T) {
	rg, conns := createCapturingMuxRouteGroup(t)
	t.Cleanup(func() {
		for _, c := range conns {
			c.Close() //nolint:errcheck,gosec
		}
	})
	require.NoError(t, rg.SetWriteDeadline(time.Now().Add(5*time.Second)))

	payload := patterned(3*math.MaxUint16 + 1234)
	n, err := rg.Write(payload)
	require.NoError(t, err)
	require.Equal(t, len(payload), n)

	maxChunk := math.MaxUint16 - routing.SeqSize
	wantFrames := (len(payload) + maxChunk - 1) / maxChunk
	frames := capturedFrames(conns)
	require.Len(t, frames, wantFrames)
	require.Equal(t, uint32(wantFrames), rg.mux.writeSeqValue(), "one sequence number per frame, none burned") //nolint:gosec

	var got []byte
	for i, f := range frames {
		require.Equal(t, uint32(i), f.SequenceNumber(), "frames must be consecutive") //nolint:gosec
		require.LessOrEqual(t, int(f.Size()), math.MaxUint16)
		got = append(got, f.DataPayloadAfterSeq()...)
	}
	require.Equal(t, payload, got, "reassembled frames must equal the written payload")
}

// Under per-frame noise every frame also carries the AEAD tag, so the segment
// size must leave room for it or the sealed frame overflows the packet.
func TestRouteGroupWriteSegmentsUnderPerFrameSeal(t *testing.T) {
	rg, conns := createCapturingMuxRouteGroup(t)
	t.Cleanup(func() {
		for _, c := range conns {
			c.Close() //nolint:errcheck,gosec
		}
	})
	require.NoError(t, rg.SetWriteDeadline(time.Now().Add(5*time.Second)))

	tag := make([]byte, perFrameSealOverhead)
	rg.mu.Lock()
	rg.mux.seal = func(_ uint32, plaintext []byte) []byte { return append(append([]byte{}, plaintext...), tag...) }
	rg.mu.Unlock()

	payload := patterned(math.MaxUint16) // fits one frame unsealed, not once tagged
	n, err := rg.Write(payload)
	require.NoError(t, err)
	require.Equal(t, len(payload), n)

	frames := capturedFrames(conns)
	require.Len(t, frames, 2)
	var got []byte
	for _, f := range frames {
		require.LessOrEqual(t, int(f.Size()), math.MaxUint16)
		sealed := f.DataPayloadAfterSeq()
		got = append(got, sealed[:len(sealed)-perFrameSealOverhead]...)
	}
	require.Equal(t, payload, got)
}

// An oversized frame must be refused before a sequence number is taken: a seq
// with no frame behind it is a hole the peer's no-skip reorder buffer waits on
// forever.
func TestWrapPayloadOversizedDoesNotConsumeSeq(t *testing.T) {
	m := newRouteMux(logging.MustGetLogger("wrap-oversize"), true)
	m.growLegs(1)

	_, _, err := m.wrapPayload(1, make([]byte, math.MaxUint16), uuid.Nil)
	require.ErrorIs(t, err, routing.ErrPayloadTooBig)
	require.Equal(t, uint32(0), m.writeSeqValue(), "a rejected frame must not advance the sequence")

	_, seq, err := m.wrapPayload(1, make([]byte, math.MaxUint16-routing.SeqSize), uuid.Nil)
	require.NoError(t, err)
	require.Equal(t, uint32(0), seq)
}
