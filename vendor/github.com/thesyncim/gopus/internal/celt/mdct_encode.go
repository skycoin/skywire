// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file provides the forward MDCT transform for encoding.

package celt

import (
	"math"

	"github.com/thesyncim/gopus/internal/opusmath"
)

func mdctMul(a, b float32) float32 {
	return noFMA32Mul(a, b)
}

func mdctMulAddMix(a, b, c, d float32) float32 {
	// Mirror the clang -ffp-contract=on float path of libopus celt/mdct.c
	// clt_mdct_backward_c() TDAC mix (S_MUL(x2,*wp1)+S_MUL(x1,*wp2)): the second
	// product is rounded on its own and the first multiply is fused into the
	// add. The fully non-fused form drifts by ~1 ULP once the overlap-add region
	// carries non-zero history (transient short-block boundaries), which seeds
	// the host-only parity cluster.
	if mdctUseFMALikeMixEnabled {
		return mdctFMA32(a, c, mdctMul(b, d))
	}
	return mdctMul(a, c) + mdctMul(b, d)
}

func mdctMulSubMix(a, b, c, d float32) float32 {
	// Mirror libopus celt/mdct.c clt_mdct_backward_c() TDAC mix
	// (S_MUL(x2,*wp2)-S_MUL(x1,*wp1)) under clang -ffp-contract=on: round the
	// subtracted product, fuse the first multiply into the subtract.
	if mdctUseFMALikeMixEnabled {
		return mdctFMA32(a, c, -mdctMul(b, d))
	}
	return mdctMul(a, c) - mdctMul(b, d)
}

func mdctMulSubMixAlt(a, b, c, d float32) float32 {
	return mdctMul(a, c) - mdctMul(b, d)
}

func mdctStoreDirectStage(dst []kissCpx, idx int, scale, re, im, t0, t1 float32) {
	yr := mdctMul(re, t0) - mdctMul(im, t1)
	yi := mdctMul(im, t0) + mdctMul(re, t1)
	dst[idx].r = yr * scale
	dst[idx].i = yi * scale
}

func mdctStoreDirectStageFMALike(dst []kissCpx, idx int, scale, re, im, t0, t1 float32) {
	yr := mdctEncodeFMA32(re, t0, -mdctMul(im, t1))
	yi := mdctEncodeFMA32(im, t0, mdctMul(re, t1))
	dst[idx].r = yr * scale
	dst[idx].i = yi * scale
}

// mdctMulAddMixEncode and mdctMulSubMixEncode are the encoder-only variants
// of the TDAC windowed-fold mix. They mirror mdctMulAddMix / mdctMulSubMix
// exactly on every build (same mdctUseFMALikeMixEnabled gating, same
// non-FMA path on amd64), so the amd64 bit-exact libopus oracle stays
// byte-parity. The win on arm64 purego is just that mdctEncodeFMA32 uses the
// Go backend's FMADDS contraction instead of mdctFMA32's wider helper, which
// the encoder pitch-search is free to use because that path is quality-gated.
func mdctMulAddMixEncode(a, b, c, d float32) float32 {
	if mdctUseFMALikeMixEnabled {
		return mdctEncodeFMA32(a, c, mdctMul(b, d))
	}
	return mdctMul(a, c) + mdctMul(b, d)
}
func mdctMulSubMixEncode(a, b, c, d float32) float32 {
	if mdctUseFMALikeMixEnabled {
		return mdctEncodeFMA32(a, c, -mdctMul(b, d))
	}
	return mdctMul(a, c) - mdctMul(b, d)
}

// MDCT computes the forward Modified Discrete Cosine Transform.
// For CELT-typical inputs (frameSize+Overlap), this uses the short-overlap
// algorithm from libopus. For legacy 2*N inputs, it falls back to the
// direct MDCT formula.
func MDCT(samples []float32) []float32 {
	if len(samples) == 0 {
		return nil
	}

	if len(samples) > Overlap {
		frameSize := len(samples) - Overlap
		if ValidFrameSize(frameSize) {
			return mdctForwardOverlap(samples, Overlap)
		}
	}

	return mdctStandard(samples)
}

// MDCTShort computes the forward MDCT for transient frames with multiple short blocks.
// This processes multiple short MDCTs and interleaves the coefficients in the same
// format expected by IMDCTShort.
//
// samples: interleaved time samples for shortBlocks MDCTs
// shortBlocks: number of short MDCTs (2, 4, or 8)
// Returns: interleaved frequency coefficients
//
// In transient mode, CELT uses multiple shorter MDCTs instead of one long MDCT.
// This provides better time resolution for transients at the cost of reduced
// frequency resolution.
//
// Reference: libopus celt/celt_encoder.c, transient mode handling
func MDCTShort(samples []float32, shortBlocks int) []float32 {
	if shortBlocks <= 1 {
		return MDCT(samples)
	}
	if len(samples) == 0 {
		return nil
	}

	if len(samples) > Overlap {
		frameSize := len(samples) - Overlap
		if ValidFrameSize(frameSize) && frameSize%shortBlocks == 0 {
			return mdctForwardShortOverlap(samples, Overlap, shortBlocks)
		}
	}

	return mdctShortStandard(samples, shortBlocks)
}

