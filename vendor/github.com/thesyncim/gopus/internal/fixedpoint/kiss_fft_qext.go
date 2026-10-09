//go:build gopus_fixed_point && gopus_qext

package fixedpoint

// Q31 radix butterflies for the ENABLE_QEXT KISS-FFT path. Arithmetic mirrors
// libopus celt/kiss_fft.c with MULT32_32_P31_ovflw from fixed_generic.h.

// qextMul matches MULT32_32_P31_ovflw on OPUS_FAST_INT64 targets.
// The +2^30 rounding term and arithmetic shift match fixed_generic.h.
func qextMul(a, b int32) int32 {
	return int32((int64(a)*int64(b) + (1 << 30)) >> 31)
}

func qextCMul(a FFTCpx, b QEXTFFTTwiddle) FFTCpx {
	return FFTCpx{
		R: sub32Ovflw(qextMul(a.R, b.R), qextMul(a.I, b.I)),
		I: add32Ovflw(qextMul(a.R, b.I), qextMul(a.I, b.R)),
	}
}

// qextKFBfly2Tw is QCONST32(0.7071067812f, COEF_SHIFT-1) with COEF_SHIFT==32, i.e.
// round(0.7071067812 * 2^31) stored as a Q31 twiddle (celt_coef).
const qextKFBfly2Tw int32 = 1518500224

// QEXTFFTTwiddle is a complex twiddle factor with int32 Q31 real and imaginary
// parts, matching libopus kiss_twiddle_cpx when kiss_twiddle_scalar == celt_coef
// == opus_val32 in the ENABLE_QEXT build.
type QEXTFFTTwiddle struct {
	R int32
	I int32
}

// qextKFBfly2 is the radix-2 KISS-FFT butterfly from libopus celt/kiss_fft.c
// (FIXED_POINT, non-CUSTOM_MODES path). It operates in place on fout starting
// at offset, processing N groups; m is always 4 in this path (the radix-2 stage
// immediately follows a radix-4 stage), so each group spans 8 complex samples.
//
// fout must hold at least offset + 8*N elements.
func qextKFBfly2(fout []FFTCpx, offset, n int) {
	tw := qextKFBfly2Tw
	base := offset
	for i := 0; i < n; i++ {
		// Fout points at base; Fout2 = Fout + 4.
		f0 := base
		f2 := base + 4

		// k == 0: t = Fout2[0]; C_SUB(Fout2[0], Fout[0], t); C_ADDTO(Fout[0], t).
		var t FFTCpx
		t = fout[f2+0]
		fout[f2+0] = FFTCpx{
			R: sub32Ovflw(fout[f0+0].R, t.R),
			I: sub32Ovflw(fout[f0+0].I, t.I),
		}
		fout[f0+0] = FFTCpx{
			R: add32Ovflw(fout[f0+0].R, t.R),
			I: add32Ovflw(fout[f0+0].I, t.I),
		}

		// k == 1.
		t.R = qextMul(add32Ovflw(fout[f2+1].R, fout[f2+1].I), tw)
		t.I = qextMul(sub32Ovflw(fout[f2+1].I, fout[f2+1].R), tw)
		fout[f2+1] = FFTCpx{
			R: sub32Ovflw(fout[f0+1].R, t.R),
			I: sub32Ovflw(fout[f0+1].I, t.I),
		}
		fout[f0+1] = FFTCpx{
			R: add32Ovflw(fout[f0+1].R, t.R),
			I: add32Ovflw(fout[f0+1].I, t.I),
		}

		// k == 2.
		t.R = fout[f2+2].I
		t.I = neg32Ovflw(fout[f2+2].R)
		fout[f2+2] = FFTCpx{
			R: sub32Ovflw(fout[f0+2].R, t.R),
			I: sub32Ovflw(fout[f0+2].I, t.I),
		}
		fout[f0+2] = FFTCpx{
			R: add32Ovflw(fout[f0+2].R, t.R),
			I: add32Ovflw(fout[f0+2].I, t.I),
		}

		// k == 3.
		t.R = qextMul(sub32Ovflw(fout[f2+3].I, fout[f2+3].R), tw)
		t.I = qextMul(neg32Ovflw(add32Ovflw(fout[f2+3].I, fout[f2+3].R)), tw)
		fout[f2+3] = FFTCpx{
			R: sub32Ovflw(fout[f0+3].R, t.R),
			I: sub32Ovflw(fout[f0+3].I, t.I),
		}
		fout[f0+3] = FFTCpx{
			R: add32Ovflw(fout[f0+3].R, t.R),
			I: add32Ovflw(fout[f0+3].I, t.I),
		}

		base += 8
	}
}

