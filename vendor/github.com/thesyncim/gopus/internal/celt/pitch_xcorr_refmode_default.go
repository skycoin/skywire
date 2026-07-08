//go:build !amd64 || purego

package celt

func libopusFloatPitchXCorrUsesAVX2FMA() bool {
	return false
}

func pitchFMADD32(a, b, c float32) float32 {
	return noFMA32Add(noFMA32Mul(a, b), c)
}

// pitchXcorrKernelAVX8 is the scalar fallback for builds without the AVX2 FMA
// kernel. pitchXCorrFloat32AVX2FMAOrder is only reached when
// libopusFloatPitchXCorrUsesAVX2FMA reports true (amd64 && !purego), so this
// path exists only to keep the shared helper compiling on other builds.
func pitchXcorrKernelAVX8(x, y []float32, sum *[8]float32, length int) {
	var sums [8][8]float32
	j := 0
	for ; j < length-7; j += 8 {
		for lane := range 8 {
			xv := x[j+lane]
			for corr := range 8 {
				sums[corr][lane] = pitchFMADD32(xv, y[j+lane+corr], sums[corr][lane])
			}
		}
	}
	if j != length {
		for lane := 0; lane < length-j; lane++ {
			xv := x[j+lane]
			for corr := range 8 {
				sums[corr][lane] = pitchFMADD32(xv, y[j+lane+corr], sums[corr][lane])
			}
		}
	}
	for corr := range 8 {
		sum[corr] = reduceAVX2PitchSum(sums[corr])
	}
}
