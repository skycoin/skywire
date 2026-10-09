//go:build !amd64 || !goexperiment.simd || nosimd || purego

package silk

// silkNSQDelDecUsesAVX2 is false outside the amd64 SIMD build: scalar and
// non-x86 libopus builds run silk_NSQ_del_dec_c.
const silkNSQDelDecUsesAVX2 = false

// nsqDelDecAVX2State is empty outside the amd64 SIMD build, which is the only
// build that runs the structure-of-arrays quantizer.
type nsqDelDecAVX2State struct{}

func nsqDelDecAVX2Supports(*NSQParams) bool { return false }

func noiseShapeQuantizeDelDecAVX2(*NSQState, []int16, *NSQParams, *nsqDelDecFrame) int {
	panic("silk: AVX2 delayed-decision NSQ outside the amd64 SIMD build")
}
