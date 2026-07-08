//go:build !arm64 || purego

package celt

// celtFusedFloat is false on the bit-exact builds: purego (rounding barrier in
// fma32_arm64.go) and amd64 (no compiler FP contraction), where the CELT float
// path is byte-identical to scalar libopus and the Tier-1 oracle tests apply.
const celtFusedFloat = false
