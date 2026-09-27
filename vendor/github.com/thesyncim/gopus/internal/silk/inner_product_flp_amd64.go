//go:build amd64 && !purego

package silk

import "github.com/thesyncim/gopus/internal/cpufeat"

var silkUseInnerProductFLPAVX2FMA = cpufeat.AMD64.HasAVX2 && cpufeat.AMD64.HasFMA

//go:noescape
func innerProductFLPAVX2(a, b []float32, length int) silkCReal

func innerProductFLPImpl(a, b []float32, length int) silkCReal {
	if length <= 0 {
		return 0
	}
	if silkUseInnerProductFLPAVX2FMA {
		_ = a[length-1]
		_ = b[length-1]
		return innerProductFLPAVX2(a, b, length)
	}
	return innerProductF32Libopus(a, b, length)
}
