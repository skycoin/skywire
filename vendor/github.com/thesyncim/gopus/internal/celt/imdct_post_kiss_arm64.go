//go:build arm64 && !purego

package celt

//go:noescape
func imdctPostRotateF32FromKiss(buf []float32, fft []kissCpx, trig []float32, n2, n4 int)
