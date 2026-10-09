//go:build amd64 && !goexperiment.simd && !nosimd && !purego

package celt

// The ordinary Go build uses the scalar libopus accumulation order. The SIMD
// build selects the libopus SSE order when its vector kernels are available.
const libopusFloatInnerProdUsesSSEOrder = false
