//go:build arm64

package celt

// libopusPitchSearchUsesFMA records the arm64 compiler's contraction of the
// float Syy recurrence in celt/pitch.c find_best_pitch(), including the
// generic-C archive used by the nosimd lane.
const libopusPitchSearchUsesFMA = true