// mdctCoreCompute computes the core MDCT formula into the provided coeffs slice.
// samples: input samples of length N2 (2*N)
// coeffs: output coefficients of length N
// scale: scale factor applied to each coefficient
// This is the shared implementation used by both mdctDirect and mdctStandard.
// Formula: X[k] = scale * sum_{n=0}^{N2-1} x[n] * cos(pi/N * (n+0.5+N/2) * (k+0.5))
func mdctCoreCompute(samples []float32, coeffs []float32, scale float32) {
	N2 := len(samples)
	N := N2 / 2
	if N <= 0 || len(coeffs) < N {
		return
	}

	for k := range N {
		var sum float32
		kPlus := float32(k) + 0.5
		for n := range N2 {
			nPlus := float32(n) + 0.5 + float32(N)/2
			angle := float32(math.Pi) / float32(N) * nPlus * kPlus
			sum += samples[n] * opusmath.CosF32(angle)
		}
		coeffs[k] = sum * scale
	}
}

// mdctDirect computes MDCT without windowing (assumes pre-windowed input).
// Used by MDCTShort for individual short blocks.
// The output is scaled by 4/N2 (or equivalently 2/N) to match libopus normalization.
// Reference: libopus celt/tests/test_unit_mdct.c check() function
// Formula: X[k] = sum_{n=0}^{N2-1} x[n] * cos(2*pi*(n+0.5+0.25*N2)*(k+0.5)/N2) / (N2/4)
func mdctDirect(samples []float32) []float32 {
	N2 := len(samples)
	N := N2 / 2

	if N <= 0 {
		return nil
	}

	coeffs := make([]float32, N)

	// Scale factor: 4/N2 = 4/(2*N) = 2/N
	// This matches libopus's division by N/4 in the test formula
	scale := float32(4.0) / float32(N2)

	mdctCoreCompute(samples, coeffs, scale)

	return coeffs
}

// applyMDCTWindow applies the Vorbis window to samples for MDCT analysis.
// CELT uses short overlap (120 samples) rather than 50% overlap.
// Only the first and last 'overlap' samples are windowed; middle samples are unmodified.
func applyMDCTWindow(samples []float32) {
	n := len(samples)
	if n <= 0 {
		return
	}

	// CELT uses short overlap of 120 samples
	overlap := min(Overlap, n/2)

	// Get precomputed window for overlap region
	window := GetWindowBufferF32(overlap)

	// Apply window to beginning (rising edge) - first 'overlap' samples
	for i := 0; i < overlap && i < n; i++ {
		samples[i] *= window[i]
	}

	// Middle samples are unmodified (window = 1.0)

	// Apply window to end (falling edge) - last 'overlap' samples
	for i := 0; i < overlap && n-overlap+i < n; i++ {
		idx := n - overlap + i
		// Falling edge uses window in reverse: window[overlap-1-i]
		samples[idx] *= window[overlap-1-i]
	}
}

// MDCTForwardWithOverlapFloat32 computes the CELT float-build MDCT without
// widening caller-owned signal scratch.
func MDCTForwardWithOverlapFloat32(samples []float32, overlap int) []float32 {
	if len(samples) <= overlap {
		return nil
	}
	coeffs := make([]float32, len(samples)-overlap)
	mdctForwardOverlapF32Scratch(samples, overlap, coeffs, nil, nil, nil, nil)
	return coeffs
}

// mdctForwardOverlap implements the CELT short-overlap MDCT (libopus clt_mdct_forward)
// for a single block. Input length must be frameSize+overlap.
// This uses float32 arithmetic internally to match libopus float precision.
func mdctForwardOverlap(samples []float32, overlap int) []float32 {
	return mdctForwardOverlapF32(samples, overlap)
}

// mdctForwardOverlapF32 is a float32-precision MDCT matching libopus float path.
func mdctForwardOverlapF32(samples []float32, overlap int) []float32 {
	coeffs := make([]float32, len(samples)-overlap)
	mdctForwardOverlapF32Scratch(samples, overlap, coeffs, nil, nil, nil, nil)
	return coeffs
}

