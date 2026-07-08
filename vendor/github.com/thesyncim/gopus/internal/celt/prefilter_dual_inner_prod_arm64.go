//go:build arm64 && !purego

package celt

// prefilterDualInnerProdAsm computes sum1 = <x,y1> and sum2 = <x,y2> over the
// first length samples using the 4-lane fused-multiply-add order of
// prefilterDualInnerProdF32NeonOrder (libopus arm/pitch_neon_intr.c
// dual_inner_prod_neon). Bit-identical to the Go reference.
//
//go:noescape
func prefilterDualInnerProdAsm(x, y1, y2 []float32, length int) (float32, float32)
