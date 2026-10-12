//go:build gopus_fixed_point

package rangecoding

// ReserveBufferCapacity keeps speculative CELT encoder snapshots in caller
// storage when a later frame enters stereo theta RDO at a larger packet size.
func (s *EncoderSnapshot) ReserveBufferCapacity(n int) {
	if n <= cap(s.bytes) {
		return
	}
	buf := make([]byte, len(s.bytes), n)
	copy(buf, s.bytes)
	s.bytes = buf
}