// mdctForwardOverlapF32Scratch is the scratch-aware version that avoids allocations.
func mdctForwardOverlapF32Scratch(samples []float32, overlap int, coeffs []float32, f []float32, fftIn []complex64, fftOut []complex64, fftTmp []kissCpx) {
	if len(samples) == 0 {
		return
	}
	if overlap < 0 {
		overlap = 0
	}
	if overlap > len(samples) {
		overlap = len(samples)
	}

	frameSize := len(samples) - overlap
	if frameSize <= 0 {
		return
	}

	n2 := frameSize
	n := n2 * 2
	n4 := n2 / 2
	if n4 <= 0 {
		return
	}

	trig := getMDCTTrigF32(n)
	var window []float32
	if overlap > 0 {
		window = GetWindowBufferF32(overlap)
	}

	st := getKissFFTState(n4)
	useDirectKissCpx := st != nil && len(st.bitrev) >= n4
	fuseDirectStage := useDirectKissCpx

	// Use provided buffers or allocate
	if !fuseDirectStage {
		if f == nil || len(f) < n2 {
			f = make([]float32, n2)
		}
	}
	if !useDirectKissCpx {
		if fftIn == nil || len(fftIn) < n4 {
			fftIn = make([]complex64, n4)
		}
		if fftOut == nil || len(fftOut) < n4 {
			fftOut = make([]complex64, n4)
		}
	}
	if fftTmp == nil || len(fftTmp) < n4 {
		fftTmp = make([]kissCpx, n4)
	}
	if coeffs == nil || len(coeffs) < n2 {
		coeffs = make([]float32, n2)
	}

	xp1 := overlap / 2
	xp2 := n2 - 1 + overlap/2
	wp1 := overlap / 2
	wp2 := overlap/2 - 1
	i := 0
	limit1 := (overlap + 3) >> 2

	// Match libopus st->scale initialization in float builds (1.f/nfft).
	scale := float32(1.0) / float32(n4)
	// QEXT clt_mdct_forward() applies the FFT scale to the post-rotation twiddles
	// instead of the pre-rotation, so split the scale into pre/post factors.
	preScale := scale
	postScale := float32(1.0)
	if mdctQEXTScalePlacement {
		preScale = 1.0
		postScale = scale
	}
	var fftStage []kissCpx
	if useDirectKissCpx {
		fftStage = fftTmp[:n4]
	}

	if fuseDirectStage {
		bitrev := st.bitrev
		_ = bitrev[n4-1]
		_ = fftStage[n4-1]
		if mdctUseFMALikeMixEnabled {
			// Leading windowed fold: whole 4-wide blocks go to the NEON
			// kernel (bit-identical per element); the scalar loop finishes
			// the remainder. The kernel's paired loads touch one extra odd
			// lane above each stream start and run the descending streams
			// down to start-2*done+2, so gate on those exact bounds.
			if lead := limit1 - i; mdctUseNeonMidFold && lead >= 4 {
				blocks := lead >> 2
				done := blocks * 4
				if xp2-n2-2*done+2 >= 0 && wp2-2*done+2 >= 0 &&
					xp1+n2+2*done-1 < len(samples) && xp2+1 < len(samples) &&
					wp1+2*done-1 < len(window) && wp2+1 < len(window) {
					mdctFold1StoreNeon(fftStage, bitrev, samples, window, trig, i, n4, n2, xp1, xp2, wp1, wp2, blocks, preScale)
					i += done
					xp1 += 2 * done
					xp2 -= 2 * done
					wp1 += 2 * done
					wp2 -= 2 * done
				}
			}
			for ; i+1 < limit1; i += 2 {
				re0 := mdctMulAddMixEncode(float32(samples[xp1+n2]), float32(samples[xp2]), window[wp2], window[wp1])
				im0 := mdctMulSubMixEncode(float32(samples[xp1]), float32(samples[xp2-n2]), window[wp1], window[wp2])
				re1 := mdctMulAddMixEncode(float32(samples[xp1+n2+2]), float32(samples[xp2-2]), window[wp2-2], window[wp1+2])
				im1 := mdctMulSubMixEncode(float32(samples[xp1+2]), float32(samples[xp2-n2-2]), window[wp1+2], window[wp2-2])
				t00, t10, t01, t11 := trig[i], trig[n4+i], trig[i+1], trig[n4+i+1]
				yr0 := mdctEncodeFMA32(re0, t00, -mdctMul(im0, t10))
				yi0 := mdctEncodeFMA32(im0, t00, mdctMul(re0, t10))
				yr1 := mdctEncodeFMA32(re1, t01, -mdctMul(im1, t11))
				yi1 := mdctEncodeFMA32(im1, t01, mdctMul(re1, t11))
				b0, b1 := bitrev[i], bitrev[i+1]
				fftStage[b0].r = yr0 * preScale
				fftStage[b0].i = yi0 * preScale
				fftStage[b1].r = yr1 * preScale
				fftStage[b1].i = yi1 * preScale
				xp1 += 4
				xp2 -= 4
				wp1 += 4
				wp2 -= 4
			}
			for ; i < limit1; i++ {
				re := mdctMulAddMixEncode(float32(samples[xp1+n2]), float32(samples[xp2]), window[wp2], window[wp1])
				im := mdctMulSubMixEncode(float32(samples[xp1]), float32(samples[xp2-n2]), window[wp1], window[wp2])
				t0, t1 := trig[i], trig[n4+i]
				yr := mdctEncodeFMA32(re, t0, -mdctMul(im, t1))
				yi := mdctEncodeFMA32(im, t0, mdctMul(re, t1))
				b := bitrev[i]
				fftStage[b].r = yr * preScale
				fftStage[b].i = yi * preScale
				xp1 += 2
				xp2 -= 2
				wp1 += 2
				wp2 -= 2
			}

			wp1 = 0
			wp2 = overlap - 1
			// The no-window middle region is elementwise; hand whole 4-wide
			// blocks to the NEON kernel (bit-identical per element) and let
			// the scalar loop finish the remainder. The kernel's paired loads
			// touch samples[xp2-2*done+2 : xp2+2] and samples[xp1 : xp1+2*done],
			// so gate on those exact bounds.
			if mid := n4 - limit1 - i; mdctUseNeonMidFold && mid >= 4 {
				blocks := mid >> 2
				done := blocks * 4
				if xp2-2*done+2 >= 0 && xp2+1 < len(samples) && xp1+2*done-1 < len(samples) {
					mdctMidFoldStoreNeon(fftStage, bitrev, samples, trig, i, n4, xp1, xp2, blocks, preScale)
					i += done
					xp1 += 2 * done
					xp2 -= 2 * done
				}
			}
			// 2× unrolled: each pair of iterations uses 4 independent FMUL/FMAD
			// chains, saturating all 4 FP dispatch slots and hiding FADD latency.
			// Manual inline of mdctStoreDirectStageFMALike avoids the function
			// call overhead on this path (inlining cost 103 > budget 80).
			for ; i+1 < n4-limit1; i += 2 {
				re0 := float32(samples[xp2])
				im0 := float32(samples[xp1])
				re1 := float32(samples[xp2-2])
				im1 := float32(samples[xp1+2])
				t00 := trig[i]
				t10 := trig[n4+i]
				t01 := trig[i+1]
				t11 := trig[n4+i+1]
				yr0 := mdctEncodeFMA32(re0, t00, -mdctMul(im0, t10))
				yi0 := mdctEncodeFMA32(im0, t00, mdctMul(re0, t10))
				yr1 := mdctEncodeFMA32(re1, t01, -mdctMul(im1, t11))
				yi1 := mdctEncodeFMA32(im1, t01, mdctMul(re1, t11))
				b0, b1 := bitrev[i], bitrev[i+1]
				fftStage[b0].r = yr0 * preScale
				fftStage[b0].i = yi0 * preScale
				fftStage[b1].r = yr1 * preScale
				fftStage[b1].i = yi1 * preScale
				xp1 += 4
				xp2 -= 4
			}
			for ; i < n4-limit1; i++ {
				re := float32(samples[xp2])
				im := float32(samples[xp1])
				t0 := trig[i]
				t1 := trig[n4+i]
				mdctStoreDirectStageFMALike(fftStage, bitrev[i], preScale, re, im, t0, t1)
				xp1 += 2
				xp2 -= 2
			}

			// Trailing windowed fold, same blocked NEON treatment.
			if tail := n4 - i; mdctUseNeonMidFold && tail >= 4 {
				blocks := tail >> 2
				done := blocks * 4
				if xp2-2*done+2 >= 0 && wp2-2*done+2 >= 0 && xp1-n2 >= 0 &&
					xp1+2*done-1 < len(samples) && xp2+n2+1 < len(samples) &&
					wp1+2*done-1 < len(window) && wp2+1 < len(window) {
					mdctFold3StoreNeon(fftStage, bitrev, samples, window, trig, i, n4, n2, xp1, xp2, wp1, wp2, blocks, preScale)
					i += done
					xp1 += 2 * done
					xp2 -= 2 * done
					wp1 += 2 * done
					wp2 -= 2 * done
				}
			}
			for ; i+1 < n4; i += 2 {
				re0 := mdctMulSubMixAlt(float32(samples[xp2]), float32(samples[xp1-n2]), window[wp2], window[wp1])
				im0 := mdctMulAddMixEncode(float32(samples[xp1]), float32(samples[xp2+n2]), window[wp2], window[wp1])
				re1 := mdctMulSubMixAlt(float32(samples[xp2-2]), float32(samples[xp1-n2+2]), window[wp2-2], window[wp1+2])
				im1 := mdctMulAddMixEncode(float32(samples[xp1+2]), float32(samples[xp2+n2-2]), window[wp2-2], window[wp1+2])
				t00, t10, t01, t11 := trig[i], trig[n4+i], trig[i+1], trig[n4+i+1]
				yr0 := mdctEncodeFMA32(re0, t00, -mdctMul(im0, t10))
				yi0 := mdctEncodeFMA32(im0, t00, mdctMul(re0, t10))
				yr1 := mdctEncodeFMA32(re1, t01, -mdctMul(im1, t11))
				yi1 := mdctEncodeFMA32(im1, t01, mdctMul(re1, t11))
				b0, b1 := bitrev[i], bitrev[i+1]
				fftStage[b0].r = yr0 * preScale
				fftStage[b0].i = yi0 * preScale
				fftStage[b1].r = yr1 * preScale
				fftStage[b1].i = yi1 * preScale
				xp1 += 4
				xp2 -= 4
				wp1 += 4
				wp2 -= 4
			}
			for ; i < n4; i++ {
				re := mdctMulSubMixAlt(float32(samples[xp2]), float32(samples[xp1-n2]), window[wp2], window[wp1])
				im := mdctMulAddMixEncode(float32(samples[xp1]), float32(samples[xp2+n2]), window[wp2], window[wp1])
				t0, t1 := trig[i], trig[n4+i]
				yr := mdctEncodeFMA32(re, t0, -mdctMul(im, t1))
				yi := mdctEncodeFMA32(im, t0, mdctMul(re, t1))
				b := bitrev[i]
				fftStage[b].r = yr * preScale
				fftStage[b].i = yi * preScale
				xp1 += 2
				xp2 -= 2
				wp1 += 2
				wp2 -= 2
			}
		} else {
			for ; i < limit1; i++ {
				re := mdctMulAddMixEncode(float32(samples[xp1+n2]), float32(samples[xp2]), window[wp2], window[wp1])
				im := mdctMulSubMixEncode(float32(samples[xp1]), float32(samples[xp2-n2]), window[wp1], window[wp2])
				t0 := trig[i]
				t1 := trig[n4+i]
				mdctStoreDirectStage(fftStage, bitrev[i], preScale, re, im, t0, t1)
				xp1 += 2
				xp2 -= 2
				wp1 += 2
				wp2 -= 2
			}

			wp1 = 0
			wp2 = overlap - 1
			for ; i < n4-limit1; i++ {
				re := float32(samples[xp2])
				im := float32(samples[xp1])
				t0 := trig[i]
				t1 := trig[n4+i]
				mdctStoreDirectStage(fftStage, bitrev[i], preScale, re, im, t0, t1)
				xp1 += 2
				xp2 -= 2
			}

			for ; i < n4; i++ {
				re := mdctMulSubMixAlt(float32(samples[xp2]), float32(samples[xp1-n2]), window[wp2], window[wp1])
				im := mdctMulAddMixEncode(float32(samples[xp1]), float32(samples[xp2+n2]), window[wp2], window[wp1])
				t0 := trig[i]
				t1 := trig[n4+i]
				mdctStoreDirectStage(fftStage, bitrev[i], preScale, re, im, t0, t1)
				xp1 += 2
				xp2 -= 2
				wp1 += 2
				wp2 -= 2
			}
		}
	} else {
		// BCE hints for staged-fold path.
		_ = f[2*n4-1]

		for ; i < limit1; i++ {
			f[2*i] = mdctMulAddMixEncode(float32(samples[xp1+n2]), float32(samples[xp2]), window[wp2], window[wp1])
			f[2*i+1] = mdctMulSubMixEncode(float32(samples[xp1]), float32(samples[xp2-n2]), window[wp1], window[wp2])
			xp1 += 2
			xp2 -= 2
			wp1 += 2
			wp2 -= 2
		}

		wp1 = 0
		wp2 = overlap - 1
		for ; i < n4-limit1; i++ {
			f[2*i] = float32(samples[xp2])
			f[2*i+1] = float32(samples[xp1])
			xp1 += 2
			xp2 -= 2
		}

		for ; i < n4; i++ {
			f[2*i] = mdctMulSubMixAlt(float32(samples[xp2]), float32(samples[xp1-n2]), window[wp2], window[wp1])
			f[2*i+1] = mdctMulAddMixEncode(float32(samples[xp1]), float32(samples[xp2+n2]), window[wp2], window[wp1])
			xp1 += 2
			xp2 -= 2
			wp1 += 2
			wp2 -= 2
		}
	}

	// BCE hints for pre-twiddle loop.
	_ = trig[n4+n4-1] // BCE hint: trig needs n2 entries
	if useDirectKissCpx {
		// Fast path: write pre-twiddled values directly into bit-reversed kissCpx
		// scratch and run in-place FFT, avoiding intermediate complex64 materialization.
		bitrev := st.bitrev
		if !fuseDirectStage {
			_ = bitrev[n4-1]   // BCE hint
			_ = fftStage[n4-1] // BCE hint
			if mdctUseFMALikeMixEnabled {
				for i = range n4 {
					re := f[2*i]
					im := f[2*i+1]
					t0 := trig[i]
					t1 := trig[n4+i]
					mdctStoreDirectStageFMALike(fftStage, bitrev[i], preScale, re, im, t0, t1)
				}
			} else {
				for i = range n4 {
					re := f[2*i]
					im := f[2*i+1]
					t0 := trig[i]
					t1 := trig[n4+i]
					mdctStoreDirectStage(fftStage, bitrev[i], preScale, re, im, t0, t1)
				}
			}
		}

		st.fftImpl(fftStage)
	} else {
		// Fallback: keep the existing complex64 path for unsupported sizes.
		_ = fftIn[n4-1] // BCE hint
		for i = range n4 {
			re := f[2*i]
			im := f[2*i+1]
			t0 := trig[i]
			t1 := trig[n4+i]
			yr := mdctMul(re, t0) - mdctMul(im, t1)
			yi := mdctMul(im, t0) + mdctMul(re, t1)
			fftIn[i] = complex(yr*preScale, yi*preScale)
		}
		kissFFT32To(fftOut, fftIn[:n4], fftTmp)
	}
	// BCE hints for post-twiddle loop.
	_ = coeffs[n2-1] // BCE hint
	trigHi := trig[n4:]
	if useDirectKissCpx {
		_ = fftStage[n4-1] // BCE hint
		_ = trigHi[n4-1]
		lo := 0
		hi := n2 - 1
		i = 0
		// Mirror 4-wide block pairs tile both coefficient ends contiguously;
		// the NEON kernel runs them with the exact scalar op sequence
		// (bit-identical per element), and the scalar loop finishes the
		// n4%8 middle. QEXT moves the scale here, so that build keeps the
		// scalar loop.
		if mdctUsePostTwiddleNeon && !mdctQEXTScalePlacement {
			if pairBlocks := n4 >> 3; pairBlocks > 0 {
				mdctPostTwiddleNeon(coeffs, fftStage, trig, n2, n4, pairBlocks)
				i = 4 * pairBlocks
				lo += 2 * i
				hi -= 2 * i
			}
		}
		top := n4 - i
		if !mdctQEXTScalePlacement {
			// 2x unroll: 8 independent FMULs per pair of iterations hide
			// 2-cycle FMUL latency and saturate the 4-wide FP dispatch.
			for ; i+1 < top; i += 2 {
				re0 := fftStage[i].r
				im0 := fftStage[i].i
				re1 := fftStage[i+1].r
				im1 := fftStage[i+1].i
				t00 := trig[i]
				t10 := trigHi[i]
				t01 := trig[i+1]
				t11 := trigHi[i+1]
				yr0 := mdctMul(im0, t10) - mdctMul(re0, t00)
				yi0 := mdctMul(re0, t10) + mdctMul(im0, t00)
				yr1 := mdctMul(im1, t11) - mdctMul(re1, t01)
				yi1 := mdctMul(re1, t11) + mdctMul(im1, t01)
				coeffs[lo] = yr0
				coeffs[hi] = yi0
				coeffs[lo+2] = yr1
				coeffs[hi-2] = yi1
				lo += 4
				hi -= 4
			}
		}
		for ; i < top; i++ {
			re := fftStage[i].r
			im := fftStage[i].i
			t0 := trig[i]
			t1 := trigHi[i]
			if mdctQEXTScalePlacement {
				t0 *= postScale
				t1 *= postScale
			}
			yr := mdctMul(im, t1) - mdctMul(re, t0)
			yi := mdctMul(re, t1) + mdctMul(im, t0)
			coeffs[lo] = yr
			coeffs[hi] = yi
			lo += 2
			hi -= 2
		}
	} else {
		_ = fftOut[n4-1] // BCE hint
		_ = trigHi[n4-1]
		lo := 0
		hi := n2 - 1
		for i = range n4 {
			re := real(fftOut[i])
			im := imag(fftOut[i])
			t0 := trig[i]
			t1 := trigHi[i]
			if mdctQEXTScalePlacement {
				t0 *= postScale
				t1 *= postScale
			}
			yr := mdctMul(im, t1) - mdctMul(re, t0)
			yi := mdctMul(re, t1) + mdctMul(im, t0)
			coeffs[lo] = yr
			coeffs[hi] = yi
			lo += 2
			hi -= 2
		}
	}

}

