//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import (
	"simd/archsimd"
	"unsafe"
)

// The x86 inner butterflies process four consecutive j values per block. The
// block loop runs outside the N repeats so each block's gathered twiddles stay
// in registers across them; the butterflies are independent, so the order is
// free.
// AVX supplies the shuffle operations used to separate and reinterleave the
// real and imaginary lanes; CPUs without AVX and stages off this grid use the
// scalar implementation. The arithmetic keeps the separate float32 multiply
// and add/subtract operations used by libopus's x86 path.
func kfBfly5InnerSIMD(fout []kissCpx, w []kissCpx, m, N, mm, fstride int) {
	if m <= 0 || N <= 0 {
		kfBfly5InnerScalar(fout, w, m, N, mm, fstride)
		return
	}
	if !archsimd.X86.AVX() || m < 4 || m&3 != 0 {
		kfBfly5InnerScalar(fout, w, m, N, mm, fstride)
		return
	}

	wBase := unsafe.Pointer(unsafe.SliceData(w))
	ya := *(*kissCpx)(unsafe.Add(wBase, fstride*m*8))
	yb := *(*kissCpx)(unsafe.Add(wBase, fstride*2*m*8))
	yar, yai := broadcastF32x4Arch(ya.r), broadcastF32x4Arch(ya.i)
	ybr, ybi := broadcastF32x4Arch(yb.r), broadcastF32x4Arch(yb.i)
	foutBase := unsafe.Pointer(unsafe.SliceData(fout))

	for j := 0; j < m; j += 4 {
		w1r, w1i := bflyGatherTwiddle4AMD64(wBase, j*fstride, fstride)
		w2r, w2i := bflyGatherTwiddle4AMD64(wBase, j*2*fstride, 2*fstride)
		w3r, w3i := bflyGatherTwiddle4AMD64(wBase, j*3*fstride, 3*fstride)
		w4r, w4i := bflyGatherTwiddle4AMD64(wBase, j*4*fstride, 4*fstride)
		for i := 0; i < N; i++ {
			p0 := unsafe.Add(foutBase, (i*mm+j)*8)
			p1 := unsafe.Add(p0, m*8)
			p2 := unsafe.Add(p1, m*8)
			p3 := unsafe.Add(p2, m*8)
			p4 := unsafe.Add(p3, m*8)
			s0r, s0i := bflyLoadCpx4AMD64(p0)
			b1r, b1i := bflyLoadCpx4AMD64(p1)
			b2r, b2i := bflyLoadCpx4AMD64(p2)
			b3r, b3i := bflyLoadCpx4AMD64(p3)
			b4r, b4i := bflyLoadCpx4AMD64(p4)

			s1r, s1i := bflyMulSource4AMD64(b1r, b1i, w1r, w1i)
			s2r, s2i := bflyMulSource4AMD64(b2r, b2i, w2r, w2i)
			s3r, s3i := bflyMulSource4AMD64(b3r, b3i, w3r, w3i)
			s4r, s4i := bflyMulSource4AMD64(b4r, b4i, w4r, w4i)
			s7r, s7i := s1r.Add(s4r), s1i.Add(s4i)
			s10r, s10i := s1r.Sub(s4r), s1i.Sub(s4i)
			s8r, s8i := s2r.Add(s3r), s2i.Add(s3i)
			s9r, s9i := s2r.Sub(s3r), s2i.Sub(s3i)

			bflyStoreCpx4AMD64(p0, s0r.Add(s7r.Add(s8r)), s0i.Add(s7i.Add(s8i)))
			s5r := s0r.Add(bflyMulAddSource4AMD64(s7r, yar, s8r, ybr))
			s5i := s0i.Add(bflyMulAddSource4AMD64(s7i, yar, s8i, ybr))
			s6r := bflyMulAddSource4AMD64(s10i, yai, s9i, ybi)
			s6i := negF32x4AVX(bflyMulAddSource4AMD64(s10r, yai, s9r, ybi))
			bflyStoreCpx4AMD64(p1, s5r.Sub(s6r), s5i.Sub(s6i))
			bflyStoreCpx4AMD64(p4, s5r.Add(s6r), s5i.Add(s6i))

			s11r := s0r.Add(bflyMulAddSource4AMD64(s7r, ybr, s8r, yar))
			s11i := s0i.Add(bflyMulAddSource4AMD64(s7i, ybr, s8i, yar))
			s12r := bflyMulSubSource4AMD64(s9i, yai, s10i, ybi)
			s12i := bflyMulSubSource4AMD64(s10r, ybi, s9r, yai)
			bflyStoreCpx4AMD64(p2, s11r.Add(s12r), s11i.Add(s12i))
			bflyStoreCpx4AMD64(p3, s11r.Sub(s12r), s11i.Sub(s12i))
		}
	}
}

