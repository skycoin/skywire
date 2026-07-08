package celt

// These helpers keep the C expression order used by libopus' scalar Kiss FFT
// path for the 120-point arm64 MDCT FFT. The noinline boundary avoids folding
// them into the FMAlike assembly path used by the larger arm64 FFTs.

//go:noinline
func kissMulAddCOrder(a, b, c, d float32) float32 {
	return a*b + c*d
}

//go:noinline
func kissMulSubCOrder(a, b, c, d float32) float32 {
	return a*b - c*d
}

func kfBfly5InnerCOrder(fout []kissCpx, w []kissCpx, m, N, mm, fstride int) {
	ya := w[fstride*m]
	yb := w[fstride*2*m]
	yar, yai := ya.r, ya.i
	ybr, ybi := yb.r, yb.i
	fstride2 := fstride * 2
	fstride3 := fstride * 3
	fstride4 := fstride * 4
	for i := range N {
		base := i * mm
		idx0, idx1, idx2, idx3, idx4 := base, base+m, base+2*m, base+3*m, base+4*m
		tw1, tw2, tw3, tw4 := 0, 0, 0, 0
		for range m {
			s0 := fout[idx0]
			b1 := fout[idx1]
			b2 := fout[idx2]
			b3 := fout[idx3]
			b4 := fout[idx4]
			w1 := w[tw1]
			w2 := w[tw2]
			w3 := w[tw3]
			w4 := w[tw4]

			s1r := kissMulSubCOrder(b1.r, w1.r, b1.i, w1.i)
			s1i := kissMulAddCOrder(b1.r, w1.i, b1.i, w1.r)
			s2r := kissMulSubCOrder(b2.r, w2.r, b2.i, w2.i)
			s2i := kissMulAddCOrder(b2.r, w2.i, b2.i, w2.r)
			s3r := kissMulSubCOrder(b3.r, w3.r, b3.i, w3.i)
			s3i := kissMulAddCOrder(b3.r, w3.i, b3.i, w3.r)
			s4r := kissMulSubCOrder(b4.r, w4.r, b4.i, w4.i)
			s4i := kissMulAddCOrder(b4.r, w4.i, b4.i, w4.r)

			s7r, s7i := s1r+s4r, s1i+s4i
			s10r, s10i := s1r-s4r, s1i-s4i
			s8r, s8i := s2r+s3r, s2i+s3i
			s9r, s9i := s2r-s3r, s2i-s3i

			fout[idx0].r = kissAdd(s0.r, kissAdd(s7r, s8r))
			fout[idx0].i = kissAdd(s0.i, kissAdd(s7i, s8i))

			s5r := kissAdd(s0.r, kissMulAddCOrder(s7r, yar, s8r, ybr))
			s5i := kissAdd(s0.i, kissMulAddCOrder(s7i, yar, s8i, ybr))
			s6r := kissMulAddCOrder(s10i, yai, s9i, ybi)
			s6i := -kissMulAddCOrder(s10r, yai, s9r, ybi)
			fout[idx1].r, fout[idx1].i = kissSub(s5r, s6r), kissSub(s5i, s6i)
			fout[idx4].r, fout[idx4].i = kissAdd(s5r, s6r), kissAdd(s5i, s6i)

			s11r := kissAdd(s0.r, kissMulAddCOrder(s7r, ybr, s8r, yar))
			s11i := kissAdd(s0.i, kissMulAddCOrder(s7i, ybr, s8i, yar))
			s12r := kissMulSubCOrder(s9i, yai, s10i, ybi)
			s12i := kissMulSubCOrder(s10r, ybi, s9r, yai)
			fout[idx2].r, fout[idx2].i = kissAdd(s11r, s12r), kissAdd(s11i, s12i)
			fout[idx3].r, fout[idx3].i = kissSub(s11r, s12r), kissSub(s11i, s12i)

			idx0++
			idx1++
			idx2++
			idx3++
			idx4++
			tw1 += fstride
			tw2 += fstride2
			tw3 += fstride3
			tw4 += fstride4
		}
	}
}

func kfBfly3InnerCOrderGeneric(fout []kissCpx, w []kissCpx, m, N, mm, fstride int) {
	m2 := 2 * m
	epi3i := w[fstride*m].i
	fstride2 := fstride * 2
	for i := range N {
		base := i * mm
		tw1, tw2 := 0, 0
		for j := range m {
			idx0 := base + j
			idx1 := idx0 + m
			idx2 := idx0 + m2
			a0r, a0i := fout[idx0].r, fout[idx0].i
			b1 := fout[idx1]
			b2 := fout[idx2]
			w1 := w[tw1]
			w2 := w[tw2]
			s1r := kissMulSubCOrder(b1.r, w1.r, b1.i, w1.i)
			s1i := kissMulAddCOrder(b1.r, w1.i, b1.i, w1.r)
			s2r := kissMulSubCOrder(b2.r, w2.r, b2.i, w2.i)
			s2i := kissMulAddCOrder(b2.r, w2.i, b2.i, w2.r)
			s3r := s1r + s2r
			s3i := s1i + s2i
			s0r := s1r - s2r
			s0i := s1i - s2i
			tw1 += fstride
			tw2 += fstride2
			fout[idx1].r = kissHalfSub(a0r, s3r)
			fout[idx1].i = kissHalfSub(a0i, s3i)
			s0r = kissScaleMul(s0r, epi3i)
			s0i = kissScaleMul(s0i, epi3i)
			fout[idx0].r = a0r + s3r
			fout[idx0].i = a0i + s3i
			fout[idx2].r = fout[idx1].r + s0i
			fout[idx2].i = fout[idx1].i - s0r
			fout[idx1].r = fout[idx1].r - s0i
			fout[idx1].i = fout[idx1].i + s0r
		}
	}
}
