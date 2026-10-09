//go:build arm64 && goexperiment.simd && !nosimd && !purego

package celt

import (
	"simd/archsimd"
	"unsafe"
)

// The SIMD inner kernels process four consecutive j values. Their scalar
// counterparts retain the short and off-grid stages where a vector block
// cannot run.
func kfBfly4M1Core(fout []kissCpx, n int) {
	kfBfly4M1CoreSIMD(fout, n)
}

func kfBfly5Inner(fout []kissCpx, w []kissCpx, m, N, mm, fstride int) {
	kfBfly5InnerSIMD(fout, w, m, N, mm, fstride)
}

func kfBfly3Inner(fout []kissCpx, w []kissCpx, m, N, mm, fstride int) {
	kfBfly3InnerSIMD(fout, w, m, N, mm, fstride)
}

func kfBfly4Inner(fout []kissCpx, w []kissCpx, m, N, mm, fstride int) {
	kfBfly4InnerSIMD(fout, w, m, N, mm, fstride)
}

func kfBfly4M1CoreSIMD(fout []kissCpx, n int) {
	if n <= 0 {
		return
	}

	base := unsafe.Pointer(unsafe.SliceData(fout))
	blocks := n / 2
	for block := 0; block < blocks; block++ {
		p := unsafe.Add(base, block*64)
		g0a01 := loadF32x4(p)
		g1a01 := loadF32x4(unsafe.Add(p, 32))
		g0a23 := loadF32x4(unsafe.Add(p, 16))
		g1a23 := loadF32x4(unsafe.Add(p, 48))

		a0 := g0a01.ToBits().ReshapeToUint64s().InterleaveLo(g1a01.ToBits().ReshapeToUint64s())
		a1 := g0a01.ToBits().ReshapeToUint64s().InterleaveHi(g1a01.ToBits().ReshapeToUint64s())
		a2 := g0a23.ToBits().ReshapeToUint64s().InterleaveLo(g1a23.ToBits().ReshapeToUint64s())
		a3 := g0a23.ToBits().ReshapeToUint64s().InterleaveHi(g1a23.ToBits().ReshapeToUint64s())

		a0f := a0.ReshapeToUint32s()
		a1f := a1.ReshapeToUint32s()
		a2f := a2.ReshapeToUint32s()
		a3f := a3.ReshapeToUint32s()
		s0 := a0f.BitsToFloat32().Sub(a2f.BitsToFloat32())
		f0 := a0f.BitsToFloat32().Add(a2f.BitsToFloat32())
		s1 := a1f.BitsToFloat32().Add(a3f.BitsToFloat32())
		f2 := f0.Sub(s1)
		f0 = f0.Add(s1)
		d1 := a1f.BitsToFloat32().Sub(a3f.BitsToFloat32())

		s0r, s0i := s0.ToBits().ConcatEven(s0.ToBits()).BitsToFloat32(), s0.ToBits().ConcatOdd(s0.ToBits()).BitsToFloat32()
		d1r, d1i := d1.ToBits().ConcatEven(d1.ToBits()).BitsToFloat32(), d1.ToBits().ConcatOdd(d1.ToBits()).BitsToFloat32()
		f1r, f1i := s0r.Add(d1i), s0i.Sub(d1r)
		f3r, f3i := s0r.Sub(d1i), s0i.Add(d1r)

		v1 := f1r.ToBits().InterleaveLo(f1i.ToBits()).BitsToFloat32()
		v3 := f3r.ToBits().InterleaveLo(f3i.ToBits()).BitsToFloat32()
		v01 := f0.ToBits().ReshapeToUint64s().InterleaveLo(v1.ToBits().ReshapeToUint64s()).ReshapeToUint32s().BitsToFloat32()
		v23 := f2.ToBits().ReshapeToUint64s().InterleaveLo(v3.ToBits().ReshapeToUint64s()).ReshapeToUint32s().BitsToFloat32()
		v45 := f0.ToBits().ReshapeToUint64s().InterleaveHi(v1.ToBits().ReshapeToUint64s()).ReshapeToUint32s().BitsToFloat32()
		v67 := f2.ToBits().ReshapeToUint64s().InterleaveHi(v3.ToBits().ReshapeToUint64s()).ReshapeToUint32s().BitsToFloat32()
		storeF32x4(p, v01)
		storeF32x4(unsafe.Add(p, 16), v23)
		storeF32x4(unsafe.Add(p, 32), v45)
		storeF32x4(unsafe.Add(p, 48), v67)
	}

	if n&1 != 0 {
		kfBfly4M1CoreScalar(fout[blocks*8:], 1)
	}
}

