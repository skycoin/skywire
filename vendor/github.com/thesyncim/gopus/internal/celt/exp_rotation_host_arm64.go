//go:build arm64 && goexperiment.simd && !nosimd && !purego

package celt

func expRotationHostSupportsSIMD() bool {
	return true
}
