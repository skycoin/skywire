package celt

import "github.com/thesyncim/gopus/internal/opusmath"

const maxExpRotationLength = maxBandWidth

var expRotationSpreadFactors = [3]int{15, 10, 5}

type expRotationCoeff struct {
	c float32
	s float32
}

var expRotationCoeffTable [len(expRotationSpreadFactors)][maxExpRotationLength + 1][MaxPVQK + 1]expRotationCoeff

func init() {
	for spreadIdx, spreadFactor := range expRotationSpreadFactors {
		for length := 1; length <= maxExpRotationLength; length++ {
			maxK := (length - 1) >> 1
			if maxK > MaxPVQK {
				maxK = MaxPVQK
			}
			for k := 0; k <= maxK; k++ {
				gain := float32(length) / float32(length+spreadFactor*k)
				theta := 0.5 * gain * gain
				expRotationCoeffTable[spreadIdx][length][k] = expRotationCoeff{
					c: opusmath.CELTCosNormF32(theta),
					s: opusmath.CELTCosNormF32(float32(1) - theta),
				}
			}
		}
	}
}

func expRotationCoefficients(length, k, spread int) (opusVal16, opusVal16, bool) {
	if spread < spreadLight || spread > spreadAggressive {
		return 0, 0, false
	}
	if length < 1 || length > maxExpRotationLength || k < 0 || k > MaxPVQK || 2*k >= length {
		return 0, 0, false
	}
	coeff := expRotationCoeffTable[spread-1][length][k]
	return opusVal16(coeff.c), opusVal16(coeff.s), true
}