func kfBfly5InnerSIMD(fout []kissCpx, w []kissCpx, m, N, mm, fstride int) {
	if m <= 0 || N <= 0 {
		return
	}
	if m < 4 || m&3 != 0 {
		kfBfly5InnerScalar(fout, w, m, N, mm, fstride)
		return
	}

	wBase := unsafe.Pointer(unsafe.SliceData(w))
	ya := *(*kissCpx)(unsafe.Add(wBase, fstride*m*8))
	yb := *(*kissCpx)(unsafe.Add(wBase, fstride*2*m*8))
	yar, yai := archsimd.BroadcastFloat32x4(ya.r), archsimd.BroadcastFloat32x4(ya.i)
	ybr, ybi := archsimd.BroadcastFloat32x4(yb.r), archsimd.BroadcastFloat32x4(yb.i)
	foutBase := unsafe.Pointer(unsafe.SliceData(fout))

	for i := 0; i < N; i++ {
		base := i * mm
		for j := 0; j < m; j += 4 {
			idx0 := base + j
			p0 := unsafe.Add(foutBase, idx0*8)
			p1 := unsafe.Add(p0, m*8)
			p2 := unsafe.Add(p1, m*8)
			p3 := unsafe.Add(p2, m*8)
			p4 := unsafe.Add(p3, m*8)
			s0r, s0i := bflyLoadCpx4(p0)
			b1r, b1i := bflyLoadCpx4(p1)
			b2r, b2i := bflyLoadCpx4(p2)
			b3r, b3i := bflyLoadCpx4(p3)
			b4r, b4i := bflyLoadCpx4(p4)
			w1r, w1i := bflyGatherTwiddle4(wBase, j*fstride, fstride)
			w2r, w2i := bflyGatherTwiddle4(wBase, j*2*fstride, 2*fstride)
			w3r, w3i := bflyGatherTwiddle4(wBase, j*3*fstride, 3*fstride)
			w4r, w4i := bflyGatherTwiddle4(wBase, j*4*fstride, 4*fstride)

			s1r, s1i := bflyMulSource4(b1r, b1i, w1r, w1i)
			s2r, s2i := bflyMulSource4(b2r, b2i, w2r, w2i)
			s3r, s3i := bflyMulSource4(b3r, b3i, w3r, w3i)
			s4r, s4i := bflyMulSource4(b4r, b4i, w4r, w4i)
			s7r, s7i := s1r.Add(s4r), s1i.Add(s4i)
			s10r, s10i := s1r.Sub(s4r), s1i.Sub(s4i)
			s8r, s8i := s2r.Add(s3r), s2i.Add(s3i)
			s9r, s9i := s2r.Sub(s3r), s2i.Sub(s3i)

			out0r, out0i := s0r.Add(s7r.Add(s8r)), s0i.Add(s7i.Add(s8i))
			s5r := s0r.Add(s7r.MulAdd(yar, s8r.Mul(ybr)))
			s5i := s0i.Add(s7i.MulAdd(yar, s8i.Mul(ybr)))
			s6r := s10i.MulAdd(yai, s9i.Mul(ybi))
			s6i := s10r.MulAdd(yai, s9r.Mul(ybi)).Neg()
			bflyStoreCpx4(p0, out0r, out0i)
			bflyStoreCpx4(p1, s5r.Sub(s6r), s5i.Sub(s6i))
			bflyStoreCpx4(p4, s5r.Add(s6r), s5i.Add(s6i))

			s11r := s0r.Add(s7r.MulAdd(ybr, s8r.Mul(yar)))
			s11i := s0i.Add(s7i.MulAdd(ybr, s8i.Mul(yar)))
			s12r := s9i.MulAdd(yai, s10i.Mul(ybi).Neg())
			s12i := s10r.MulAdd(ybi, s9r.Mul(yai).Neg())
			bflyStoreCpx4(p2, s11r.Add(s12r), s11i.Add(s12i))
			bflyStoreCpx4(p3, s11r.Sub(s12r), s11i.Sub(s12i))
		}
	}
}

