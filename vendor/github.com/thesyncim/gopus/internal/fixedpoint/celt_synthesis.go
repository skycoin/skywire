//go:build gopus_fixed_point

package fixedpoint

// This file ports the integer FIXED_POINT celt/celt_decoder.c celt_synthesis:
// the IMDCT + overlap-add stage that turns the de-normalised frequency-domain
// coefficients X (celt_norm) plus the per-band log energies oldBandE
// (celt_glog) into the time-domain out_syn (celt_sig) buffers, before the comb
// post-filter and deemphasis run.
//
// In the default reference build (FIXED_POINT, no ENABLE_QEXT):
//
//	celt_norm == celt_sig == celt_glog == opus_val32 (int32)
//	celt_coef (the MDCT window) == opus_val16 (int16, Q15)
//	SIG_SAT == 536870911 (the IMDCT saturation bound)
//
// The cross-frame overlap history lives in the caller's decode_mem buffer: each
// out_syn[c] aliases decode_mem[c] at offset decode_buffer_size-N, and the
// IMDCT writes its windowed tail (the overlap>>1 samples past N) into the start
// of the next frame's region, which the caller's per-frame shift carries
// forward. celt_synthesis itself only writes into out_syn[c]; the history is
// already present there because of that aliasing.

// half32 implements libopus HALF32(x) == SHR32(x, 1) on int32 (arithmetic
// right shift by one).
func half32(x int32) int32 {
	return x >> 1
}

// CeltSynthesis ports libopus celt_synthesis (FIXED_POINT, non-QEXT). It
// de-normalises X into the synthesis spectrum per channel and runs the
// per-short-block inverse MDCT (with the windowed TDAC overlap-add built into
// MDCTBackward) into the time-domain out_syn buffers, then saturates the IMDCT
// output to SIG_SAT.
//
// Parameters mirror the C plumbing:
//
//	mdct          the mode MDCT lookup (mode->mdct), built for the full mode size
//	window        the mode overlap window (mode->window), overlap int16 Q15 coefs
//	eBands        mode band boundaries (mode->eBands), length >= effEnd+1
//	nbEBands      mode->nbEBands
//	shortMdctSize mode->shortMdctSize
//	maxLM         mode->maxLM
//	overlap       mode->overlap
//	x             de-normalised input coefficients (celt_norm); channel c is at
//	              x[c*N : (c+1)*N] with N = shortMdctSize<<LM
//	outSyn        per-channel output buffers (celt_sig); outSyn[c] must have at
//	              least N + overlap/2 elements writable from its start
//	oldBandE      per-band quantized log energy (celt_glog); channel c is at
//	              oldBandE[c*nbEBands:]
//	start, effEnd active band range passed to denormalise_bands
//	C             number of coded channels (1 or 2)
//	CC            number of output channels (1 or 2)
//	isTransient   transient flag (selects short-block decomposition)
//	LM            log2 of the number of short MDCTs in the frame
//	downsample    decode downsampling factor (st->downsample)
//	silence       whole-frame silence flag
func CeltSynthesis(mdct *MDCTLookup, window []int16, eBands []int16,
	nbEBands, shortMdctSize, maxLM, overlap int,
	x []int32, outSyn [][]int32, oldBandE []int32,
	start, effEnd, C, CC, LM, downsample int, isTransient, silence bool) {

	N := shortMdctSize << LM
	M := 1 << LM

	var B, NB, shift int
	if isTransient {
		B = M
		NB = shortMdctSize
		shift = maxLM
	} else {
		B = 1
		NB = shortMdctSize << LM
		shift = maxLM - LM
	}

	freq := make([]int32, N)

	switch {
	case CC == 2 && C == 1:
		// Copying a mono stream to two channels. freq2 is a scratch view into
		// out_syn[1] at offset overlap/2.
		DenormaliseBands(x, freq, oldBandE, eBands, shortMdctSize, start, effEnd, M, downsample, silence)
		freq2 := outSyn[1][overlap/2:]
		// Store a temporary copy in the output buffer because the IMDCT
		// destroys its input.
		copy(freq2[:N], freq[:N])
		for b := 0; b < B; b++ {
			mdct.MDCTBackward(freq2[b:], outSyn[0][NB*b:], window, overlap, shift, B)
		}
		for b := 0; b < B; b++ {
			mdct.MDCTBackward(freq[b:], outSyn[1][NB*b:], window, overlap, shift, B)
		}
	case CC == 1 && C == 2:
		// Downmixing a stereo stream to mono. freq2 reuses out_syn[0] as temp.
		freq2 := outSyn[0][overlap/2:]
		DenormaliseBands(x, freq, oldBandE, eBands, shortMdctSize, start, effEnd, M, downsample, silence)
		DenormaliseBands(x[N:], freq2, oldBandE[nbEBands:], eBands, shortMdctSize, start, effEnd, M, downsample, silence)
		for i := 0; i < N; i++ {
			freq[i] = add32(half32(freq[i]), half32(freq2[i]))
		}
		for b := 0; b < B; b++ {
			mdct.MDCTBackward(freq[b:], outSyn[0][NB*b:], window, overlap, shift, B)
		}
	default:
		// Normal case (mono or stereo).
		for c := 0; c < CC; c++ {
			DenormaliseBands(x[c*N:], freq, oldBandE[c*nbEBands:], eBands, shortMdctSize, start, effEnd, M, downsample, silence)
			for b := 0; b < B; b++ {
				mdct.MDCTBackward(freq[b:], outSyn[c][NB*b:], window, overlap, shift, B)
			}
		}
	}

	// Saturate IMDCT output so the pitch postfilter / comb filter can't
	// overflow.
	for c := 0; c < CC; c++ {
		for i := 0; i < N; i++ {
			outSyn[c][i] = saturateSig(outSyn[c][i])
		}
	}
}
