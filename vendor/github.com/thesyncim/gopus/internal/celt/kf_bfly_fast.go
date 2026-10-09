package celt

import "math"

// kissFFTFastLimitBits is 2^100 as float32 bits. When every input component of
// an FFT is below it in magnitude, no stage can overflow: each radix-3/4/5
// stage grows the component magnitude by less than 20x and twiddles are at most
// 1, so all products and sums stay finite and no twiddle product is NaN.
const kissFFTFastLimitBits = 0x71800000

// kissFFTInputBounded reports whether every component of x is finite and below
// 2^100 in magnitude. NaN and infinity fail the test.
func kissFFTInputBounded(x []kissCpx) bool {
	// Adding 2^31-limit to the magnitude bits carries into bit 31 exactly when
	// the magnitude reaches the limit.
	const bias = 0x80000000 - kissFFTFastLimitBits
	var accR, accI uint32
	for _, v := range x {
		accR |= (math.Float32bits(v.r) & 0x7fffffff) + bias
		accI |= (math.Float32bits(v.i) & 0x7fffffff) + bias
	}
	return (accR|accI)>>31 == 0
}

// kissStageTwiddles holds one FFT stage's twiddles packed per butterfly: for
// butterfly u of a radix-p stage with stride fstride, entry u holds
// w[k*u*fstride] for k = 1..p-1. Only the slice matching the stage radix is
// set, and only for stages with m > 1. Radix-4 entries carry an unused fourth
// slot so every entry size is a power of two and indexes with a shift.
type kissStageTwiddles struct {
	tw3 [][2]kissCpx
	tw4 [][4]kissCpx
	tw5 [][4]kissCpx
}

// newKissStageTwiddles packs the per-stage twiddles fftImpl passes to the Fast
// butterflies.
func newKissStageTwiddles(factors []int, fstride []int, shift int, w []kissCpx) []kissStageTwiddles {
	shift = max(shift, 0)
	stages := make([]kissStageTwiddles, len(factors)/2)
	for i := range stages {
		p, m := factors[2*i], factors[2*i+1]
		if m <= 1 || i >= len(fstride) {
			continue
		}
		fs := fstride[i] << shift
		switch p {
		case 3:
			stages[i].tw3 = packKissTwiddles3(w, m, fs)
		case 4:
			stages[i].tw4 = packKissTwiddles4(w, m, fs)
		case 5:
			stages[i].tw5 = packKissTwiddles5(w, m, fs)
		}
	}
	return stages
}

func packKissTwiddles3(w []kissCpx, m, fstride int) [][2]kissCpx {
	tw := make([][2]kissCpx, m)
	for u := range tw {
		tw[u] = [2]kissCpx{w[u*fstride], w[2*u*fstride]}
	}
	return tw
}

func packKissTwiddles4(w []kissCpx, m, fstride int) [][4]kissCpx {
	tw := make([][4]kissCpx, m)
	for u := range tw {
		tw[u] = [4]kissCpx{w[u*fstride], w[2*u*fstride], w[3*u*fstride]}
	}
	return tw
}

func packKissTwiddles5(w []kissCpx, m, fstride int) [][4]kissCpx {
	tw := make([][4]kissCpx, m)
	for u := range tw {
		tw[u] = [4]kissCpx{w[u*fstride], w[2*u*fstride], w[3*u*fstride], w[4*u*fstride]}
	}
	return tw
}

// kfBfly5InnerFast is kfBfly5InnerScalar for bounded FFT input, with packed
// stage twiddles and ya = w[fstride*m], yb = w[2*fstride*m]. Each outer
// iteration slices a checked radix-by-m row into m-element columns.
func kfBfly5InnerFast(fout []kissCpx, tw [][4]kissCpx, ya, yb kissCpx, N, mm int) {
	m := len(tw)
	if m == 0 {
		return
	}
	_ = tw[m-1]
	y := [4]float32{ya.r, ya.i, yb.r, yb.i}
	for i := 0; i < N; i++ {
		span := fout[i*mm : i*mm+5*m]
		f0 := span[:m]
		f1 := span[m : 2*m]
		f2 := span[2*m : 3*m]
		f3 := span[3*m : 4*m]
		f4 := span[4*m : 5*m]
		_ = f0[m-1]
		_ = f1[m-1]
		_ = f2[m-1]
		_ = f3[m-1]
		_ = f4[m-1]
		for u := range f0 {
			t := &tw[u]
			f0u := &f0[u]
			f1u := &f1[u]
			f2u := &f2[u]
			f3u := &f3[u]
			f4u := &f4[u]
			s1r := kissMulSubFast(f1u.r, t[0].r, f1u.i, t[0].i)
			s1i := kissMulAddFast(f1u.r, t[0].i, f1u.i, t[0].r)
			s4r := kissMulSubFast(f4u.r, t[3].r, f4u.i, t[3].i)
			s4i := kissMulAddFast(f4u.r, t[3].i, f4u.i, t[3].r)
			s7r, s7i := s1r+s4r, s1i+s4i
			s10r, s10i := s1r-s4r, s1i-s4i
			s2r := kissMulSubFast(f2u.r, t[1].r, f2u.i, t[1].i)
			s2i := kissMulAddFast(f2u.r, t[1].i, f2u.i, t[1].r)
			s3r := kissMulSubFast(f3u.r, t[2].r, f3u.i, t[2].i)
			s3i := kissMulAddFast(f3u.r, t[2].i, f3u.i, t[2].r)
			s8r, s8i := s2r+s3r, s2i+s3i
			s9r, s9i := s2r-s3r, s2i-s3i

			// Keep the scalar kernel's real-output order before reading s0i.
			s0r := f0u.r
			f0u.r = s0r + (s7r + s8r)
			s5r := s0r + kissMulAddFast(s7r, y[0], s8r, y[2])
			s6r := kissMulAddFast(s10i, y[1], s9i, y[3])
			f1u.r = s5r - s6r
			f4u.r = s5r + s6r
			s11r := s0r + kissMulAddFast(s7r, y[2], s8r, y[0])
			s12r := kissMulSubFast(s9i, y[1], s10i, y[3])
			f2u.r = s11r + s12r
			f3u.r = s11r - s12r

			s0i := f0u.i
			f0u.i = s0i + (s7i + s8i)
			s5i := s0i + kissMulAddFast(s7i, y[0], s8i, y[2])
			s6i := kissMulAddFast(s10r, y[1], s9r, y[3])
			f1u.i = s5i + s6i
			f4u.i = s5i - s6i
			s11i := s0i + kissMulAddFast(s7i, y[2], s8i, y[0])
			s12i := kissMulSubFast(s10r, y[3], s9r, y[1])
			f2u.i = s11i + s12i
			f3u.i = s11i - s12i
		}
	}
}

