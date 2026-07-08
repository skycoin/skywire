//go:build gopus_fixed_point

package rangecoding

// SkipToTell advances the decoder's bit accounting so Tell() reports exactly
// targetBits, without consuming further input. It mirrors the CELT silence
// fast-path in celt_decode_with_ec: dec->nbits_total += targetBits-ec_tell(dec).
func (d *Decoder) SkipToTell(targetBits int) {
	d.nbitsTotal += int32(targetBits - d.Tell())
}

// SkipToTell pretends the given number of bits have been written, mirroring the
// libopus silence path (enc->nbits_total += tell - ec_tell(enc)). After the call
// Tell() returns tell. Used by the CELT encoder's silence branch.
func (e *Encoder) SkipToTell(tell int) {
	e.nbitsTotal += int32(tell - e.Tell())
}

// EncoderSnapshot captures the encoder state so a speculative encode can be
// rolled back. It mirrors libopus's "ec_save = *ec" plus the save of the
// output byte region [start, storage), where start is the write offset at the
// time the speculative section began. Restoring requires putting those bytes
// back because a re-encode may write a different (possibly smaller) byte count.
type EncoderSnapshot struct {
	enc   Encoder
	start uint32
	bytes []byte
}

// Snapshot returns a snapshot of the current encoder state. The startOffs is
// the write offset (Offs) recorded at the beginning of the speculative section;
// the output bytes in [startOffs, storage) are copied so Restore can roll them
// back exactly. Pass the same startOffs for every snapshot of a given section.
func (e *Encoder) Snapshot(startOffs uint32) EncoderSnapshot {
	var bytes []byte
	if startOffs < e.storage {
		bytes = append([]byte(nil), e.buf[startOffs:e.storage]...)
	}
	return EncoderSnapshot{enc: *e, start: startOffs, bytes: bytes}
}

// SnapshotInto captures the current encoder state into a caller-owned snapshot,
// reusing the snapshot's byte buffer instead of allocating. It is the
// allocation-free counterpart to Snapshot for hot paths that take many
// speculative snapshots (the CELT stereo theta-RDO). The captured bytes are the
// output region [startOffs, storage).
func (e *Encoder) SnapshotInto(s *EncoderSnapshot, startOffs uint32) {
	s.enc = *e
	s.start = startOffs
	if startOffs < e.storage {
		n := int(e.storage - startOffs)
		if cap(s.bytes) < n {
			s.bytes = make([]byte, n)
		} else {
			s.bytes = s.bytes[:n]
		}
		copy(s.bytes, e.buf[startOffs:e.storage])
	} else {
		s.bytes = s.bytes[:0]
	}
}

// Restore rolls the encoder back to a previously taken snapshot, restoring both
// the scalar state and the output bytes captured at snapshot time.
func (e *Encoder) Restore(s EncoderSnapshot) {
	buf := e.buf
	*e = s.enc
	e.buf = buf
	if len(s.bytes) > 0 {
		copy(e.buf[s.start:s.enc.storage], s.bytes)
	}
}
