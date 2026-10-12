//go:build !gopus_fixed_point

package celt

// celt/pitch.c:compute_pitch_gain evaluates the float division for zero inputs.
const pitchGainZeroInputReturnsZero = false
