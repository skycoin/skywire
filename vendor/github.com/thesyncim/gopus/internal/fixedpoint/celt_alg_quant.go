//go:build gopus_fixed_point

package fixedpoint

import (
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// This file ports the entropy-coupled CELT FIXED_POINT PVQ wrappers alg_quant
// and alg_unquant from celt/vq.c (FIXED_POINT, non-QEXT path). They wrap the
// integer pulse search OpPvqSearch and the CWRS pulse coder with the range
// coder:
//
//   - exp_rotation / exp_rotation1: the pre/post-search rotation of X.
//   - normalise_residual: rescales the decoded integer codeword to unit norm.
//   - extract_collapse_mask: the anti-collapse bitmask of populated bands.
//   - encode_pulses / decode_pulses: CWRS index <-> range coder (ec_enc_uint /
//     ec_dec_uint), reusing celt.EncodePulses / celt.DecodePulses / celt.PVQ_V.
//
// Type model (celt/arch.h FIXED_POINT, QEXT off): celt_norm and opus_val32 are
// int32, opus_val16 is int16, opus_val64 is int64. NORM_SHIFT is 24. The
// macros that truncate operands to int16 before multiplying are reproduced
// exactly (mult16x16, mac16x16, mult16x16Q15i32, mult16x32Q15, ...).

// q15one is libopus Q15ONE (celt/arch.h, FIXED_POINT): 1.0 in Q15. It is the
// int16 unit used by the PVQ rotation-angle math, always widened with
// int32(q15one) before multiplying.
const q15one int16 = 32767

// spreadNone is SPREAD_NONE (celt/bands.h). The remaining SPREAD_* symbols are
// declared with the spreading-decision and bands kernels. Like its siblings it
// is an untyped int constant compared against int spread values.
const spreadNone = 0

// spreadFactor mirrors the static SPREAD_FACTOR[3] table in exp_rotation.
var spreadFactor = [3]int{15, 10, 5}

// expRotation1 ports celt/vq.c exp_rotation1 (FIXED_POINT path). It applies an
// in-place Givens-style rotation of X across the given stride. X is first
// scaled down to the int16 working range, rotated, then scaled back up.
func expRotation1(x []int32, length, stride int, c, s int16) {
	ms := neg16(s)
	normScaledown(x, length, normShift-14)
	// Forward sweep.
	ptr := 0
	for i := 0; i < length-stride; i++ {
		x1 := x[ptr]
		x2 := x[ptr+stride]
		// Xptr[stride] = EXTRACT16(PSHR32(MAC16_16(MULT16_16(c, x2),  s, x1), 15))
		x[ptr+stride] = int32(int16(pshr32(mac16x16(mult16x16(int32(c), x2), int32(s), x1), 15)))
		// *Xptr++ = EXTRACT16(PSHR32(MAC16_16(MULT16_16(c, x1), ms, x2), 15))
		x[ptr] = int32(int16(pshr32(mac16x16(mult16x16(int32(c), x1), int32(ms), x2), 15)))
		ptr++
	}
	// Backward sweep starting at &X[len-2*stride-1].
	ptr = length - 2*stride - 1
	for i := length - 2*stride - 1; i >= 0; i-- {
		x1 := x[ptr]
		x2 := x[ptr+stride]
		x[ptr+stride] = int32(int16(pshr32(mac16x16(mult16x16(int32(c), x2), int32(s), x1), 15)))
		x[ptr] = int32(int16(pshr32(mac16x16(mult16x16(int32(c), x1), int32(ms), x2), 15)))
		ptr--
	}
	normScaleup(x, length, normShift-14)
}

// expRotation ports celt/vq.c exp_rotation (FIXED_POINT path). It rotates X in
// place by +/-theta (dir selects forward/inverse) using the spread parameter.
func expRotation(x []int32, length, dir, stride, k, spread int) {
	if 2*k >= length || spread == spreadNone {
		return
	}
	factor := spreadFactor[spread-1]

	// gain = celt_div(MULT16_16(Q15_ONE, len), len+factor*K)
	//      = MULT32_32_Q31(MULT16_16(Q15_ONE, len), celt_rcp(len+factor*K))
	gain := int16(mult32x32q31(mult16x16(int32(q15one), int32(length)), CeltRcp(int32(length+factor*k))))
	// theta = HALF16(MULT16_16_Q15(gain, gain))
	theta := mult16x16Q15(int32(gain), int32(gain)) >> 1

	c := CeltCosNorm(theta)
	// s = celt_cos_norm(SUB16(Q15ONE, theta))  (= sin(theta))
	s := CeltCosNorm(int32(int16(int32(q15one) - theta)))

	stride2 := 0
	if length >= 8*stride {
		stride2 = 1
		// Increment while (stride2+0.5)^2 < len/stride.
		for (stride2*stride2+stride2)*stride+(stride>>2) < length {
			stride2++
		}
	}

	length = int(celtUdiv(uint32(length), uint32(stride)))
	for i := 0; i < stride; i++ {
		seg := x[i*length:]
		if dir < 0 {
			if stride2 != 0 {
				expRotation1(seg, length, stride2, s, c)
			}
			expRotation1(seg, length, 1, c, s)
		} else {
			expRotation1(seg, length, 1, c, neg16(s))
			if stride2 != 0 {
				expRotation1(seg, length, stride2, s, neg16(c))
			}
		}
	}
}

// mult16x16Q15 ports MULT16_16_Q15(a,b) = SHR(MULT16_16(a,b),15) over int32
// operands that are first truncated to int16.
func mult16x16Q15(a, b int32) int32 {
	return mult16x16(a, b) >> 15
}

// normaliseResidual ports celt/vq.c normalise_residual (FIXED_POINT, QEXT off).
// It rescales the integer codeword iy to unit norm scaled by gain, writing the
// celt_norm result into X.
func normaliseResidual(iy []int32, x []int32, n int, ryy, gain int32) {
	k := int(CeltILog2(ryy)) >> 1
	t := vshr32(ryy, 2*(k-7)-15)
	g := mult32x32q31(CeltRsqrtNorm32(t), gain)
	for i := 0; i < n; i++ {
		// X[i] = VSHR32(MULT16_32_Q15(iy[i], g), k+15-NORM_SHIFT)
		x[i] = vshr32(mult16x32Q15(int16(iy[i]), g), k+15-normShift)
	}
}

// extractCollapseMask ports celt/vq.c extract_collapse_mask. It returns a
// bitmask whose i-th bit is set when the i-th of B equal-width sub-bands of iy
// contains any nonzero pulse.
func extractCollapseMask(iy []int32, n, b int) uint32 {
	if b <= 1 {
		return 1
	}
	n0 := int(celtUdiv(uint32(n), uint32(b)))
	var collapseMask uint32
	for i := 0; i < b; i++ {
		var tmp int32
		for j := 0; j < n0; j++ {
			tmp |= iy[i*n0+j]
		}
		if tmp != 0 {
			collapseMask |= 1 << uint(i)
		}
	}
	return collapseMask
}

// AlgQuant ports celt/vq.c alg_quant (FIXED_POINT, non-QEXT). It rotates X,
// runs the integer PVQ search for K pulses over N dimensions, encodes the
// resulting codeword into enc via the CWRS pulse coder, and (when resynth is
// set) reconstructs the unit-norm shaped vector back into X. It returns the
// anti-collapse mask. X is modified in place.
func AlgQuant(x []int32, n, k, spread, b int, enc *rangecoding.Encoder, gain int32, resynth bool, scratch *celtEncodeScratch) uint32 {
	// iy needs N+3 slots for the search's vectorisation headroom.
	var iy []int32
	if scratch != nil {
		iy = ensureInt32(&scratch.pvqIy, n+3)
	} else {
		iy = make([]int32, n+3)
	}

	expRotation(x, n, 1, b, k, spread)

	yy := OpPvqSearch(x, iy, k, n, scratch)
	collapseMask := extractCollapseMask(iy, n, b)
	encodePulses(iy[:n], n, k, enc, scratch)
	if resynth {
		normaliseResidual(iy, x, n, yy, gain)
	}

	if resynth {
		expRotation(x, n, -1, b, k, spread)
	}
	return collapseMask
}

// AlgUnquant ports celt/vq.c alg_unquant (FIXED_POINT, non-QEXT). It decodes a
// pulse codeword from dec via the CWRS coder, normalises and inverse-rotates it
// into X, and returns the anti-collapse mask. X is written, not read.
func AlgUnquant(x []int32, n, k, spread, b int, dec *rangecoding.Decoder, gain int32) uint32 {
	var pulseStorage [celtMaxBandWidth]int32
	var row [256]uint32
	var iy []int32
	if n <= len(pulseStorage) {
		iy = pulseStorage[:n]
	} else {
		iy = make([]int32, n)
	}
	celt.DecodePulsesInto32(dec.DecodeUniform(celt.PVQ_V(n, k)), n, k, iy, row[:])
	var ryy int32
	for _, pulse := range iy {
		v := int32(int16(pulse))
		ryy += v * v
	}
	shift := int(CeltILog2(ryy)) >> 1
	g := mult32x32q31(CeltRsqrtNorm32(vshr32(ryy, 2*(shift-7)-15)), gain)
	for i := range iy {
		x[i] = vshr32(mult16x32Q15(int16(iy[i]), g), shift+15-normShift)
	}
	expRotation(x, n, -1, b, k, spread)
	if b <= 1 {
		return 1
	}
	n0 := int(celtUdiv(uint32(n), uint32(b)))
	var mask uint32
	for i := 0; i < b; i++ {
		var nonzero int32
		for j := 0; j < n0; j++ {
			nonzero |= iy[i*n0+j]
		}
		if nonzero != 0 {
			mask |= 1 << uint(i)
		}
	}
	return mask
}

// encodePulses ports celt/vq.c encode_pulses: compute the CWRS index of the
// pulse vector and its codeword count V(N,K), then encode the index as a
// uniform via ec_enc_uint.
func encodePulses(y []int32, n, k int, enc *rangecoding.Encoder, scratch *celtEncodeScratch) {
	index := celt.EncodePulses32Scratch(y, n, k, pulseCWRSBuffer(scratch))
	nc := celt.PVQ_V(n, k)
	enc.EncodeUniform(index, nc)
}

func pulseCWRSBuffer(scratch *celtEncodeScratch) *[]uint32 {
	if scratch == nil {
		return nil
	}
	return &scratch.pvqCWRS
}