// mdctScratch computes the MDCT using scratch buffers to avoid allocations.
func mdctScratch(samples []float32, scratch *encoderScratch) []float32 {
	if len(samples) == 0 {
		return nil
	}

	if len(samples) > Overlap {
		frameSize := len(samples) - Overlap
		if ValidFrameSize(frameSize) {
			return mdctForwardOverlapScratch(samples, Overlap, scratch)
		}
	}

	return mdctStandard(samples)
}

func mdctScratchF32(samples []float32, scratch *encoderScratch) []float32 {
	if len(samples) == 0 {
		return nil
	}

	if len(samples) > Overlap {
		frameSize := len(samples) - Overlap
		if ValidFrameSize(frameSize) {
			coeffs := ensureFloat32Slice(&scratch.mdctCoeffsF32, frameSize)
			mdctForwardOverlapF32Scratch(samples, Overlap, coeffs,
				scratch.mdctF, scratch.mdctFFTIn, scratch.mdctFFTOut, scratch.mdctFFTTmp)
			return coeffs
		}
	}

	return nil
}

func mdctScratchF32Coeffs(samples []float32, scratch *encoderScratch) []float32 {
	if len(samples) == 0 {
		return nil
	}

	if len(samples) > Overlap {
		frameSize := len(samples) - Overlap
		if ValidFrameSize(frameSize) {
			coeffs := ensureFloat32Slice(&scratch.mdctCoeffsF32, frameSize)
			mdctForwardOverlapF32Scratch(samples, Overlap, coeffs,
				scratch.mdctF, scratch.mdctFFTIn, scratch.mdctFFTOut, scratch.mdctFFTTmp)
			return coeffs
		}
	}

	return nil
}

