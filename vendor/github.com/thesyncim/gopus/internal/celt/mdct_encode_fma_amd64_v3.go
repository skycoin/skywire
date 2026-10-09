//go:build amd64.v3

package celt

// Go contracts this expression to the AMD64 v3 FMA instruction, matching the
// scalar float path in libopus celt/mdct.c without software emulation.
func mdctEncodeFMA32(a, b, c float32) float32 { return fma32(a, b, c) }

// mdctMixFMA32 uses native AMD64 v3 contraction for MDCT mix hot paths.
//go:noinline
func mdctMixFMA32(a, b, c float32) float32 { return fma32(a, b, c) }