func kfBfly3InnerSIMD(fout []kissCpx, w []kissCpx, m, N, mm, fstride int) {
	if m <= 0 || N <= 0 {
		return
	}
	if m < 4 || m&3 != 0 {
		kfBfly3InnerScalar(fout, w, m, N, mm, fstride)
		return
	}

	wBase := unsafe.Pointer(unsafe.SliceData(w))
	epi3i := archsimd.BroadcastFloat32x4((*kissCpx)(unsafe.Add(wBase, fstride*m*8)).i)
	foutBase := unsafe.Pointer(unsafe.SliceData(fout))
	for i := 0; i < N; i++ {
		base := i * mm
		for j := 0; j < m; j += 4 {
			idx0 := base + j
			p0 := unsafe.Add(foutBase, idx0*8)
			p1 := unsafe.Add(p0, m*8)
			p2 := unsafe.Add(p1, m*8)
			a0r, a0i := bflyLoadCpx4(p0)
			b1r, b1i := bflyLoadCpx4(p1)
			b2r, b2i := bflyLoadCpx4(p2)
			w1r, w1i := bflyGatherTwiddle4(wBase, j*fstride, fstride)
			w2r, w2i := bflyGatherTwiddle4(wBase, j*2*fstride, 2*fstride)
			s1r, s1i := bflyMulSource4(b1r, b1i, w1r, w1i)
			s2r, s2i := bflyMulSource4(b2r, b2i, w2r, w2i)
			s3r, s3i := s1r.Add(s2r), s1i.Add(s2i)
			s0r, s0i := s1r.Sub(s2r), s1i.Sub(s2i)
			h3r, h3i := s3r.Mul(archsimd.BroadcastFloat32x4(0.5)), s3i.Mul(archsimd.BroadcastFloat32x4(0.5))
			f1r, f1i := a0r.Sub(h3r), a0i.Sub(h3i)
			s0r, s0i = s0r.Mul(epi3i), s0i.Mul(epi3i)
			bflyStoreCpx4(p0, a0r.Add(s3r), a0i.Add(s3i))
			bflyStoreCpx4(p2, f1r.Add(s0i), f1i.Sub(s0r))
			bflyStoreCpx4(p1, f1r.Sub(s0i), f1i.Add(s0r))
		}
	}
}