// qextKFBfly4 is the radix-4 KISS-FFT butterfly from libopus celt/kiss_fft.c
// (FIXED_POINT). It operates in place on fout starting at offset.
//
// The m==1 case is the degenerate path where all twiddles are 1: it processes N
// groups of 4 consecutive complex samples and ignores tw/fstride/mm.
//
// For m>1 (m a multiple of 4) the general path applies the twiddle table tw with
// the given fstride and mm strides, exactly as kf_bfly4 does against
// st->twiddles. tw must hold at least (m-1)*3*fstride+1 entries, and fout must
// hold offset + (N-1)*mm + m3 + m elements.
func qextKFBfly4(fout []FFTCpx, offset int, tw []QEXTFFTTwiddle, fstride, m, n, mm int) {
	if m == 1 {
		base := offset
		for i := 0; i < n; i++ {
			f := base
			scratch0 := FFTCpx{
				R: sub32Ovflw(fout[f+0].R, fout[f+2].R),
				I: sub32Ovflw(fout[f+0].I, fout[f+2].I),
			}
			fout[f+0] = FFTCpx{
				R: add32Ovflw(fout[f+0].R, fout[f+2].R),
				I: add32Ovflw(fout[f+0].I, fout[f+2].I),
			}
			scratch1 := FFTCpx{
				R: add32Ovflw(fout[f+1].R, fout[f+3].R),
				I: add32Ovflw(fout[f+1].I, fout[f+3].I),
			}
			fout[f+2] = FFTCpx{
				R: sub32Ovflw(fout[f+0].R, scratch1.R),
				I: sub32Ovflw(fout[f+0].I, scratch1.I),
			}
			fout[f+0] = FFTCpx{
				R: add32Ovflw(fout[f+0].R, scratch1.R),
				I: add32Ovflw(fout[f+0].I, scratch1.I),
			}
			scratch1 = FFTCpx{
				R: sub32Ovflw(fout[f+1].R, fout[f+3].R),
				I: sub32Ovflw(fout[f+1].I, fout[f+3].I),
			}

			fout[f+1].R = add32Ovflw(scratch0.R, scratch1.I)
			fout[f+1].I = sub32Ovflw(scratch0.I, scratch1.R)
			fout[f+3].R = sub32Ovflw(scratch0.R, scratch1.I)
			fout[f+3].I = add32Ovflw(scratch0.I, scratch1.R)
			base += 4
		}
		return
	}

	m2 := 2 * m
	m3 := 3 * m
	for i := 0; i < n; i++ {
		f := offset + i*mm
		var t1, t2, t3 int // indices into tw for tw1, tw2, tw3
		for j := 0; j < m; j++ {
			scratch0 := qextCMul(fout[f+m], tw[t1])
			scratch1 := qextCMul(fout[f+m2], tw[t2])
			scratch2 := qextCMul(fout[f+m3], tw[t3])

			scratch5 := FFTCpx{
				R: sub32Ovflw(fout[f].R, scratch1.R),
				I: sub32Ovflw(fout[f].I, scratch1.I),
			}
			fout[f] = FFTCpx{
				R: add32Ovflw(fout[f].R, scratch1.R),
				I: add32Ovflw(fout[f].I, scratch1.I),
			}
			scratch3 := FFTCpx{
				R: add32Ovflw(scratch0.R, scratch2.R),
				I: add32Ovflw(scratch0.I, scratch2.I),
			}
			scratch4 := FFTCpx{
				R: sub32Ovflw(scratch0.R, scratch2.R),
				I: sub32Ovflw(scratch0.I, scratch2.I),
			}
			fout[f+m2] = FFTCpx{
				R: sub32Ovflw(fout[f].R, scratch3.R),
				I: sub32Ovflw(fout[f].I, scratch3.I),
			}
			t1 += fstride
			t2 += fstride * 2
			t3 += fstride * 3
			fout[f] = FFTCpx{
				R: add32Ovflw(fout[f].R, scratch3.R),
				I: add32Ovflw(fout[f].I, scratch3.I),
			}

			fout[f+m].R = add32Ovflw(scratch5.R, scratch4.I)
			fout[f+m].I = sub32Ovflw(scratch5.I, scratch4.R)
			fout[f+m3].R = sub32Ovflw(scratch5.R, scratch4.I)
			fout[f+m3].I = add32Ovflw(scratch5.I, scratch4.R)
			f++
		}
	}
}

