package celt

import "github.com/thesyncim/gopus/internal/opusmath"

// Stereo processing for CELT decoding.
// CELT supports three stereo coding modes:
// 1. Mid-side stereo: encode M=(L+R)/2 and S=(L-R)/2 with angular rotation
// 2. Intensity stereo: encode mono and spread with optional sign flip
// 3. Dual stereo: encode left and right independently
//
// Reference: RFC 6716 Section 4.3.4, libopus celt/bands.c

// StereoMode specifies the stereo coding mode for a band.
type StereoMode int

const (
	// StereoMidSide uses mid-side encoding with theta rotation.
	// Good for correlated stereo content (most music).
	StereoMidSide StereoMode = iota

	// StereoIntensity uses mono with optional sign inversion.
	// Used for high frequency bands to save bits.
	StereoIntensity

	// StereoDual encodes left and right independently.
	// Used when channels are uncorrelated.
	StereoDual
)

// String returns the string representation of the stereo mode.
func (sm StereoMode) String() string {
	switch sm {
	case StereoMidSide:
		return "mid-side"
	case StereoIntensity:
		return "intensity"
	case StereoDual:
		return "dual"
	default:
		return "unknown"
	}
}

// MidSideToLR converts mid-side stereo to left-right.
// The conversion uses a rotation matrix controlled by theta:
//
//	L = cos(theta) * M + sin(theta) * S
//	R = cos(theta) * M - sin(theta) * S
//
// Parameters:
//   - mid: mid channel coefficients (M = (L+R)/2)
//   - side: side channel coefficients (S = (L-R)/2)
//   - theta: stereo angle in radians (0 = mono, pi/2 = full stereo)
//
// Returns: left and right channel coefficient arrays
func MidSideToLR(mid, side []float32, theta float32) (left, right []float32) {
	n := len(mid)
	if n == 0 {
		return nil, nil
	}

	// Handle mismatched lengths
	if len(side) != n {
		// If side is empty/shorter, treat as mono
		side = make([]float32, n)
	}

	left = make([]float32, n)
	right = make([]float32, n)

	cosT := opusmath.CosF32(theta)
	sinT := opusmath.SinF32(theta)

	for i := range n {
		// Rotation matrix:
		// [L]   [cos(theta)  sin(theta)] [M]
		// [R] = [cos(theta) -sin(theta)] [S]
		left[i] = cosT*mid[i] + sinT*side[i]
		right[i] = cosT*mid[i] - sinT*side[i]
	}

	return left, right
}

// IntensityStereo creates stereo from mono with optional inversion.
// In intensity stereo mode, both channels share the same spectral shape
// but may have opposite signs. This is efficient for high-frequency
// content where the ear is less sensitive to phase.
//
// Parameters:
//   - mono: the mid channel coefficients
//   - invert: if true, right channel is inverted (sign flipped)
//
// Returns: left and right coefficient arrays
func IntensityStereo(mono []float32, invert bool) (left, right []float32) {
	n := len(mono)
	if n == 0 {
		return nil, nil
	}

	left = make([]float32, n)
	right = make([]float32, n)

	copy(left, mono)

	if invert {
		for i := range n {
			right[i] = -mono[i]
		}
	} else {
		copy(right, mono)
	}

	return left, right
}

// GetStereoMode determines the stereo mode for a band.
// The mode depends on:
//   - band index relative to intensity stereo start band
//   - whether dual stereo mode is enabled
//   - bit allocation for the band
//
// Parameters:
//   - band: band index (0 to nbBands-1)
//   - intensityBand: band where intensity stereo starts (-1 if not used)
//   - dualStereo: true if dual stereo mode is enabled
//
// Returns: the stereo mode to use for this band
func GetStereoMode(band, intensityBand int, dualStereo bool) StereoMode {
	// Check intensity stereo first
	if intensityBand >= 0 && band >= intensityBand {
		return StereoIntensity
	}

	// Dual stereo for explicitly flagged bands
	if dualStereo {
		return StereoDual
	}

	// Default to mid-side
	return StereoMidSide
}
