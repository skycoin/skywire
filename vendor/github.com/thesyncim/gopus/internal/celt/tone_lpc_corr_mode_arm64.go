//go:build arm64

package celt

// The selected libopus arm64 tone_lpc keeps sample order within each of its
// three correlations. SIMD parallelizes different correlations, not samples.
const toneLPCStereoLane4 = false
