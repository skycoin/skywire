package celt

import (
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

func computeQEXTBandAmplitudesF32Into(mdctCoeffs []float32, cfg *qextModeConfig, end, lm int, bandE []celtEner) {
	if cfg == nil || end <= 0 {
		return
	}
	if end > len(cfg.EBands)-1 {
		end = len(cfg.EBands) - 1
	}
	if end > len(bandE) {
		end = len(bandE)
	}
	for i := 0; i < end; i++ {
		start := cfg.EBands[i] << lm
		stop := cfg.EBands[i+1] << lm
		if start < 0 {
			start = 0
		}
		if stop > len(mdctCoeffs) {
			stop = len(mdctCoeffs)
		}
		if stop <= start {
			bandE[i] = celtEner(1e-27)
			continue
		}
		sum := float32(1e-27)
		for _, v := range mdctCoeffs[start:stop] {
			sum += v * v
		}
		bandE[i] = celtEner(opusmath.SqrtF32(sum))
	}
}

func computeQEXTBandLogEF32Into(mdctCoeffs []float32, cfg *qextModeConfig, end, lm int, bandE []celtEner, bandLogE []celtGLog) {
	computeQEXTBandAmplitudesF32Into(mdctCoeffs, cfg, end, lm, bandE)
	if end > len(bandLogE) {
		end = len(bandLogE)
	}
	for i := 0; i < end; i++ {
		amp := float32(bandE[i])
		if amp < 1e-27 {
			amp = 1e-27
		}
		bandLogE[i] = celtGLog(celtLog2(amp) - float32(eMeans[i]))
	}
}

func normalizeQEXTBandsF32Into(mdctCoeffs []float32, cfg *qextModeConfig, end, lm int, bandE []celtEner, norm []celtNorm) {
	if cfg == nil || end <= 0 || len(norm) == 0 {
		return
	}
	clear(norm)
	if end > len(cfg.EBands)-1 {
		end = len(cfg.EBands) - 1
	}
	if end > len(bandE) {
		end = len(bandE)
	}
	for i := 0; i < end; i++ {
		start := cfg.EBands[i] << lm
		stop := cfg.EBands[i+1] << lm
		if start < 0 {
			start = 0
		}
		if stop > len(mdctCoeffs) {
			stop = len(mdctCoeffs)
		}
		if stop > len(norm) {
			stop = len(norm)
		}
		if stop <= start {
			continue
		}
		amp := float32(bandE[i])
		if amp < float32(1e-27) {
			amp = float32(1e-27)
		}
		g := float32(1.0) / amp
		for j := start; j < stop; j++ {
			norm[j] = celtNorm(mdctCoeffs[j] * g)
		}
	}
}

func (e *Encoder) encodeQEXTCoarseEnergyWithEncoder(re *rangecoding.Encoder, energies []celtGLog, nbBands, lm, nbAvailableBytes int, oldBandEState []celtGLog, quantizedEnergies []celtGLog, errorVals []celtGLog, delayedIntra *float32) bool {
	if re == nil || nbBands <= 0 {
		return false
	}
	channels := int(e.channels)
	needed := nbBands * channels
	if len(energies) < needed || len(quantizedEnergies) < needed || len(errorVals) < needed {
		return false
	}
	if len(oldBandEState) < MaxBands*channels {
		return false
	}

	savedRE := e.rangeEncoder
	savedPrev := e.prevEnergy
	savedDelayed := e.delayedIntra
	savedCoarseAvail := e.coarseAvailableBytes
	savedFrameBits := e.frameBits
	savedQuant := e.scratch.quantizedEnergies
	savedErr := e.scratch.coarseError
	defer func() {
		e.rangeEncoder = savedRE
		e.prevEnergy = savedPrev
		e.delayedIntra = savedDelayed
		e.coarseAvailableBytes = savedCoarseAvail
		e.frameBits = savedFrameBits
		e.scratch.quantizedEnergies = savedQuant
		e.scratch.coarseError = savedErr
	}()

	e.rangeEncoder = re
	e.prevEnergy = oldBandEState
	if delayedIntra != nil {
		e.delayedIntra = opusVal32(*delayedIntra)
	} else {
		e.delayedIntra = 0
	}
	e.coarseAvailableBytes = int32(nbAvailableBytes)
	e.frameBits = int32(re.StorageBits())
	e.scratch.quantizedEnergies = quantizedEnergies[:needed]
	e.scratch.coarseError = errorVals[:needed]

	clear(e.scratch.quantizedEnergies)
	clear(e.scratch.coarseError)

	intra := e.DecideIntraMode(energies, 0, nbBands, lm)
	if re.Tell()+3 <= int(e.frameBits) {
		re.EncodeBit(boolToInt(intra), 3)
	}
	e.EncodeCoarseEnergy(energies, nbBands, intra, lm)
	if delayedIntra != nil {
		*delayedIntra = float32(e.delayedIntra)
	}
	return intra
}
