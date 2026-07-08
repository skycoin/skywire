//go:build arm64 && !purego

package celt

// celtFusedFloat reports whether this build lets the compiler fuse a*b+c into
// FMADD in the CELT float path (the default arm64/asm build, see
// fma32_arm64_fast.go). Such a build is quality-gated (opus_compare), not
// bit-exact with scalar libopus, exactly like libopus's own NEON kernels.
// The bit-exact Tier-1 oracle is the purego build (and amd64, which does not
// fuse), where celtFusedFloat is false.
const celtFusedFloat = true
