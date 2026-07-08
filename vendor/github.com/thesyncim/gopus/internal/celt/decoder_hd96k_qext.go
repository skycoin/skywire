//go:build gopus_qext

package celt

// Native 96 kHz CELT decode scaffolding (Opus HD / QEXT, increment 2a).
//
// This file wires the native 96 kHz CELT mode (HD96kMode, libopus
// mode96000_1920_240: 1920-sample frames, 3840-MDCT, overlap 240, 8 short
// blocks) through gopus's existing size-driven CELT kernels. It exists to
// document and pin the mode-parametric decode contract; the surrounding decode
// driver (DecodeFrame) is still hardwired to the 48 kHz static mode via
// GetModeConfig/ValidFrameSize, so full top-level routing lands in 2b.
//
// What is ALREADY mode-parametric (verified, reused unchanged):
//   - IMDCT (imdctOverlapWithPrevScratchF32Output32 / imdctInPlaceScratchF32Spectrum):
//     length-driven, looks up trig via getMDCTTrigF32(N) and the KISS-FFT state
//     via getKissFFTState(N/4). For HD96k the long block is N=3840 (FFT 960,
//     radix<=5) and short blocks are N=480 (FFT 120, static).
//   - PVQ band decode (quantAllBandsDecodeWithScratchWithMode): accepts
//     frameSize, lm, and bandEdges/bandLogN overrides.
//   - Coarse/fine energy decode and bit allocation: parametric on end/lm/nbBands.
//   - denormalizeBandsPackedDownsampleIntoFloat32: takes the band-edge slice and
//     lm explicitly.
//
// What is NOT yet mode-parametric (the 2b work):
//   - GetModeConfig / ValidFrameSize reject frameSize=1920 (cap LM at 3, frame
//     sizes 120/240/480/960). prepareDecodeFrame() rejects 1920 outright.
//   - The public Synthesize/SynthesizeStereo wrappers bake in the Overlap=120
//     package constant; the HD path must thread overlap=240 (the underlying
//     synthesizeChannelWithOverlapScratchF32 already takes overlap as a param).
//   - Decoder synthesis state sizing (overlapBuffer, postfilter/PLC history,
//     DecodeBufferSize) assumes the 48 kHz overlap; HD needs overlap*channels=240.
//   - Preemph/deemphasis coefficients differ at 96 kHz (HD96kMode.Preemph).
//   - Top-level Opus framing must carry the reserved QEXT extension payload and
//     route Fs=96000 here instead of the 2:1 resample wrapper (decoder_96k_qext.go).
//
// HD96kSynthesizeMono/Stereo below are the overlap=240 synthesis primitives the
// 2b driver will call; they reuse the size-driven kernels verbatim and are
// pinned against the native 96 kHz decode oracle by the gopus-level tests.

// HD96kSynthesizeMono runs the native 96 kHz long/short-block IMDCT + overlap-add
// for one channel of denormalized HD96k spectrum (1920 bins). prevOverlap is the
// overlap=240 history; it is updated in place with this frame's new tail.
// Output is the 1920 time-domain samples for the frame.
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
