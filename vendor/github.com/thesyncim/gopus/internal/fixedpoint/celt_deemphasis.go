//go:build gopus_fixed_point

package fixedpoint

// CELT inverse-preemphasis (deemphasis) for the FIXED_POINT, ENABLE_RES24
// build configuration, ported from celt/celt_decoder.c. This is the final
// synthesis stage: a per-channel one-tap IIR that undoes the encoder's
// preemphasis, with optional integer downsampling and optional accumulation
// into the destination buffer.
//
// Type mapping for this config (celt/arch.h):
//
//	celt_sig    = opus_val32 = int32
//	opus_val16  = opus_int16 = int16
//	opus_res    = opus_val32 = int32   (ENABLE_RES24)
//	SIG_SHIFT   = 12, RES_SHIFT = 8
//	SIG2RES(a)  = PSHR32(a, SIG_SHIFT-RES_SHIFT) = PSHR32(a, 4)
//	RES2INT16(a)= SAT16(PSHR32(a, RES_SHIFT))    = SAT16(PSHR32(a, 8))
//	ADD_RES(a,b)= ADD32(a, b)
//	VERY_SMALL  = 0
//
// Deemphasis implements the one-tap path that consumes coef[0]. The decoder's
// deemphasisMode handles the custom two-tap path when coef[1] is nonzero;
// QEXT decoding uses deemphasisQEXT in celt_synthesis_qext.go.

const (
	// sigShift is libopus SIG_SHIFT.
	sigShift = 12
	// resShift is libopus RES_SHIFT for the ENABLE_RES24 build.
	resShift = 8
)

// sat16 implements libopus SAT16(x): clamp an int32 to the int16 range.
func sat16(x int32) int16 {
	if x > 32767 {
		return 32767
	}
	if x < -32768 {
		return -32768
	}
	return int16(x)
}

// sig2res implements libopus SIG2RES(a) for ENABLE_RES24: PSHR32(a, 4).
func sig2res(a int32) int32 {
	return pshr32(a, sigShift-resShift)
}

// Res2Int16 implements libopus RES2INT16(a) for ENABLE_RES24: SAT16(PSHR32(a, 8)).
// It converts an opus_res sample (as produced by Deemphasis) to the int16 PCM
// value the decoder ultimately emits.
func Res2Int16(a int32) int16 {
	return sat16(pshr32(a, resShift))
}

// Deemphasis applies the CELT inverse-preemphasis IIR to the per-channel
// synthesis buffers in, writing opus_res samples into pcm interleaved with
// stride C (pcm[j*C+c]). It mirrors celt/celt_decoder.c deemphasis for the
// FIXED_POINT ENABLE_RES24 build.
//
//	in          per-channel celt_sig input; in[c] has at least N samples
//	pcm         interleaved opus_res destination of length (N/downsample)*C;
//	            when accum is set, existing values are added to (ADD_RES)
//	coef0       mode->preemph[0]
//	mem         per-channel filter state, length C; updated in place
//	N           per-channel sample count
//	downsample  decimation factor (>= 1)
//	accum       accumulate into pcm instead of overwriting
func Deemphasis(in [][]int32, pcm []int32, coef0 int16, mem []int32, N, downsample int, accum bool) {
	C := len(in)
	// Short version for the one-tap stereo, no-downsample, no-accum case. Custom
	// two-tap modes use deemphasisMode instead of this helper.
	if downsample == 1 && C == 2 && !accum {
		deemphasisStereoSimple(in, pcm, N, coef0, mem)
		return
	}

	Nd := N / downsample
	var local [celtMaxFrameSize]int32
	var scratch []int32
	if downsample > 1 {
		if N <= len(local) {
			scratch = local[:N]
		} else {
			scratch = make([]int32, N)
		}
	}

	for c := 0; c < C; c++ {
		m := mem[c]
		x := in[c]
		applyDownsampling := false

		switch {
		case downsample > 1:
			for j := 0; j < N; j++ {
				tmp := saturateSig(x[j] + m)
				m = mult16x32q15(coef0, tmp)
				scratch[j] = tmp
			}
			applyDownsampling = true
		case accum:
			for j := 0; j < N; j++ {
				tmp := saturateSig(x[j] + m)
				m = mult16x32q15(coef0, tmp)
				pcm[j*C+c] = add32(pcm[j*C+c], sig2res(tmp))
			}
		default:
			for j := 0; j < N; j++ {
				tmp := saturateSig(x[j] + m)
				m = mult16x32q15(coef0, tmp)
				pcm[j*C+c] = sig2res(tmp)
			}
		}
		mem[c] = m

		if applyDownsampling {
			if accum {
				for j := 0; j < Nd; j++ {
					pcm[j*C+c] = add32(pcm[j*C+c], sig2res(scratch[j*downsample]))
				}
			} else {
				for j := 0; j < Nd; j++ {
					pcm[j*C+c] = sig2res(scratch[j*downsample])
				}
			}
		}
	}
}

// deemphasisMode applies the custom two-tap inverse when mode->preemph[1] is
// nonzero. celt/celt_decoder.c deemphasis() scales each reconstructed sample
// by mode->preemph[3] before SIG2RES, including at downsample=1.
func (d *CELTDecoder) deemphasisMode(in [][]int32, pcm []int32, N int, accum bool) {
	if d.preemph1 == 0 {
		Deemphasis(in, pcm, d.preemph0, d.preemphMemD, N, d.downsample, accum)
		return
	}
	C := len(in)
	Nd := N / d.downsample
	scratch := d.deemphasisScratch[:N]
	for c := 0; c < C; c++ {
		m := d.preemphMemD[c]
		for j := 0; j < N; j++ {
			x := in[c][j]
			tmp := saturateSig(x + m)
			m = mult16x32q15(d.preemph0, tmp) - mult16x32q15(d.preemph1, x)
			scratch[j] = shl32(mult16x32q15(d.preemph3, tmp), 2)
		}
		d.preemphMemD[c] = m
		for j := 0; j < Nd; j++ {
			v := sig2res(scratch[j*d.downsample])
			if accum {
				pcm[j*C+c] = add32(pcm[j*C+c], v)
			} else {
				pcm[j*C+c] = v
			}
		}
	}
}

// deemphasisStereoSimple is the fast stereo path from celt/celt_decoder.c: no
// downsampling and no accumulation, both channels processed together.
func deemphasisStereoSimple(in [][]int32, pcm []int32, N int, coef0 int16, mem []int32) {
	x0 := in[0]
	x1 := in[1]
	m0 := mem[0]
	m1 := mem[1]
	for j := 0; j < N; j++ {
		tmp0 := saturateSig(x0[j] + m0)
		tmp1 := saturateSig(x1[j] + m1)
		m0 = mult16x32q15(coef0, tmp0)
		m1 = mult16x32q15(coef0, tmp1)
		pcm[2*j] = sig2res(tmp0)
		pcm[2*j+1] = sig2res(tmp1)
	}
	mem[0] = m0
	mem[1] = m1
}
