//go:build gopus_fixed_point

package fixedpoint

// normShift is libopus NORM_SHIFT: the Q-shift applied to celt_norm samples in
// the FIXED_POINT build (arch.h).
const normShift = 24

// epsilon is libopus EPSILON for the FIXED_POINT build.
const epsilon = 1

// NormaliseBands ports the FIXED_POINT celt/bands.c normalise_bands: it scales
// each band of the frequency-domain signal so its energy is one, writing
// celt_norm (NORM_SHIFT) output into X.
//
// For each channel c and band i it reads the band amplitude bandE, raises a
// very low energy by EPSILON, normalises it into [1.0, 2.0) Q30 via a left
// shift, takes its reciprocal with celt_rcp_norm32, then multiplies each shifted
// frequency sample by that reciprocal.
//
// Inputs mirror the libopus CELTMode plumbing:
//
//	freq          frequency-domain signal, channel-major, length C*N
//	X             output normalised bands, channel-major, length C*N
//	bandE         band energies, length >= C*nbEBands
//	eBands        mode band boundaries (m->eBands), length nbEBands+1
//	nbEBands      m->nbEBands
//	shortMdctSize m->shortMdctSize (N = M*shortMdctSize)
//	end, C, M     active band count, channel count and the short-block multiplier
func NormaliseBands(freq []int32, X []int32, bandE []int32, eBands []int16, nbEBands, shortMdctSize, end, C, M int) {
	n := M * shortMdctSize
	for c := 0; c < C; c++ {
		for i := 0; i < end; i++ {
			e := bandE[i+c*nbEBands]
			// For very low energies this prevents energy rounding from blowing
			// up the normalised signal.
			if e < 10 {
				e += epsilon
			}
			shift := 30 - int(celtZlog2(e))
			e = shl32(e, shift)
			g := CeltRcpNorm32(e)
			for j := M * int(eBands[i]); j < M*int(eBands[i+1]); j++ {
				X[j+c*n] = pshr32(mult32x32q31(g, shl32(freq[j+c*n], shift)), 30-normShift)
			}
		}
	}
}
