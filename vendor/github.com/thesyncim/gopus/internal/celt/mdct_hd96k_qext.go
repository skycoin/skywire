//go:build gopus_qext

package celt

// Native 96 kHz CELT MDCT transform plumbing (Opus HD / QEXT).
//
// The 96 kHz mode (HD96kMode, libopus mode96000_1920_240) is a clean 2x
// scale-up of the 48 kHz fullband mode: overlap 240, shortMdctSize 240,
// long-block MDCT N=3840, nbShortMdcts 8. Its four MDCT shifts use block sizes
// 3840/1920/960/480, so the per-shift KISS-FFT states are nfft 960/480/240/120.
//
// gopus's forward/inverse MDCT kernels (mdctForwardOverlapF32Scratch,
// imdctOverlapWithPrevScratchF32Output32, imdctInPlaceScratchF32Spectrum) are
// fully driven by the transform length: they look up the MDCT twiddle segment
// via getMDCTTrigF32(N) and the KISS-FFT state via getKissFFTState(N/4), and
// the overlap window via GetWindowBufferF32(overlap). For the 96 kHz lengths
// those helpers reproduce exactly the closed-form tables HD96kMode carries
// (verified against the libopus qext oracle), and every required FFT size
// factors with radix <= 5. So the existing kernels handle the 96 kHz mode
// without modification; this file is the thin, mode-aware wiring that drives
// them at the native lengths and pins the table identity.

// hd96kMDCTForward computes the long-block (non-transient) forward MDCT for the
// native 96 kHz mode. input is frameSize+overlap = 1920+240 samples; the result
// is frameSize = 1920 MDCT coefficients (libopus clt_mdct_forward with
// shift = maxLM - LM = 0, stride 1).
func (m *HD96kMode) hd96kMDCTForward(input []float32) []float32 {
	frameSize := m.MdctN / 2
	if len(input) != frameSize+m.Overlap {
		return nil
	}
	coeffs := make([]float32, frameSize)
	mdctForwardOverlapF32Scratch(input, m.Overlap, coeffs, nil, nil, nil, nil)
	return coeffs
}

// hd96kMDCTForwardShort computes the transient (short-block) forward MDCT for
// the native 96 kHz mode. input is frameSize+overlap = 1920+240 samples,
// processed as nbShortMdcts=8 blocks of shortMdctSize=240 with overlap 240
// (each block's MDCT length is N>>maxLM = 480). Coefficients are interleaved
// exactly as libopus lays out transient blocks (out[b + i*shortBlocks]).
func (m *HD96kMode) hd96kMDCTForwardShort(input []float32) []float32 {
	frameSize := m.MdctN / 2
	if len(input) != frameSize+m.Overlap {
		return nil
	}
	return mdctForwardShortOverlap(input, m.Overlap, m.NbShortMdcts)
}

// hd96kIMDCTLong computes the long-block inverse MDCT for the native 96 kHz
// mode (libopus clt_mdct_backward with shift 0, stride 1). spectrum is
// frameSize = 1920 coefficients; prevOverlap is the overlap=240 history; the
// result is frameSize+overlap = 2160 windowed time samples.
func (m *HD96kMode) hd96kIMDCTLong(spectrum, prevOverlap []float32) []float32 {
	frameSize := m.MdctN / 2
	if len(spectrum) != frameSize {
		return nil
	}
	return imdctOverlapWithPrevScratchF32Output32(spectrum, prevOverlap, m.Overlap, nil)
}

// hd96kIMDCTShort computes the transient inverse MDCT for the native 96 kHz
// mode, mirroring libopus's per-block clt_mdct_backward(freq+b, out+shortSize*b,
// ..., maxLM, shortBlocks, 0) loop with overlap-add into a shared buffer.
// spectrum is the interleaved frameSize=1920 coefficients; prevOverlap is the
// overlap=240 history. The result is frameSize+overlap = 2160 samples.
func (m *HD96kMode) hd96kIMDCTShort(spectrum, prevOverlap []float32) []float32 {
	frameSize := m.MdctN / 2
	shortBlocks := m.NbShortMdcts
	if len(spectrum) != frameSize || shortBlocks <= 0 || frameSize%shortBlocks != 0 {
		return nil
	}
	shortSize := frameSize / shortBlocks
	needed := frameSize + m.Overlap

	out := make([]float32, needed)
	copyLen := min(len(prevOverlap), m.Overlap)
	for i := 0; i < copyLen; i++ {
		out[i] = prevOverlap[i]
	}

	var scratch imdctScratchF32
	block := make([]float32, shortSize)
	for b := 0; b < shortBlocks; b++ {
		// De-interleave this short block's coefficients (freq + b, stride
		// shortBlocks) into a contiguous buffer for the size-driven kernel.
		for i := 0; i < shortSize; i++ {
			block[i] = spectrum[b+i*shortBlocks]
		}
		imdctInPlaceScratchF32Spectrum(block, out, b*shortSize, m.Overlap, &scratch)
	}
	return out
}
