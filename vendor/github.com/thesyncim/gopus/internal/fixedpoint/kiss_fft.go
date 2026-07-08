//go:build gopus_fixed_point

package fixedpoint

// Integer KISS-FFT building blocks ported from libopus celt/kiss_fft.c under
// FIXED_POINT (without ENABLE_QEXT). The complex samples are kiss_fft_cpx with
// kiss_fft_scalar == opus_int32 (Q-format carried by the caller), and the
// twiddles are kiss_twiddle_scalar == celt_coef == opus_val16 (Q15, since
// COEF_SHIFT-1 == 15). The arithmetic reproduces the libopus integer kernels
// bit-for-bit on OPUS_FAST_INT64 targets.

// FFTCpx is a complex sample with int32 real and imaginary parts, matching
// libopus kiss_fft_cpx in the FIXED_POINT (non-QEXT) build.
type FFTCpx struct {
	R int32
	I int32
}

// kfBfly2Tw is QCONST32(0.7071067812f, COEF_SHIFT-1) with COEF_SHIFT==16, i.e.
// round(0.7071067812 * 2^15) stored as a Q15 twiddle (opus_val16).
const kfBfly2Tw int16 = 23170

// FFTTwiddle is a complex twiddle factor with int16 Q15 real and imaginary
// parts, matching libopus kiss_twiddle_cpx when kiss_twiddle_scalar == celt_coef
// == opus_val16 in the FIXED_POINT (non-QEXT) build.
type FFTTwiddle struct {
	R int16
	I int16
}

// cMul implements libopus C_MUL(m, a, b) for a sample a (int32 complex) times a
// twiddle b (int16 Q15 complex):
//
//	m.r = SUB32_ovflw(S_MUL(a.r, b.r), S_MUL(a.i, b.i))
//	m.i = ADD32_ovflw(S_MUL(a.r, b.i), S_MUL(a.i, b.r))
//
// where S_MUL(x, y) == MULT16_32_Q15(y, x) treats y as the int16 twiddle.
func cMul(a FFTCpx, b FFTTwiddle) FFTCpx {
	return FFTCpx{
		R: sub32Ovflw(sMul(a.R, b.R), sMul(a.I, b.I)),
		I: add32Ovflw(sMul(a.R, b.I), sMul(a.I, b.R)),
	}
}

// halfOf implements libopus HALF_OF(x) == (x)>>1 (arithmetic shift) on the
// FIXED_POINT build.
func halfOf(x int32) int32 { return x >> 1 }

// sMul implements libopus S_MUL(a, b) == MULT16_32_Q15(b, a) on an
// OPUS_FAST_INT64 target: (opus_val32)((opus_int64)(opus_val16)b * a >> 15).
// Here a is the int32 sample and b is the int16 twiddle.
func sMul(a int32, b int16) int32 {
	return int32((int64(b) * int64(a)) >> 15)
}

// add32Ovflw implements libopus ADD32_ovflw: add as uint32, ignoring overflow.
func add32Ovflw(a, b int32) int32 {
	return int32(uint32(a) + uint32(b))
}

// sub32Ovflw implements libopus SUB32_ovflw: subtract as uint32, ignoring overflow.
func sub32Ovflw(a, b int32) int32 {
	return int32(uint32(a) - uint32(b))
}

// neg32Ovflw implements libopus NEG32_ovflw: negate as uint32, ignoring overflow.
func neg32Ovflw(a int32) int32 {
	return int32(0 - uint32(a))
}

