//go:build arm64 && !purego

package celt

// imdctTDACWindowFMA32 is the arm64 assembly form of the IMDCT TDAC overlap-add
// windowing used when mdctUseFMALikeMixEnabled is set. For each step
// i in [0, count):
//
//	x1 = xsrc[xSrc0-i]
//	x2 = out[yOut0+i]
//	w1 = window[i]
//	w2 = window[wBwd0-i]
//	out[yOut0+i] = round(x2*w2 + round(-(x1*w1)))
//	out[xOut0-i] = round(x2*w1 + round( x1*w2))
//
//go:noescape
func imdctTDACWindowFMA32(out, xsrc, window []float32, yOut0, xOut0, xSrc0, wBwd0, count int)
