//go:build gopus_fixed_point && gopus_qext

package fixedpoint

import (
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// qextMaxPVQDimension matches celtMaxBandWidth. The largest supported
// standard partition is 22*2^3=176; QEXT mode partitions are at most 10*2^3=80.
const qextMaxPVQDimension = celtMaxBandWidth

// AlgUnquantQEXT ports celt/vq.c alg_unquant for FIXED_POINT + ENABLE_QEXT.
// The main coder supplies the base CWRS vector and extDec supplies the QEXT
// refinement symbols. x is normalized in place and the returned mask is the
// collapse mask from the refined pulse vector.
func AlgUnquantQEXT(x []int32, n, k, spread, blocks int, dec, extDec *rangecoding.Decoder,
	gain int32, extraBits int) uint32 {
	if extraBits < 2 || extDec == nil {
		return AlgUnquant(x, n, k, spread, blocks, dec, gain)
	}
	if n < 2 || n > qextMaxPVQDimension || k < 1 || len(x) < n || dec == nil {
		return 0
	}

	var iyStorage [qextMaxPVQDimension]int32
	var row [256]uint32
	iy := iyStorage[:n]
	celt.DecodePulsesInto32(dec.DecodeUniform(celt.PVQ_V(n, k)), n, k, iy, row[:])

	up := (1 << extraBits) - 1
	yyShift := imax(0, extraBits-7)
	var ryy int32
	if n == 2 {
		refine := int32(extDec.DecodeUniform(uint32(up))) - int32((up-1)/2)
		iy[0] *= int32(up)
		iy[1] *= int32(up)
		if iy[1] == 0 {
			if iy[0] > 0 {
				iy[1] = -refine
			} else {
				iy[1] = refine
			}
			if int64(refine)*int64(iy[0]) > 0 {
				iy[0] -= refine
			} else {
				iy[0] += refine
			}
		} else if iy[1] > 0 {
			iy[0] += refine
			sign := int32(1)
			if iy[0] <= 0 {
				sign = -1
			}
			iy[1] -= refine * sign
		} else {
			iy[0] -= refine
			sign := int32(1)
			if iy[0] <= 0 {
				sign = -1
			}
			iy[1] -= refine * sign
		}
		yy64 := int64(iy[0])*int64(iy[0]) + int64(iy[1])*int64(iy[1])
		if yyShift > 0 {
			yy64 += int64(1) << uint(2*yyShift-1)
			yy64 >>= uint(2 * yyShift)
		}
		ryy = int32(yy64)
	} else {
		var refineStorage [qextMaxPVQDimension]int32
		refine := refineStorage[:n-1]
		useEntropy := extDec.StorageBits()-extDec.Tell() > (n-1)*(extraBits+3)+1
		for i := 0; i < n-1; i++ {
			refine[i] = int32(decodeQEXTPVQRefine(extDec, up, extraBits, useEntropy))
		}
		sign := 0
		if iy[n-1] == 0 {
			sign = int(extDec.DecodeRawBit())
		} else if iy[n-1] < 0 {
			sign = 1
		}
		for i := 0; i < n-1; i++ {
			iy[i] = iy[i]*int32(up) + refine[i]
		}
		last := int32(up * k)
		for i := 0; i < n-1; i++ {
			last -= abs32(iy[i])
		}
		if sign != 0 {
			last = -last
		}
		iy[n-1] = last
		ryy = qextRoundedSquareSum(iy, n, yyShift)
	}

	normaliseResidualQEXT(iy, x, n, ryy, gain, yyShift)
	expRotation(x, n, -1, blocks, k, spread)
	return extractCollapseMask32(iy, n, blocks)
}

// decodeQEXTPVQRefine mirrors celt/vq.c ec_dec_refine.
func decodeQEXTPVQRefine(dec *rangecoding.Decoder, up, extraBits int, useEntropy bool) int {
	logp := uint(1)
	if useEntropy {
		logp = 3
	}
	if dec.DecodeBit(logp) != 0 {
		sign := int(dec.DecodeRawBit())
		refine := int(dec.DecodeRawBits(uint(extraBits-1))) + up/2 + 1
		if sign != 0 {
			return -refine
		}
		return refine
	}
	return int(dec.DecodeRawBits(uint(extraBits))) - up/2
}

// cubicUnquantQEXT ports celt/vq.c cubic_unquant and cubic_synthesis in the
// fixed-point QEXT build. The bounded CELT mode widths fit the local pulse
// vector and keep the decoder hot path allocation-free.
func cubicUnquantQEXT(x []int32, n, resolution, blocks int, dec *rangecoding.Decoder, gain int32) uint32 {
	if n <= 0 || n > qextMaxPVQDimension || len(x) < n || dec == nil {
		return 0
	}
	k := 1 << resolution
	if blocks != 1 {
		k = imax(1, k-1)
	}
	if k == 1 {
		clear(x[:n])
		return 0
	}
	face := int(dec.DecodeUniform(uint32(n)))
	sign := int(dec.DecodeRawBit())
	var iyStorage [qextMaxPVQDimension]int32
	iy := iyStorage[:n]
	for i := 0; i < n; i++ {
		if i != face {
			iy[i] = int32(dec.DecodeRawBits(uint(resolution)))
		}
	}
	qextCubicSynthesis(x[:n], iy, n, k, face, sign, gain)
	return (1 << blocks) - 1
}

func qextCubicSynthesis(x []int32, iy []int32, n, k, face, sign int, gain int32) {
	var sum int32
	shift := imax(int(CeltILog2(int32(k)))+int(CeltILog2(int32(n)))/2-13, 0)
	for i := 0; i < n; i++ {
		x[i] = int32(1+2*iy[i]) - int32(k)
	}
	if sign != 0 {
		x[face] = -int32(k)
	} else {
		x[face] = int32(k)
	}
	for i := 0; i < n; i++ {
		term := mult16x16(x[i], x[i])
		if shift > 0 {
			term = pshr32(term, 2*shift)
		}
		sum += term
	}
	sumShift := (29 - int(CeltILog2(sum))) >> 1
	mag := CeltRsqrtNorm32(shl32(sum, 2*sumShift+1))
	g := mult32x32q31(mag, gain)
	outShift := shift - sumShift + 29 - normShift
	for i := 0; i < n; i++ {
		v := mult16x32Q15(int16(x[i]), g)
		x[i] = vshr32(v, outShift)
	}
}