func kfBfly4InnerSIMD(fout []kissCpx, w []kissCpx, m, N, mm, fstride int) {
	if m <= 0 || N <= 0 {
		return
	}
	if m < 4 || m&3 != 0 {
		kfBfly4InnerScalar(fout, w, m, N, mm, fstride)
		return
	}

	wBase := unsafe.Pointer(unsafe.SliceData(w))
	foutBase := unsafe.Pointer(unsafe.SliceData(fout))
	for i := 0; i < N; i++ {
		base := i * mm
		for j := 0; j < m; j += 4 {
			idx0 := base + j
			p0 := unsafe.Add(foutBase, idx0*8)
			p1 := unsafe.Add(p0, m*8)
			p2 := unsafe.Add(p1, m*8)
			p3 := unsafe.Add(p2, m*8)
			f0r, f0i := bflyLoadCpx4(p0)
			b1r, b1i := bflyLoadCpx4(p1)
			b2r, b2i := bflyLoadCpx4(p2)
			b3r, b3i := bflyLoadCpx4(p3)
			w1r, w1i := bflyGatherTwiddle4(wBase, j*fstride, fstride)
			w2r, w2i := bflyGatherTwiddle4(wBase, j*2*fstride, 2*fstride)
			w3r, w3i := bflyGatherTwiddle4(wBase, j*3*fstride, 3*fstride)
			s0r, s0i := bflyMulSource4(b1r, b1i, w1r, w1i)
			s1r, s1i := bflyMulSource4(b2r, b2i, w2r, w2i)
			s2r, s2i := bflyMulSource4(b3r, b3i, w3r, w3i)
			s5r, s5i := f0r.Sub(s1r), f0i.Sub(s1i)
			f0r, f0i = f0r.Add(s1r), f0i.Add(s1i)
			s3r, s3i := s0r.Add(s2r), s0i.Add(s2i)
			s4r, s4i := s0r.Sub(s2r), s0i.Sub(s2i)
			out2r, out2i := f0r.Sub(s3r), f0i.Sub(s3i)
			out0r, out0i := f0r.Add(s3r), f0i.Add(s3i)
			out1r, out1i := s5r.Add(s4i), s5i.Sub(s4r)
			out3r, out3i := s5r.Sub(s4i), s5i.Add(s4r)
			bflyStoreCpx4(p2, out2r, out2i)
			bflyStoreCpx4(p0, out0r, out0i)
			bflyStoreCpx4(p1, out1r, out1i)
			bflyStoreCpx4(p3, out3r, out3i)
		}
	}
}

func bflyLoadCpx4(p unsafe.Pointer) (re, im archsimd.Float32x4) {
	lo := loadF32x4(p).ToBits()
	hi := loadF32x4(unsafe.Add(p, 16)).ToBits()
	return lo.ConcatEven(hi).BitsToFloat32(), lo.ConcatOdd(hi).BitsToFloat32()
}

func bflyStoreCpx4(p unsafe.Pointer, re, im archsimd.Float32x4) {
	reBits, imBits := re.ToBits(), im.ToBits()
	storeF32x4(p, reBits.InterleaveLo(imBits).BitsToFloat32())
	storeF32x4(unsafe.Add(p, 16), reBits.InterleaveHi(imBits).BitsToFloat32())
}

func bflyGatherTwiddle4(wBase unsafe.Pointer, start, stride int) (re, im archsimd.Float32x4) {
	t0 := *(*kissCpx)(unsafe.Add(wBase, start*8))
	t1 := *(*kissCpx)(unsafe.Add(wBase, (start+stride)*8))
	t2 := *(*kissCpx)(unsafe.Add(wBase, (start+2*stride)*8))
	t3 := *(*kissCpx)(unsafe.Add(wBase, (start+3*stride)*8))
	re = archsimd.BroadcastFloat32x4(t0.r).SetElem(1, t1.r).SetElem(2, t2.r).SetElem(3, t3.r)
	im = archsimd.BroadcastFloat32x4(t0.i).SetElem(1, t1.i).SetElem(2, t2.i).SetElem(3, t3.i)
	return re, im
}

func bflyMulSource4(ar, ai, wr, wi archsimd.Float32x4) (re, im archsimd.Float32x4) {
	re = ar.MulAdd(wr, ai.Mul(wi).Neg())
	im = ar.MulAdd(wi, ai.Mul(wr))
	return re, im
}

// kfBflyScalarFastInput is false: the SIMD butterflies handle every stage.
func kfBflyScalarFastInput([]kissCpx) bool { return false }

func kfBfly2M4(fout []kissCpx, n int) {
	kfBfly2M4Scalar(fout, n)
}
