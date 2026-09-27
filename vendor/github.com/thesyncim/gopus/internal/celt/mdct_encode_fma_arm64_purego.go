//go:build arm64 && purego

package celt

// mdctEncodeFMA32 computes a*b+c for the forward MDCT encoder fold via the
// Go arm64 backend's FMADDS contraction of fma32(a,b,c). It avoids the
// (FCVT+FMADDD+FCVT) round-trip of the shared mdctFMA32 helper while staying
// bit-identical to the libopus NEON vfmaq_f32 path. The encoder pitch-search
// and MDCT fold are quality-gated; the IMDCT decoder pre-rotation keeps
// mdctFMA32 to preserve existing parity fixtures.
func mdctEncodeFMA32(a, b, c float32) float32 { return fma32(a, b, c) }