// kfBfly3InnerFast is kfBfly3InnerScalar for bounded FFT input with packed
// stage twiddles and epi3i = w[fstride*m].i. Each outer iteration slices a
// checked radix-by-m row into m-element columns.
func kfBfly3InnerFast(fout []kissCpx, tw [][2]kissCpx, epi3i float32, N, mm int) {
	m := len(tw)
	if m == 0 {
		return
	}
	_ = tw[m-1]
	for i := 0; i < N; i++ {
		span := fout[i*mm : i*mm+3*m]
		f0 := span[:m]
		f1 := span[m : 2*m]
		f2 := span[2*m : 3*m]
		_ = f0[m-1]
		_ = f1[m-1]
		_ = f2[m-1]
		for u := range f0 {
			t := &tw[u]
			f0u := &f0[u]
			f1u := &f1[u]
			f2u := &f2[u]
			s1r := kissMulSubFast(f1u.r, t[0].r, f1u.i, t[0].i)
			s1i := kissMulAddFast(f1u.r, t[0].i, f1u.i, t[0].r)
			s2r := kissMulSubFast(f2u.r, t[1].r, f2u.i, t[1].i)
			s2i := kissMulAddFast(f2u.r, t[1].i, f2u.i, t[1].r)

			s3r := s1r + s2r
			s3i := s1i + s2i
			s0r := s1r - s2r
			s0i := s1i - s2i

			a0r, a0i := f0u.r, f0u.i
			h1r := kissHalfSub(a0r, s3r)
			h1i := kissHalfSub(a0i, s3i)
			f0u.r = a0r + s3r
			f0u.i = a0i + s3i
			o1r, o1i, o2r, o2i := kissRadix3ScaledOutputs(h1r, h1i, s0r, s0i, epi3i)
			f1u.r = o1r
			f1u.i = o1i
			f2u.r = o2r
			f2u.i = o2i
		}
	}
}

// kfBfly4InnerFast is kfBfly4InnerScalar for bounded FFT input with packed
// stage twiddles. Each outer iteration slices a checked radix-by-m row into
// m-element columns.
func kfBfly4InnerFast(fout []kissCpx, tw [][4]kissCpx, N, mm int) {
	m := len(tw)
	if m == 0 {
		return
	}
	_ = tw[m-1]
	for i := 0; i < N; i++ {
		span := fout[i*mm : i*mm+4*m]
		f0 := span[:m]
		f1 := span[m : 2*m]
		f2 := span[2*m : 3*m]
		f3 := span[3*m : 4*m]
		_ = f0[m-1]
		_ = f1[m-1]
		_ = f2[m-1]
		_ = f3[m-1]
		for u := range f0 {
			t := &tw[u]
			f0u := &f0[u]
			f1u := &f1[u]
			f2u := &f2[u]
			f3u := &f3[u]
			s0r := kissMulSubFast(f1u.r, t[0].r, f1u.i, t[0].i)
			s0i := kissMulAddFast(f1u.r, t[0].i, f1u.i, t[0].r)
			s1r := kissMulSubFast(f2u.r, t[1].r, f2u.i, t[1].i)
			s1i := kissMulAddFast(f2u.r, t[1].i, f2u.i, t[1].r)
			s2r := kissMulSubFast(f3u.r, t[2].r, f3u.i, t[2].i)
			s2i := kissMulAddFast(f3u.r, t[2].i, f3u.i, t[2].r)

			a0r, a0i := f0u.r, f0u.i
			s5r := a0r - s1r
			s5i := a0i - s1i
			f0r := a0r + s1r
			f0i := a0i + s1i
			s3r := s0r + s2r
			s3i := s0i + s2i
			s4r := s0r - s2r
			s4i := s0i - s2i
			f2u.r = f0r - s3r
			f2u.i = f0i - s3i
			f0r += s3r
			f0i += s3i
			f0u.r = f0r
			f0u.i = f0i
			f1u.r = s5r + s4i
			f1u.i = s5i - s4r
			f3u.r = s5r - s4i
			f3u.i = s5i + s4r
		}
	}
}
