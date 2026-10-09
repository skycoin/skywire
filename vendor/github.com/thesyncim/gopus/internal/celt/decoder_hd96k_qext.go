//go:build gopus_qext

package celt

// HD96kSynthesizeMono runs the native 96 kHz long/short-block IMDCT + overlap-add
// for one channel of denormalized HD96k spectrum (1920 bins). prevOverlap is the
// overlap=240 history; it is updated in place with this frame's new tail.
// out must hold the 1920 frame samples plus the 240-sample overlap tail. It
// returns the frame samples, or nil if spec is not exactly 1920 bins or a
// required buffer is too short.
func (m *HD96kMode) HD96kSynthesizeMono(spec []float32, prevOverlap []celtSig, transient bool, scratch *imdctScratchF32, shortCoeffs, out []float32) []float32 {
	frameSize := m.MdctN / 2
	if len(spec) != frameSize {
		return nil
	}
	output := synthesizeChannelWithOverlapScratchF32(spec, prevOverlap, m.Overlap, transient, m.NbShortMdcts, out, scratch, shortCoeffs)
	if len(output) < frameSize+m.Overlap {
		return nil
	}
	copy(prevOverlap[:m.Overlap], output[frameSize:frameSize+m.Overlap])
	return output[:frameSize]
}