// KFBfly2 is the radix-2 KISS-FFT butterfly from libopus celt/kiss_fft.c
// (FIXED_POINT, non-CUSTOM_MODES path). It operates in place on fout starting
// at offset, processing N groups; m is always 4 in this path (the radix-2 stage
// immediately follows a radix-4 stage), so each group spans 8 complex samples.
//
// fout must hold at least offset + 8*N elements.
func KFBfly2(fout []FFTCpx, offset, n int) {
	tw := kfBfly2Tw
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
		t.R = sMul(add32Ovflw(fout[f2+1].R, fout[f2+1].I), tw)
		t.I = sMul(sub32Ovflw(fout[f2+1].I, fout[f2+1].R), tw)
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
		t.R = sMul(sub32Ovflw(fout[f2+3].I, fout[f2+3].R), tw)
		t.I = sMul(neg32Ovflw(add32Ovflw(fout[f2+3].I, fout[f2+3].R)), tw)
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

// KFBfly4 is the radix-4 KISS-FFT butterfly from libopus celt/kiss_fft.c
// (FIXED_POINT). It operates in place on fout starting at offset.
//
// The m==1 case is the degenerate path where all twiddles are 1: it processes N
// groups of 4 consecutive complex samples and ignores tw/fstride/mm.
//
// For m>1 (m a multiple of 4) the general path applies the twiddle table tw with
// the given fstride and mm strides, exactly as kf_bfly4 does against
// st->twiddles. tw must hold at least (m-1)*3*fstride+1 entries, and fout must
// hold offset + (N-1)*mm + m3 + m elements.
func KFBfly4(fout []FFTCpx, offset int, tw []FFTTwiddle, fstride, m, n, mm int) {
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
			scratch0 := cMul(fout[f+m], tw[t1])
			scratch1 := cMul(fout[f+m2], tw[t2])
			scratch2 := cMul(fout[f+m3], tw[t3])

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

// kfBfly3Epi3I is -QCONST32(0.86602540f, COEF_SHIFT-1) with COEF_SHIFT==16, the
// imaginary part of epi3 used by kf_bfly3 in the FIXED_POINT build (the real
// part is unused).
const kfBfly3Epi3I int16 = -28378

// KFBfly3 is the radix-3 KISS-FFT butterfly from libopus celt/kiss_fft.c
// (FIXED_POINT). It operates in place on fout starting at offset, applying the
// twiddle table tw with the given fstride and mm strides exactly as kf_bfly3
// does against st->twiddles. m is a multiple of 4 for non-custom modes.
func KFBfly3(fout []FFTCpx, offset int, tw []FFTTwiddle, fstride, m, n, mm int) {
	m2 := 2 * m
	epi3i := kfBfly3Epi3I
	for i := 0; i < n; i++ {
		f := offset + i*mm
		var t1, t2 int
		for k := 0; k < m; k++ {
			scratch1 := cMul(fout[f+m], tw[t1])
			scratch2 := cMul(fout[f+m2], tw[t2])

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

			scratch0.R = sMul(scratch0.R, epi3i)
			scratch0.I = sMul(scratch0.I, epi3i)

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

// kf_bfly5 hardcoded Q15 twiddle constants from libopus celt/kiss_fft.c
// (FIXED_POINT, COEF_SHIFT==16): ya = exp(-2pi i/5), yb = exp(-4pi i/5).
var (
	kfBfly5Ya = FFTTwiddle{R: 10126, I: -31164}  // QCONST32(0.30901699f,15), -QCONST32(0.95105652f,15)
	kfBfly5Yb = FFTTwiddle{R: -26510, I: -19261} // -QCONST32(0.80901699f,15), -QCONST32(0.58778525f,15)
)

// KFBfly5 is the radix-5 KISS-FFT butterfly from libopus celt/kiss_fft.c
// (FIXED_POINT). It operates in place on fout starting at offset, applying the
// twiddle table tw with the given fstride and mm strides exactly as kf_bfly5
// does against st->twiddles. m is a multiple of 4 for non-custom modes.
func KFBfly5(fout []FFTCpx, offset int, tw []FFTTwiddle, fstride, m, n, mm int) {
	ya := kfBfly5Ya
	yb := kfBfly5Yb
	for i := 0; i < n; i++ {
		f0 := offset + i*mm
		f1 := f0 + m
		f2 := f0 + 2*m
		f3 := f0 + 3*m
		f4 := f0 + 4*m
		for u := 0; u < m; u++ {
			scratch0 := fout[f0]

			scratch1 := cMul(fout[f1], tw[u*fstride])
			scratch2 := cMul(fout[f2], tw[2*u*fstride])
			scratch3 := cMul(fout[f3], tw[3*u*fstride])
			scratch4 := cMul(fout[f4], tw[4*u*fstride])

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
				R: add32Ovflw(scratch0.R, add32Ovflw(sMul(scratch7.R, ya.R), sMul(scratch8.R, yb.R))),
				I: add32Ovflw(scratch0.I, add32Ovflw(sMul(scratch7.I, ya.R), sMul(scratch8.I, yb.R))),
			}

			scratch6 := FFTCpx{
				R: add32Ovflw(sMul(scratch10.I, ya.I), sMul(scratch9.I, yb.I)),
				I: neg32Ovflw(add32Ovflw(sMul(scratch10.R, ya.I), sMul(scratch9.R, yb.I))),
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
				R: add32Ovflw(scratch0.R, add32Ovflw(sMul(scratch7.R, yb.R), sMul(scratch8.R, ya.R))),
				I: add32Ovflw(scratch0.I, add32Ovflw(sMul(scratch7.I, yb.R), sMul(scratch8.I, ya.R))),
			}
			scratch12 := FFTCpx{
				R: sub32Ovflw(sMul(scratch9.I, ya.I), sMul(scratch10.I, yb.I)),
				I: sub32Ovflw(sMul(scratch10.R, yb.I), sMul(scratch9.R, ya.I)),
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
