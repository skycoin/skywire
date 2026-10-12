package celt

// imdctPostRotateF32FromKissScalar is the clt_mdct_backward_c() post-rotation
// of libopus celt/mdct.c. The float paths on arm64 and AMD64 v3 contract the
// first source product and round the second; mdctMulAddMix/mdctMulSubMix
// reproduce that shape when mdctUseFMALikeMixEnabled is set. Other targets
// keep the separately rounded products.
//
// The rotation reads fft[] without modifying it and writes only buf[] (the two
// are distinct, non-aliasing buffers), so it folds the libopus "copy fft
// into buf, then rotate in place" into a single pass that reads the source
// complex pair directly. Output pair i is buf[2i:2i+2] (libopus yp0) and pair
// k = n4-1-i is buf[2k:2k+2] (libopus yp1).
func imdctPostRotateF32FromKissScalar(buf []float32, fft []kissCpx, trig []float32, n2, n4 int) {
	if n4 <= 0 || len(buf) < n2 || len(fft) < n4 {
		return
	}
	imdctPostRotateF32FromKissPairs(buf, fft, trig, n4)
}

// imdctPostRotateF32FromKissPairs rotates checked complex input pairs into
// checked output pairs. Both source pairs are read before either output pair is
// written, including the middle pair when n4 is odd.
func imdctPostRotateF32FromKissPairs(buf []float32, fft []kissCpx, trig []float32, n4 int) {
	fft = fft[:n4]
	buf = buf[:2*n4]
	trigA := trig[:n4]
	trigB := trig[n4 : 2*n4][:len(trigA)]
	for i := range (len(trigA) + 1) >> 1 {
		k := len(trigA) - 1 - i
		re, im := fft[i].i, fft[i].r
		re2, im2 := fft[k].i, fft[k].r
		t0, t1 := trigA[i], trigB[i]
		t2, t3 := trigA[k], trigB[k]
		pairI := (*[2]float32)(buf[2*i:])
		pairK := (*[2]float32)(buf[2*k:])
		pairI[0] = mdctMulAddMix(re, im, t0, t1)
		pairK[1] = mdctMulSubMix(re, im, t1, t0)
		pairK[0] = mdctMulAddMix(re2, im2, t2, t3)
		pairI[1] = mdctMulSubMix(re2, im2, t3, t2)
	}
}