// mdctShortScratch computes the short-block MDCT using scratch buffers.
func mdctShortScratch(samples []float32, shortBlocks int, scratch *encoderScratch) []float32 {
	if shortBlocks <= 1 {
		return mdctScratch(samples, scratch)
	}
	if len(samples) == 0 {
		return nil
	}

	if len(samples) > Overlap {
		frameSize := len(samples) - Overlap
		if ValidFrameSize(frameSize) && frameSize%shortBlocks == 0 {
			return mdctForwardShortOverlapScratch(samples, Overlap, shortBlocks, scratch)
		}
	}

	return mdctShortStandard(samples, shortBlocks)
}

func mdctShortScratchF32(samples []float32, shortBlocks int, scratch *encoderScratch) []float32 {
	if shortBlocks <= 1 {
		return mdctScratchF32(samples, scratch)
	}
	if len(samples) == 0 {
		return nil
	}

	if len(samples) > Overlap {
		frameSize := len(samples) - Overlap
		if ValidFrameSize(frameSize) && frameSize%shortBlocks == 0 {
			return mdctForwardShortOverlapScratchF32(samples, Overlap, shortBlocks, scratch)
		}
	}

	return nil
}

func mdctShortScratchF32Coeffs(samples []float32, shortBlocks int, scratch *encoderScratch) []float32 {
	if shortBlocks <= 1 {
		return mdctScratchF32Coeffs(samples, scratch)
	}
	if len(samples) == 0 {
		return nil
	}

	if len(samples) > Overlap {
		frameSize := len(samples) - Overlap
		if ValidFrameSize(frameSize) && frameSize%shortBlocks == 0 {
			return mdctForwardShortOverlapScratchF32Coeffs(samples, Overlap, shortBlocks, scratch)
		}
	}

	return nil
}

