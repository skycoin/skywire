//go:build gopus_fixed_point

package celt

// celt/pitch.c:compute_pitch_gain returns zero for zero fixed-point operands.
const pitchGainZeroInputReturnsZero = true