func kfBfly4InnerSIMD(fout []kissCpx, w []kissCpx, m, N, mm, fstride int) {
	if m <= 0 || N <= 0 {
		kfBfly4InnerScalar(fout, w, m, N, mm, fstride)
		return
	}
	if !archsimd.X86.AVX() || m < 4 || m&3 != 0 {
		kfBfly4InnerScalar(fout, w, m, N, mm, fstride)
		return
	}

	wBase := unsafe.Pointer(unsafe.SliceData(w))
	foutBase := unsafe.Pointer(unsafe.SliceData(fout))
	for j := 0; j < m; j += 4 {
		w1r, w1i := bflyGatherTwiddle4AMD64(wBase, j*fstride, fstride)
		w2r, w2i := bflyGatherTwiddle4AMD64(wBase, j*2*fstride, 2*fstride)
		w3r, w3i := bflyGatherTwiddle4AMD64(wBase, j*3*fstride, 3*fstride)
		for i := 0; i < N; i++ {
			p0 := unsafe.Add(foutBase, (i*mm+j)*8)
			p1 := unsafe.Add(p0, m*8)
			p2 := unsafe.Add(p1, m*8)
			p3 := unsafe.Add(p2, m*8)
			f0r, f0i := bflyLoadCpx4AMD64(p0)
			b1r, b1i := bflyLoadCpx4AMD64(p1)
			b2r, b2i := bflyLoadCpx4AMD64(p2)
			b3r, b3i := bflyLoadCpx4AMD64(p3)

			s0ur, s0ui := bflyMulSource4UnfusedAMD64(b1r, b1i, w1r, w1i)
			s1r, s1i := bflyMulSource4UnfusedAMD64(b2r, b2i, w2r, w2i)
			s2ur, s2ui := bflyMulSource4UnfusedAMD64(b3r, b3i, w3r, w3i)
			s0fr, s0fi := bflyMulSource4FusedAMD64(b1r, b1i, w1r, w1i)
			s2fr, s2fi := bflyMulSource4FusedAMD64(b3r, b3i, w3r, w3i)
			s5r, s5i := f0r.Sub(s1r), f0i.Sub(s1i)
			f0r, f0i = f0r.Add(s1r), f0i.Add(s1i)
			s3r, s3i := s0ur.Add(s2ur), s0ui.Add(s2ui)
			s4r, s4i := s0fr.Sub(s2fr), s0fi.Sub(s2fi)
			out2r, out2i := f0r.Sub(s3r), f0i.Sub(s3i)
			out0r, out0i := f0r.Add(s3r), f0i.Add(s3i)
			out1r, out1i := s5r.Add(s4i), s5i.Sub(s4r)
			out3r, out3i := s5r.Sub(s4i), s5i.Add(s4r)
			bflyStoreCpx4AMD64(p2, out2r, out2i)
			bflyStoreCpx4AMD64(p0, out0r, out0i)
			bflyStoreCpx4AMD64(p1, out1r, out1i)
			bflyStoreCpx4AMD64(p3, out3r, out3i)
		}
	}
}