// mdctShortBlocksCore is a helper that processes multiple short MDCT blocks.
// It calls blockMDCT for each short block and interleaves results into output.
func mdctShortBlocksCore(samples []float32, overlap, shortBlocks, shortSize int, output, blockCoeffs []float32, blockMDCT func(block, coeffs []float32)) {
	if shortBlocks <= 0 || shortSize <= 0 {
		return
	}
	frameSize := shortBlocks * shortSize
	if len(output) < frameSize || len(blockCoeffs) < shortSize {
		return
	}
	output = output[:frameSize:frameSize]
	blockCoeffs = blockCoeffs[:shortSize:shortSize]
	_ = output[frameSize-1]
	_ = blockCoeffs[shortSize-1]
	for b := range shortBlocks {
		start := b * shortSize
		end := start + shortSize + overlap
		if end > len(samples) {
			break
		}

		// Compute short block MDCT
		blockMDCT(samples[start:end], blockCoeffs)

		// Interleave coefficients into output
		outIdx := b
		for i := range shortSize {
			output[outIdx] = blockCoeffs[i]
			outIdx += shortBlocks
		}
	}
}

func mdctForwardShortOverlapScratchIntoF32Coeffs(samples []float32, overlap, shortBlocks int, output []float32, scratch *encoderScratch) []float32 {
	if shortBlocks <= 1 {
		if len(output) >= len(samples)-overlap {
			mdctForwardOverlapF32Scratch(samples, overlap, output,
				scratch.mdctF, scratch.mdctFFTIn, scratch.mdctFFTOut, scratch.mdctFFTTmp)
			return output[:len(samples)-overlap]
		}
		return mdctForwardOverlapScratchF32Coeffs(samples, overlap, scratch)
	}
	if len(samples) <= overlap || overlap < 0 {
		return nil
	}

	frameSize := len(samples) - overlap
	if frameSize <= 0 || frameSize%shortBlocks != 0 {
		return nil
	}
	shortSize := frameSize / shortBlocks
	blockCoeffs := ensureFloat32Slice(&scratch.mdctBlockCoeffs, shortSize)
	for b := range shortBlocks {
		start := b * shortSize
		end := start + shortSize + overlap
		if end > len(samples) {
			break
		}
		mdctForwardOverlapF32Scratch(samples[start:end], overlap, blockCoeffs,
			scratch.mdctF, scratch.mdctFFTIn, scratch.mdctFFTOut, scratch.mdctFFTTmp)
		for i := range blockCoeffs {
			outIdx := b + i*shortBlocks
			if outIdx < len(output) {
				output[outIdx] = blockCoeffs[i]
			}
		}
	}
	return output[:frameSize]
}