// qextKFBfly3Epi3I is -QCONST32(0.86602540f, COEF_SHIFT-1) with COEF_SHIFT==32, the
// imaginary part of epi3 used by kf_bfly3 in the ENABLE_QEXT build (the real
// part is unused).
const qextKFBfly3Epi3I int32 = -1859775360

// qextKFBfly3 is the radix-3 KISS-FFT butterfly from libopus celt/kiss_fft.c
// (FIXED_POINT). It operates in place on fout starting at offset, applying the
// twiddle table tw with the given fstride and mm strides exactly as kf_bfly3
// does against st->twiddles. m is a multiple of 4 for non-custom modes.
func qextKFBfly3(fout []FFTCpx, offset int, tw []QEXTFFTTwiddle, fstride, m, n, mm int) {
	m2 := 2 * m
	epi3i := qextKFBfly3Epi3I
	for i := 0; i < n; i++ {
		f := offset + i*mm
		var t1, t2 int
		for k := 0; k < m; k++ {
			scratch1 := qextCMul(fout[f+m], tw[t1])
			scratch2 := qextCMul(fout[f+m2], tw[t2])

			scratch3 := FFTCpx{
				R: add32Ovflw(scratch1.R, scratch2.R),
				I: add32Ovflw(scratch1.I, scratch2.I),
			}
			scratch0 := FFTCpx{
				R: sub32Ovflw(scratch1.R, scratch2.R),
				I: sub32Ovflw(scratch1.I, scratch2.I),
			}
			t1 += fstride
			t2 += fstride * 2

			fout[f+m].R = sub32Ovflw(fout[f].R, halfOf(scratch3.R))
			fout[f+m].I = sub32Ovflw(fout[f].I, halfOf(scratch3.I))

			scratch0.R = qextMul(scratch0.R, epi3i)
			scratch0.I = qextMul(scratch0.I, epi3i)

			fout[f] = FFTCpx{
				R: add32Ovflw(fout[f].R, scratch3.R),
				I: add32Ovflw(fout[f].I, scratch3.I),
			}

			fout[f+m2].R = add32Ovflw(fout[f+m].R, scratch0.I)
			fout[f+m2].I = sub32Ovflw(fout[f+m].I, scratch0.R)

			fout[f+m].R = sub32Ovflw(fout[f+m].R, scratch0.I)
			fout[f+m].I = add32Ovflw(fout[f+m].I, scratch0.R)

			f++
		}
	}
}

// kf_bfly5 hardcoded Q31 twiddle constants from libopus celt/kiss_fft.c
// (FIXED_POINT, COEF_SHIFT==32): ya = exp(-2pi i/5), yb = exp(-4pi i/5).
var (
	qextKFBfly5Ya = QEXTFFTTwiddle{R: 663608960, I: -2042378368}
	qextKFBfly5Yb = QEXTFFTTwiddle{R: -1737350784, I: -1262259200}
)

