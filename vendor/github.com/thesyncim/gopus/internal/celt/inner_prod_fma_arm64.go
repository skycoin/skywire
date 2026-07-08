//go:build arm64 && !purego

package celt

// celtInnerProd8FMA32 computes the float32 inner product of x and y over the
// first n elements using the 4-lane fused-multiply-add accumulation order of
// celtInnerProdNeonStyle (libopus arm/pitch_neon_intr.c celt_inner_prod_neon).
// The lane layout and horizontal reduction are bit-identical to the Go path.
//
//go:noescape
func celtInnerProd8FMA32(x, y []float32, n int) float32