// mdctForwardOverlapScratch computes the MDCT forward transform using scratch buffers.
func mdctForwardOverlapScratch(samples []float32, overlap int, scratch *encoderScratch) []float32 {
	frameSize := len(samples) - overlap
	if frameSize <= 0 {
		return nil
	}

	// Use scratch buffer for coeffs output
	coeffs := ensureFloat32Slice(&scratch.mdctCoeffsF32, frameSize)

	// Call the scratch-aware version with all buffers
	mdctForwardOverlapF32Scratch(samples, overlap, coeffs,
		scratch.mdctF, scratch.mdctFFTIn, scratch.mdctFFTOut, scratch.mdctFFTTmp)

	return coeffs
}

func mdctForwardOverlapScratchF32(samples []float32, overlap int, scratch *encoderScratch) []float32 {
	frameSize := len(samples) - overlap
	if frameSize <= 0 {
		return nil
	}
	coeffs := ensureFloat32Slice(&scratch.mdctCoeffsF32, frameSize)
	mdctForwardOverlapF32Scratch(samples, overlap, coeffs,
		scratch.mdctF, scratch.mdctFFTIn, scratch.mdctFFTOut, scratch.mdctFFTTmp)
	return coeffs
}

func mdctForwardOverlapScratchF32Coeffs(samples []float32, overlap int, scratch *encoderScratch) []float32 {
	frameSize := len(samples) - overlap
	if frameSize <= 0 {
		return nil
	}
	coeffs := ensureFloat32Slice(&scratch.mdctCoeffsF32, frameSize)
	mdctForwardOverlapF32Scratch(samples, overlap, coeffs,
		scratch.mdctF, scratch.mdctFFTIn, scratch.mdctFFTOut, scratch.mdctFFTTmp)
	return coeffs
}

// mdctForwardShortOverlapScratch computes short-block MDCT using scratch buffers.
func mdctForwardShortOverlapScratch(samples []float32, overlap, shortBlocks int, scratch *encoderScratch) []float32 {
	if shortBlocks <= 1 {
		return mdctForwardOverlapScratch(samples, overlap, scratch)
	}
	if len(samples) <= overlap || overlap < 0 {
		return nil
	}

	frameSize := len(samples) - overlap
	if frameSize <= 0 || frameSize%shortBlocks != 0 {
		return nil
	}

	shortSize := frameSize / shortBlocks
	output := ensureFloat32Slice(&scratch.mdctCoeffsF32, frameSize)

	// Use scratch buffer for per-block coefficients
	blockCoeffs := ensureFloat32Slice(&scratch.mdctBlockCoeffs, shortSize)

	for b := range shortBlocks {
		start := b * shortSize
		end := start + shortSize + overlap
		if end > len(samples) {
			break
		}

		// Compute short block MDCT using scratch buffers
		mdctForwardOverlapF32Scratch(samples[start:end], overlap, blockCoeffs,
			scratch.mdctF, scratch.mdctFFTIn, scratch.mdctFFTOut, scratch.mdctFFTTmp)

		for i := range blockCoeffs {
			outIdx := b + i*shortBlocks
			if outIdx < len(output) {
				output[outIdx] = blockCoeffs[i]
			}
		}
	}

	return output
}

