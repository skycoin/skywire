//go:build arm64 && !purego

package celt

// stereoMergeRescaleNEON applies the final mid/side rescale of stereoMerge over
// len(x) lanes in place. Every lane op is a bare FMUL/FADD/FSUB (matching the
// scalar noFMA32 path), so it is bit-exact on every build.
//
//go:noescape
func stereoMergeRescaleNEON(x, y []float32, mid, lgain, rgain float32)