// qextKFBfly5 is the radix-5 KISS-FFT butterfly from libopus celt/kiss_fft.c
// (FIXED_POINT). It operates in place on fout starting at offset, applying the
// twiddle table tw with the given fstride and mm strides exactly as kf_bfly5
// does against st->twiddles. m is a multiple of 4 for non-custom modes.
func qextKFBfly5(fout []FFTCpx, offset int, tw []QEXTFFTTwiddle, fstride, m, n, mm int) {
	ya := qextKFBfly5Ya
	yb := qextKFBfly5Yb
	for i := 0; i < n; i++ {
		f0 := offset + i*mm
		f1 := f0 + m
		f2 := f0 + 2*m
		f3 := f0 + 3*m
		f4 := f0 + 4*m
		for u := 0; u < m; u++ {
			scratch0 := fout[f0]

			scratch1 := qextCMul(fout[f1], tw[u*fstride])
			scratch2 := qextCMul(fout[f2], tw[2*u*fstride])
			scratch3 := qextCMul(fout[f3], tw[3*u*fstride])
			scratch4 := qextCMul(fout[f4], tw[4*u*fstride])

			scratch7 := FFTCpx{
				R: add32Ovflw(scratch1.R, scratch4.R),
				I: add32Ovflw(scratch1.I, scratch4.I),
			}
			scratch10 := FFTCpx{
				R: sub32Ovflw(scratch1.R, scratch4.R),
				I: sub32Ovflw(scratch1.I, scratch4.I),
			}
			scratch8 := FFTCpx{
				R: add32Ovflw(scratch2.R, scratch3.R),
				I: add32Ovflw(scratch2.I, scratch3.I),
			}
			scratch9 := FFTCpx{
				R: sub32Ovflw(scratch2.R, scratch3.R),
				I: sub32Ovflw(scratch2.I, scratch3.I),
			}

			fout[f0].R = add32Ovflw(fout[f0].R, add32Ovflw(scratch7.R, scratch8.R))
			fout[f0].I = add32Ovflw(fout[f0].I, add32Ovflw(scratch7.I, scratch8.I))

			scratch5 := FFTCpx{
				R: add32Ovflw(scratch0.R, add32Ovflw(qextMul(scratch7.R, ya.R), qextMul(scratch8.R, yb.R))),
				I: add32Ovflw(scratch0.I, add32Ovflw(qextMul(scratch7.I, ya.R), qextMul(scratch8.I, yb.R))),
			}

			scratch6 := FFTCpx{
				R: add32Ovflw(qextMul(scratch10.I, ya.I), qextMul(scratch9.I, yb.I)),
				I: neg32Ovflw(add32Ovflw(qextMul(scratch10.R, ya.I), qextMul(scratch9.R, yb.I))),
			}

			fout[f1] = FFTCpx{
				R: sub32Ovflw(scratch5.R, scratch6.R),
				I: sub32Ovflw(scratch5.I, scratch6.I),
			}
			fout[f4] = FFTCpx{
				R: add32Ovflw(scratch5.R, scratch6.R),
				I: add32Ovflw(scratch5.I, scratch6.I),
			}

			scratch11 := FFTCpx{
				R: add32Ovflw(scratch0.R, add32Ovflw(qextMul(scratch7.R, yb.R), qextMul(scratch8.R, ya.R))),
				I: add32Ovflw(scratch0.I, add32Ovflw(qextMul(scratch7.I, yb.R), qextMul(scratch8.I, ya.R))),
			}
			scratch12 := FFTCpx{
				R: sub32Ovflw(qextMul(scratch9.I, ya.I), qextMul(scratch10.I, yb.I)),
				I: sub32Ovflw(qextMul(scratch10.R, yb.I), qextMul(scratch9.R, ya.I)),
			}

			fout[f2] = FFTCpx{
				R: add32Ovflw(scratch11.R, scratch12.R),
				I: add32Ovflw(scratch11.I, scratch12.I),
			}
			fout[f3] = FFTCpx{
				R: sub32Ovflw(scratch11.R, scratch12.R),
				I: sub32Ovflw(scratch11.I, scratch12.I),
			}

			f0++
			f1++
			f2++
			f3++
			f4++
		}
	}
}