func mdctForwardShortOverlapScratchF32(samples []float32, overlap, shortBlocks int, scratch *encoderScratch) []float32 {
	if shortBlocks <= 1 {
		return mdctForwardOverlapScratchF32(samples, overlap, scratch)
	}
	if len(samples) <= overlap || overlap < 0 {
		return nil
	}

	frameSize := len(samples) - overlap
	if frameSize <= 0 || frameSize%shortBlocks != 0 {
		return nil
	}
	shortSize := frameSize / shortBlocks
	output := ensureFloat32Slice(&scratch.mdctCoeffsF32, frameSize)
	blockCoeffs := ensureFloat32Slice(&scratch.mdctBlockCoeffs, shortSize)
	for b := range shortBlocks {
		start := b * shortSize
		end := start + shortSize + overlap
		if end > len(samples) {
			break
		}
		mdctForwardOverlapF32Scratch(samples[start:end], overlap, blockCoeffs,
			scratch.mdctF, scratch.mdctFFTIn, scratch.mdctFFTOut, scratch.mdctFFTTmp)
		for i := range blockCoeffs {
			outIdx := b + i*shortBlocks
			if outIdx < len(output) {
				output[outIdx] = blockCoeffs[i]
			}
		}
	}
	return output
}

func mdctForwardShortOverlapScratchF32Coeffs(samples []float32, overlap, shortBlocks int, scratch *encoderScratch) []float32 {
	if shortBlocks <= 1 {
		return mdctForwardOverlapScratchF32Coeffs(samples, overlap, scratch)
	}
	if len(samples) <= overlap || overlap < 0 {
		return nil
	}

	frameSize := len(samples) - overlap
	if frameSize <= 0 || frameSize%shortBlocks != 0 {
		return nil
	}
	shortSize := frameSize / shortBlocks
	output := ensureFloat32Slice(&scratch.mdctCoeffsF32, frameSize)
	blockCoeffs := ensureFloat32Slice(&scratch.mdctBlockCoeffs, shortSize)
	for b := range shortBlocks {
		start := b * shortSize
		end := start + shortSize + overlap
		if end > len(samples) {
			break
		}
		mdctForwardOverlapF32Scratch(samples[start:end], overlap, blockCoeffs,
			scratch.mdctF, scratch.mdctFFTIn, scratch.mdctFFTOut, scratch.mdctFFTTmp)
		for i := range blockCoeffs {
			outIdx := b + i*shortBlocks
			if outIdx < len(output) {
				output[outIdx] = blockCoeffs[i]
			}
		}
	}
	return output
}

// mdctForwardShortOverlap computes interleaved MDCT coefficients for transient frames.
// samples length must be frameSize+overlap.
func mdctForwardShortOverlap(samples []float32, overlap, shortBlocks int) []float32 {
	if shortBlocks <= 1 {
		return mdctForwardOverlap(samples, overlap)
	}
	if len(samples) <= overlap || overlap < 0 {
		return nil
	}

	frameSize := len(samples) - overlap
	if frameSize <= 0 || frameSize%shortBlocks != 0 {
		return nil
	}

	shortSize := frameSize / shortBlocks
	output := make([]float32, frameSize)

	for b := range shortBlocks {
		start := b * shortSize
		end := start + shortSize + overlap
		if end > len(samples) {
			break
		}
		blockCoeffs := mdctForwardOverlap(samples[start:end], overlap)
		for i, v := range blockCoeffs {
			outIdx := b + i*shortBlocks
			if outIdx < len(output) {
				output[outIdx] = v
			}
		}
	}

	return output
}

// mdctStandard computes the direct MDCT for legacy 2*N inputs.
func mdctStandard(samples []float32) []float32 {
	if len(samples) == 0 {
		return nil
	}

	// Input is 2*N samples, output is N coefficients
	N2 := len(samples)
	N := N2 / 2
	if N <= 0 {
		return nil
	}

	windowed := make([]float32, N2)
	copy(windowed, samples)
	applyMDCTWindow(windowed)

	coeffs := make([]float32, N)
	// scale = 1.0 for mdctStandard (no normalization)
	mdctCoreCompute(windowed, coeffs, 1.0)

	return coeffs
}

func mdctShortStandard(samples []float32, shortBlocks int) []float32 {
	totalSamples := len(samples)
	if totalSamples == 0 {
		return nil
	}

	shortSampleSize := totalSamples / shortBlocks
	shortCoeffSize := shortSampleSize / 2
	if shortSampleSize <= 0 || shortCoeffSize <= 0 {
		return mdctStandard(samples)
	}

	totalCoeffs := shortCoeffSize * shortBlocks
	output := make([]float32, totalCoeffs)

	for b := range shortBlocks {
		shortSamples := make([]float32, shortSampleSize)
		startIdx := b * shortSampleSize
		for i := 0; i < shortSampleSize && startIdx+i < totalSamples; i++ {
			shortSamples[i] = samples[startIdx+i]
		}

		shortCoeffs := mdctDirect(shortSamples)
		for i := 0; i < len(shortCoeffs) && i < shortCoeffSize; i++ {
			outIdx := b + i*shortBlocks
			if outIdx < totalCoeffs {
				output[outIdx] = shortCoeffs[i]
			}
		}
	}

	return output
}
