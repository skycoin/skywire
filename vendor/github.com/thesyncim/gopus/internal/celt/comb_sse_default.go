//go:build !amd64 || !goexperiment.simd || nosimd || purego

package celt

const combUsesSSE = false

// combOverlapVector is false: combFilterOverlap is the scalar loop here.
const combOverlapVector = false

const combOverlapMax = 240

// combFilterConstSSE is only reached when combUsesSSE is true; builds without
// the amd64 SIMD kernel keep the scalar form of the same operation order.
func combFilterConstSSE(dst, src, delay []celtSig, from, to int, g10, g11, g12 float32) {
	for i := from; i < to; i++ {
		dst[i] = combFilterConstSSEValue(src[i], g10, g11, g12, delay[i+2], delay[i+3], delay[i+1], delay[i+4], delay[i])
	}
}

// combFilterOverlap is the cross-faded part of libopus comb_filter over
// len(dst) outputs, in place: d0[k] and d1[k] are x[i-T0-2+k] and
// x[i-T1-2+k] for the first output i, and wsq holds window[i]^2.
func combFilterOverlap(dst, d0, d1, wsq []float32, g00, g01, g02, g10, g11, g12 float32) {
	combFilterOverlapScalar(dst, d0, d1, wsq, g00, g01, g02, g10, g11, g12)
}
