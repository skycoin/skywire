//go:build gopus_celt_trace && !gopus_fixed_point

package rangecoding

// BoundaryTraceSnapshot is a bounded copy of the live ec_enc state used by
// the Hybrid coder-boundary diagnostic. It is available only in trace builds.
// Forward and backward slices contain bytes already written into the shared
// range-coder buffer; pending carry and raw-bit state remain in the scalar
// fields below.
type BoundaryTraceSnapshot struct {
	Storage     uint32
	Offs        uint32
	EndOffs     uint32
	EndWindow   uint32
	Range       uint32
	Value       uint32
	Extension   uint32
	NBitsTotal  int32
	NEndBits    int32
	Remainder   int32
	Error       int32
	Tell        int32
	TellFrac    int32
	ForwardLen  uint32
	BackwardLen uint32
	Forward     [4096]byte
	Backward    [4096]byte
}

// SnapshotBoundaryForTesting copies the active coder state and the bytes
// already written at both ends of its buffer. The fixed capture is large
// enough for the public Opus packet limit and does not allocate.
func (e *Encoder) SnapshotBoundaryForTesting(dst *BoundaryTraceSnapshot) bool {
	if dst == nil || e.storage > uint32(len(dst.Forward)) || e.offs > e.storage || e.endOffs > e.storage-e.offs || e.rng == 0 {
		return false
	}
	if int(e.storage) > len(e.buf) {
		return false
	}
	*dst = BoundaryTraceSnapshot{
		Storage:     e.storage,
		Offs:        e.offs,
		EndOffs:     e.endOffs,
		EndWindow:   e.endWindow,
		Range:       e.rng,
		Value:       e.val,
		Extension:   e.ext,
		NBitsTotal:  e.nbitsTotal,
		NEndBits:    e.nendBits,
		Remainder:   e.rem,
		Error:       e.err,
		Tell:        int32(e.Tell()),
		TellFrac:    int32(e.TellFrac()),
		ForwardLen:  e.offs,
		BackwardLen: e.endOffs,
	}
	copy(dst.Forward[:dst.ForwardLen], e.buf[:e.offs])
	backwardStart := e.storage - e.endOffs
	copy(dst.Backward[:dst.BackwardLen], e.buf[backwardStart:e.storage])
	return true
}