func kfBfly3InnerSIMD(fout []kissCpx, w []kissCpx, m, N, mm, fstride int) {
	if m <= 0 || N <= 0 || !archsimd.X86.AVX() || m&3 != 0 {
		kfBfly3InnerScalar(fout, w, m, N, mm, fstride)
		return
	}
	_ = fout[(N-1)*mm+3*m-1]
	_ = w[2*(m-1)*fstride]
	_ = w[fstride*m]

	wBase := unsafe.Pointer(unsafe.SliceData(w))
	epi3i := broadcastF32x4Arch(w[fstride*m].i)
	half := broadcastF32x4Arch(0.5)
	foutBase := unsafe.Pointer(unsafe.SliceData(fout))
	for j := 0; j < m; j += 4 {
		w1r, w1i := bflyGatherTwiddle4AMD64(wBase, j*fstride, fstride)
		w2r, w2i := bflyGatherTwiddle4AMD64(wBase, j*2*fstride, 2*fstride)
		for i := 0; i < N; i++ {
			p0 := unsafe.Add(foutBase, (i*mm+j)*8)
			p1 := unsafe.Add(p0, m*8)
			p2 := unsafe.Add(p1, m*8)
			a0r, a0i := bflyLoadCpx4AMD64(p0)
			b1r, b1i := bflyLoadCpx4AMD64(p1)
			b2r, b2i := bflyLoadCpx4AMD64(p2)

			s1r, s1i := bflyMulSource4AMD64(b1r, b1i, w1r, w1i)
			s2r, s2i := bflyMulSource4AMD64(b2r, b2i, w2r, w2i)
			s3r, s3i := s1r.Add(s2r), s1i.Add(s2i)
			s0r, s0i := s1r.Sub(s2r), s1i.Sub(s2i)
			f1r, f1i := bflyHalfSub4AMD64(a0r, s3r, half), bflyHalfSub4AMD64(a0i, s3i, half)
			s0r, s0i = s0r.Mul(epi3i), s0i.Mul(epi3i)
			bflyStoreCpx4AMD64(p0, a0r.Add(s3r), a0i.Add(s3i))
			bflyStoreCpx4AMD64(p2, f1r.Add(s0i), f1i.Sub(s0r))
			bflyStoreCpx4AMD64(p1, f1r.Sub(s0i), f1i.Add(s0r))
		}
	}
}

// kfBfly4M1CoreSIMD runs one radix-4 m==1 butterfly per iteration on the two
// interleaved vectors {a0,a1} and {a2,a3}:
//
//	{f0,s1} = {a0+a2, a1+a3}, {s0,t} = {a0-a2, a1-a3}
//	{f0,f1} = {f0+s1.., s0 + (t.i, -t.r)}, {f2,f3} = {f0-s1.., s0 - (t.i, -t.r)}
//
// Lane 3 needs the opposite operation from lanes 0-2 (f1.i = s0.i - t.r,
// f3.i = s0.i + t.r), so the sum and difference are blended rather than
// negating t.r; every lane runs the scalar reference's exact operation.
func kfBfly4M1CoreSIMD(fout []kissCpx, n int) {
	if n <= 0 || !archsimd.X86.AVX() {
		kfBfly4M1CoreScalar(fout, n)
		return
	}
	_ = fout[4*n-1]
	lane3 := archsimd.LoadInt32x4Array(&[4]int32{0, 0, 0, -1}).ToMask()
	p := unsafe.Pointer(unsafe.SliceData(fout))
	for range n {
		x := loadF32x4(p)
		y := loadF32x4(unsafe.Add(p, 16))
		sum := x.Add(y)
		diff := x.Sub(y)
		a := sum.ConcatPermuteScalars(0, 1, 4, 5, diff)
		b := sum.ConcatPermuteScalars(2, 3, 7, 6, diff)
		plus := a.Add(b)
		minus := a.Sub(b)
		storeF32x4(p, minus.IfElse(lane3, plus))
		storeF32x4(unsafe.Add(p, 16), plus.IfElse(lane3, minus))
		p = unsafe.Add(p, 32)
	}
}

func bflyLoadCpx4AMD64(p unsafe.Pointer) (re, im archsimd.Float32x4) {
	lo := loadF32x4(p)
	hi := loadF32x4(unsafe.Add(p, 16))
	return lo.ConcatPermuteScalars(0, 2, 4, 6, hi), lo.ConcatPermuteScalars(1, 3, 5, 7, hi)
}

func bflyStoreCpx4AMD64(p unsafe.Pointer, re, im archsimd.Float32x4) {
	reBits, imBits := re.ToBits(), im.ToBits()
	storeF32x4(p, reBits.InterleaveLo(imBits).BitsToFloat32())
	storeF32x4(unsafe.Add(p, 16), reBits.InterleaveHi(imBits).BitsToFloat32())
}

func bflyGatherTwiddle4AMD64(wBase unsafe.Pointer, start, stride int) (re, im archsimd.Float32x4) {
	t0 := *(*kissCpx)(unsafe.Add(wBase, start*8))
	t1 := *(*kissCpx)(unsafe.Add(wBase, (start+stride)*8))
	t2 := *(*kissCpx)(unsafe.Add(wBase, (start+2*stride)*8))
	t3 := *(*kissCpx)(unsafe.Add(wBase, (start+3*stride)*8))
	re = broadcastF32x4Arch(t0.r).SetElem(1, t1.r).SetElem(2, t2.r).SetElem(3, t3.r)
	im = broadcastF32x4Arch(t0.i).SetElem(1, t1.i).SetElem(2, t2.i).SetElem(3, t3.i)
	return re, im
}
