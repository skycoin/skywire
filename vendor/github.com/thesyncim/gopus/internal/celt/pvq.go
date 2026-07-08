package celt

import (
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

func decodeUniformPVQIndex(rd *rangecoding.Decoder, ft uint32) uint32 {
	if ft <= 1<<rangecoding.EC_UINT_BITS {
		return rd.DecodeUniformSmall(ft)
	}
	return rd.DecodeUniform(ft)
}

// PVQ (Pyramid Vector Quantization) decoding for CELT band shapes.
// PVQ encodes normalized vectors where the L1 norm (sum of absolute values)
// equals a fixed number of pulses K. The decoder reads a CWRS index and
// converts it to a normalized L2 unit vector.
//
// Reference: RFC 6716 Section 4.3.4.1, libopus celt/bands.c quant_band()

// DecodePVQ decodes a PVQ codeword from the range decoder.
// n: band width (number of MDCT bins)
// k: number of pulses (from bit allocation)
// Returns: normalized celt_norm vector of length n with unit L2 norm.
//
// If k == 0, returns a zero vector (caller should fold from another band).
func (d *Decoder) DecodePVQ(n, k int) []celtNorm {
	if k == 0 || n <= 0 {
		// No pulses - return zero vector (will be folded)
		return make([]celtNorm, n)
	}

	// Read CWRS index from range coder
	// Index has V(n,k) possible values
	vSize := PVQ_V(n, k)
	if vSize == 0 {
		return make([]celtNorm, n)
	}

	index := decodeUniformPVQIndex(d.rangeDecoder, vSize)

	// Convert index to pulse vector using CWRS
	pulses := DecodePulses(index, n, k)

	out := make([]celtNorm, n)
	normalizeResidualInto(out, pulses, 1, 0)
	return out
}

// NormalizeVector scales a CELT norm vector to unit L2 norm.
// If the input vector has zero energy, returns the input unchanged.
func NormalizeVector(v []celtNorm) []celtNorm {
	if len(v) == 0 {
		return v
	}

	var energy float32
	for _, x := range v {
		energy += x * x
	}

	if energy < 1e-15 {
		// Avoid division by zero - return input unchanged
		return v
	}

	scale := celtRSqrt(energy)
	result := make([]celtNorm, len(v))
	for i, x := range v {
		result[i] = celtNorm(x * scale)
	}
	return result
}

// intToNorm converts integer PVQ pulses to CELT norm-width values.
func intToNorm(v []int) []celtNorm {
	if v == nil {
		return nil
	}
	result := make([]celtNorm, len(v))
	for i, x := range v {
		result[i] = celtNorm(x)
	}
	return result
}

// DecodeStereoTheta decodes theta with sign for stereo balance.
// qn: number of quantization steps (determines precision)
// Returns: itheta value (0 = pure mid, qn = pure side)
//
// Reference: libopus celt/bands.c compute_theta()
func (d *Decoder) DecodeStereoTheta(qn int) int {
	if qn <= 0 {
		return 0
	}

	// Decode as uniform value
	ft := uint32(qn + 1)
	itheta := 0
	if ft <= 1<<8 {
		itheta = int(d.rangeDecoder.DecodeUniformSmall(ft))
	} else {
		itheta = int(d.rangeDecoder.DecodeUniform(ft))
	}

	return itheta
}

// ThetaToGains converts itheta to mid and side gains.
// itheta: quantized angle (0 to qn)
// qn: number of quantization steps
// Returns: mid gain, side gain (both in [0, 1])
//
// Reference: libopus celt/bands.c
func ThetaToGains(itheta, qn int) (mid, side opusVal16) {
	if qn <= 0 {
		return 1.0, 0.0
	}

	// theta in [0, pi/2]
	const piOver2 = float32(1.5707963267948966)
	theta := float32(itheta) * piOver2 / float32(qn)

	mid = opusVal16(opusmath.CosF32(theta))
	side = opusVal16(opusmath.SinF32(theta))

	return mid, side
}

// DecodeIntensityStereo decodes intensity stereo for a band.
// mid: the mid channel coefficients
// Returns: left and right with optional sign inversion on right.
//
// In intensity stereo, both channels share the same shape but may have
// opposite signs (determined by a single bit).
//
// Reference: RFC 6716 Section 4.3.4.3
func (d *Decoder) DecodeIntensityStereo(mid []celtNorm) (left, right []celtNorm) {
	n := len(mid)
	left = make([]celtNorm, n)
	right = make([]celtNorm, n)

	// Copy mid to both channels
	copy(left, mid)
	copy(right, mid)

	// Decode inversion flag (1 bit)
	inv := d.rangeDecoder.DecodeBit(1) == 1

	if inv {
		// Invert right channel
		for i := range right {
			right[i] = -right[i]
		}
	}

	return left, right
}

func (d *Decoder) decodePVQNormInto(band, n, k int, dst []celtNorm) {
	if k == 0 || n <= 0 || len(dst) < n {
		for i := 0; i < n && i < len(dst); i++ {
			dst[i] = 0
		}
		return
	}

	vSize := PVQ_V(n, k)
	if vSize == 0 {
		for i := range n {
			dst[i] = 0
		}
		return
	}

	index := decodeUniformPVQIndex(d.rangeDecoder, vSize)

	// Convert index to pulse vector using CWRS with pre-allocated buffer
	pulses := d.scratchBands.ensurePVQPulses(n)
	decodePulsesInto32(index, n, k, pulses, &d.scratchBands)

	// Convert to float and normalize directly into dst
	var energy opusVal16
	for i := range n {
		pulse := float32(pulses[i])
		dst[i] = pulse
		energy = opusVal16(energy + pulse*pulse)
	}

	// Normalize to unit L2 energy
	if energy >= 1e-15 {
		scale := celtRSqrt(energy)
		for i := range n {
			dst[i] = celtNorm(dst[i] * scale)
		}
	}
}

func (d *Decoder) decodeIntensityStereoNormInto(mid, left, right []celtNorm) {
	n := len(mid)
	if len(left) < n || len(right) < n {
		return
	}

	inv := d.rangeDecoder.DecodeBit(1) == 1
	if inv {
		for i := range n {
			left[i] = mid[i]
			right[i] = -mid[i]
		}
		return
	}
	copy(left[:n], mid)
	copy(right[:n], mid)
}

func applyMidSideRotationNormInto(mid, side []celtNorm, midGain, sideGain opusVal16, left, right []celtNorm) {
	n := len(mid)
	if len(side) != n || len(left) < n || len(right) < n {
		return
	}

	mg := midGain
	sg := sideGain
	for i := range n {
		m := mid[i]
		s := side[i]
		left[i] = celtNorm(mg*m + sg*s)
		right[i] = celtNorm(mg*m - sg*s)
	}
}
