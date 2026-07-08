//go:build arm64 && !purego

package celt

// imdctPreRotateFMA32Kiss is the arm64 assembly form of the IMDCT pre-rotation
// used when mdctUseFMALikeMixEnabled is set. It reproduces the fused float shape
//
//	yr = round(x1*t0 + round(-(x2*t1)))
//	yi = round(x2*t0 + round(x1*t1))
//
// where x1=spectrum[2*i], x2=spectrum[n2-1-2*i], t0=trig[i], t1=trig[n4+i], and
// writes complex(yr, yi) into fftIn[i].
//
//go:noescape
func imdctPreRotateFMA32Kiss(fftIn []complex64, spectrum []float32, trig []float32, n2, n4 int)
