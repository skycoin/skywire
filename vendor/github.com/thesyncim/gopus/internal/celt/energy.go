// Package celt implements the CELT decoder per RFC 6716 Section 4.3.
package celt

import "github.com/thesyncim/gopus/internal/rangecoding"

// Laplace decoding constants per libopus celt/laplace.c.
const (
	laplaceLogMinP = 0
	laplaceMinP    = 1 << laplaceLogMinP
	laplaceNMin    = 16
	laplaceFTBits  = 15
	laplaceFS      = 1 << laplaceFTBits
)

// ec_laplace_get_freq1 returns the frequency of the "1" symbol.
// Reference: libopus celt/laplace.c
func ec_laplace_get_freq1(fs0 int, decay int) int {
	ft := laplaceFS - laplaceMinP*(2*laplaceNMin) - fs0
	return (ft * (16384 - decay)) >> 15
}

// DecodeLaplaceTest is an exported wrapper for testing.
//
// This helper exists for tests and codec-development tooling and may change.
// It decodes a Laplace-distributed integer using the range coder.
func (d *Decoder) DecodeLaplaceTest(fs int, decay int) int {
	return d.decodeLaplace(fs, decay)
}

// decodeLaplace decodes a Laplace-distributed integer using the range coder.
// Uses the probability model from RFC 6716 Section 4.3.2.1.
// Parameters:
//   - fs: total frequency (typically 32768)
//   - decay: controls the distribution spread (larger = narrower around 0)
//
// Reference: libopus celt/laplace.c ec_laplace_decode()
func (d *Decoder) decodeLaplace(fs int, decay int) int {
	rd := d.rangeDecoder
	if rd == nil {
		return 0
	}
	return decodeLaplaceWithRangeDecoder(rd, fs, decay)
}

func decodeLaplaceWithRangeDecoder(rd *rangecoding.Decoder, fs int, decay int) int {
	fm := int(rd.DecodeBin(laplaceFTBits))
	if fm < fs {
		rd.Update(0, uint32(fs), uint32(laplaceFS))
		return 0
	}

	val := 1
	fl := fs
	fs = ec_laplace_get_freq1(fs, decay) + laplaceMinP
	for fs > laplaceMinP && fm >= fl+2*fs {
		fs *= 2
		fl += fs
		fs = ((fs - 2*laplaceMinP) * decay >> 15) + laplaceMinP
		val++
	}
	if fs <= laplaceMinP {
		di := (fm - fl) >> (laplaceLogMinP + 1)
		val += di
		fl += 2 * di * laplaceMinP
	}
	if fm < fl+fs {
		val = -val
	} else {
		fl += fs
	}
	fh := fl + fs
	if fh > laplaceFS {
		fh = laplaceFS
	}
	rd.Update(uint32(fl), uint32(fh), uint32(laplaceFS))
	return val
}

// DecodeCoarseEnergy decodes coarse band energies in log2 units (1 = 6 dB).
// intra=true: no inter-frame prediction (first frame or after loss)
// intra=false: uses alpha prediction from previous frame
// Reference: RFC 6716 Section 4.3.2, libopus celt/quant_bands.c unquant_coarse_energy()
func (d *Decoder) DecodeCoarseEnergy(nbBands int, intra bool, lm int) []celtGLog {
	return d.decodeCoarseEnergyGLogInto(nil, nbBands, intra, lm)
}

func (d *Decoder) decodeCoarseEnergyGLogInto(dst []celtGLog, nbBands int, intra bool, lm int) []celtGLog {
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if nbBands < 0 {
		nbBands = 0
	}
	if lm < 0 {
		lm = 0
	}
	if lm > 3 {
		lm = 3
	}

	channels := int(d.channels)
	needed := nbBands * channels
	if len(dst) < needed {
		dst = make([]celtGLog, needed)
	} else {
		dst = dst[:needed]
	}

	rd := d.rangeDecoder
	if rd == nil {
		return dst
	}

	// Get prediction coefficients
	var alpha, beta float32
	if intra {
		alpha = 0.0
		beta = float32(BetaIntra)
	} else {
		alpha = float32(AlphaCoef[lm])
		beta = float32(BetaCoefInter[lm])
	}

	prob := eProbModel[lm][0]
	if intra {
		prob = eProbModel[lm][1]
	}

	budget := rd.StorageBits()

	// Decode band-major to match libopus ordering.
	var prevBandEnergy [2]float32
	for band := 0; band < nbBands; band++ {
		for c := range channels {
			// Decode Laplace-distributed residual
			tell := rd.Tell()
			qi := 0
			remaining := budget - tell
			if remaining >= 15 {
				pi := 2 * band
				if pi > 40 {
					pi = 40
				}
				fs := int(prob[pi]) << 7
				decay := int(prob[pi+1]) << 6
				qi = decodeLaplaceWithRangeDecoder(rd, fs, decay)
			} else if remaining >= 2 {
				qi = rd.DecodeICDF(smallEnergyICDF, 2)
				qi = (qi >> 1) ^ -(qi & 1)
			} else if remaining >= 1 {
				qi = -rd.DecodeBit(1)
			} else {
				qi = -1
			}

			// Apply prediction
			// pred = alpha * prevEnergy[band] + prevBandEnergy
			prevFrameEnergy := float32(d.prevEnergy[c*d.predStride()+band])
			minEnergy := float32(-9.0 * DB6)
			if prevFrameEnergy < minEnergy {
				prevFrameEnergy = minEnergy
			}

			// Compute energy: pred + qi * DB6 (6 dB per step)
			q := float32(qi) * float32(DB6)
			energy := alpha*prevFrameEnergy + prevBandEnergy[c] + q

			// Store result
			dst[c*nbBands+band] = celtGLog(energy)

			// Update prev band energy for next band's inter-band prediction.
			// Per libopus: prev is filtered by the quantized delta.
			// Formula: prev = prev + q - beta*q, where q = qi*DB6
			prevBandEnergy[c] = prevBandEnergy[c] + q - beta*q
		}
	}

	// Update previous frame energy for next frame's inter-frame prediction
	for c := range channels {
		for band := 0; band < nbBands; band++ {
			d.prevEnergy[c*d.predStride()+band] = dst[c*nbBands+band]
		}
	}

	return dst
}

func (d *Decoder) decodeCoarseEnergyRangeGLog(start, end int, intra bool, lm int, energies []celtGLog) {
	if d.rangeDecoder == nil {
		return
	}
	if start < 0 {
		start = 0
	}
	if end > MaxBands {
		end = MaxBands
	}
	if end <= start {
		return
	}
	if lm < 0 {
		lm = 0
	}
	if lm > 3 {
		lm = 3
	}
	channels := int(d.channels)
	if len(energies) < end*channels {
		return
	}

	rd := d.rangeDecoder

	// Prediction coefficients
	var alpha, beta float32
	if intra {
		alpha = 0.0
		beta = float32(BetaIntra)
	} else {
		alpha = float32(AlphaCoef[lm])
		beta = float32(BetaCoefInter[lm])
	}

	prob := eProbModel[lm][0]
	if intra {
		prob = eProbModel[lm][1]
	}

	budget := rd.StorageBits()

	// Inter-band prediction state starts at 0 (matches libopus).
	var prevBandEnergy [2]float32
	for band := start; band < end; band++ {
		for c := range channels {
			tell := rd.Tell()
			qi := 0
			remaining := budget - tell
			if remaining >= 15 {
				pi := 2 * band
				if pi > 40 {
					pi = 40
				}
				fs := int(prob[pi]) << 7
				decay := int(prob[pi+1]) << 6
				qi = d.decodeLaplace(fs, decay)
			} else if remaining >= 2 {
				qi = rd.DecodeICDF(smallEnergyICDF, 2)
				qi = (qi >> 1) ^ -(qi & 1)
			} else if remaining >= 1 {
				qi = -rd.DecodeBit(1)
			} else {
				qi = -1
			}

			prevFrameEnergy := float32(d.prevEnergy[c*d.predStride()+band])
			minEnergy := float32(-9.0 * DB6)
			if prevFrameEnergy < minEnergy {
				prevFrameEnergy = minEnergy
			}

			q := float32(qi) * float32(DB6)
			energy := alpha*prevFrameEnergy + prevBandEnergy[c] + q

			energies[c*end+band] = celtGLog(energy)
			prevBandEnergy[c] = prevBandEnergy[c] + q - beta*q
		}
	}
}

// DecodeCoarseEnergyWithDecoder decodes coarse energies using an explicit range decoder.
// This variant allows passing a range decoder directly rather than using d.rangeDecoder.
func (d *Decoder) DecodeCoarseEnergyWithDecoder(rd *rangecoding.Decoder, nbBands int, intra bool, lm int) []celtGLog {
	// Temporarily set range decoder
	oldRD := d.rangeDecoder
	d.rangeDecoder = rd
	defer func() { d.rangeDecoder = oldRD }()

	return d.DecodeCoarseEnergy(nbBands, intra, lm)
}

// DecodeFineEnergy adds fine energy precision to coarse values.
// fineBits[band] specifies bits allocated for refinement (0 = no refinement).
// Reference: RFC 6716 Section 4.3.2, libopus celt/quant_bands.c unquant_fine_energy()
func (d *Decoder) DecodeFineEnergy(energies []celtGLog, nbBands int, fineBits []int32) {
	d.decodeFineEnergyGLogRange(energies, 0, nbBands, nil, fineBits)
}

// DecodeFineEnergyWithDecoder adds fine energy precision using an explicit range decoder.
func (d *Decoder) DecodeFineEnergyWithDecoder(rd *rangecoding.Decoder, energies []celtGLog, nbBands int, fineBits []int32) {
	oldRD := d.rangeDecoder
	d.rangeDecoder = rd
	defer func() { d.rangeDecoder = oldRD }()

	d.decodeFineEnergyGLogRange(energies, 0, nbBands, nil, fineBits)
}

func (d *Decoder) decodeFineEnergyGLogWithDecoder(rd *rangecoding.Decoder, energies []celtGLog, nbBands int, fineBits []int32) {
	oldRD := d.rangeDecoder
	d.rangeDecoder = rd
	defer func() { d.rangeDecoder = oldRD }()

	d.decodeFineEnergyGLogRange(energies, 0, nbBands, nil, fineBits)
}

// DecodeFineEnergyRange adds fine energy precision for bands in [start, end).
// For hybrid mode, start should be HybridCELTStartBand (17), matching libopus
// unquant_fine_energy().
func (d *Decoder) DecodeFineEnergyRange(energies []celtGLog, start, end int, fineBits []int32) {
	d.decodeFineEnergyGLogRange(energies, start, end, nil, fineBits)
}

// decodeFineEnergyGLog mirrors libopus unquant_fine_energy() for float builds.
// prevQuant may be nil; extraQuant provides per-band refinement bits.
func (d *Decoder) decodeFineEnergyGLog(energies []celtGLog, nbBands int, prevQuant, extraQuant []int32) {
	d.decodeFineEnergyGLogRange(energies, 0, nbBands, prevQuant, extraQuant)
}

func (d *Decoder) decodeFineEnergyGLogRange(energies []celtGLog, start, end int, prevQuant, extraQuant []int32) {
	if d.rangeDecoder == nil {
		return
	}
	if start < 0 {
		start = 0
	}
	if end > MaxBands {
		end = MaxBands
	}
	if end > len(extraQuant) {
		end = len(extraQuant)
	}
	if end <= start {
		return
	}

	rd := d.rangeDecoder
	for band := start; band < end; band++ {
		extra := extraQuant[band]
		if extra <= 0 {
			continue
		}
		channels := int(d.channels)
		if rd.Tell()+channels*int(extra) > rd.StorageBits() {
			continue
		}

		prev := 0
		if prevQuant != nil && band < len(prevQuant) {
			prev = int(prevQuant[band])
		}

		for c := range channels {
			q2 := rd.DecodeRawBits(uint(extra))
			offset := (float32(q2)+float32(0.5))*float32(uint(1)<<uint(14-extra))*float32(1.0/16384.0) - float32(0.5)
			offset *= float32(uint(1)<<uint(14-prev)) * float32(1.0/16384.0)

			idx := c*end + band
			if idx < len(energies) {
				energies[idx] = celtGLog(float32(energies[idx]) + offset)
			}
		}
	}
}

// DecodeEnergyRemainder uses remaining bits for additional energy precision.
// Called after all PVQ bands decoded, uses leftover bits from bit allocation.
// Reference: RFC 6716 Section 4.3.2, libopus celt/quant_bands.c unquant_energy_finalise()
func (d *Decoder) DecodeEnergyRemainder(energies []celtGLog, nbBands int, remainderBits []int32) {
	if d.rangeDecoder == nil {
		return
	}
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if nbBands > len(remainderBits) {
		nbBands = len(remainderBits)
	}

	channels := int(d.channels)
	for c := range channels {
		for band := 0; band < nbBands; band++ {
			bits := remainderBits[band]
			if bits <= 0 {
				continue
			}

			// Remainder bits provide even finer precision
			// Each bit halves the remaining quantization interval

			// Decode single bit for each remainder bit
			for i := int32(0); i < bits && i < 8; i++ {
				bit := d.rangeDecoder.DecodeBit(1)

				// Each bit provides 6dB / 2^(fineBits+i+1) precision
				// The precision gets finer with each additional bit
				precision := celtGLog(float32(DB6) / float32(uint(1)<<uint(i+2)))

				idx := c*nbBands + band
				if idx < len(energies) {
					if bit == 1 {
						energies[idx] += precision
					} else {
						energies[idx] -= precision
					}
				}
			}
		}
	}
}

// DecodeEnergyRemainderWithDecoder uses remainder bits with an explicit range decoder.
func (d *Decoder) DecodeEnergyRemainderWithDecoder(rd *rangecoding.Decoder, energies []celtGLog, nbBands int, remainderBits []int32) {
	oldRD := d.rangeDecoder
	d.rangeDecoder = rd
	defer func() { d.rangeDecoder = oldRD }()

	d.DecodeEnergyRemainder(energies, nbBands, remainderBits)
}

// DecodeEnergyFinalise consumes leftover bits for additional energy refinement.
// This mirrors libopus unquant_energy_finalise().
// For non-hybrid mode, use start=0.
func (d *Decoder) DecodeEnergyFinalise(energies []celtGLog, nbBands int, fineQuant []int32, finePriority []int32, bitsLeft int) {
	d.decodeEnergyFinaliseGLogRange(0, nbBands, energies, fineQuant, finePriority, bitsLeft)
}

func (d *Decoder) decodeEnergyFinaliseGLog(energies []celtGLog, nbBands int, fineQuant []int32, finePriority []int32, bitsLeft int) {
	d.decodeEnergyFinaliseGLogRange(0, nbBands, energies, fineQuant, finePriority, bitsLeft)
}

// DecodeEnergyFinaliseRange consumes leftover bits for energy refinement in range [start, end).
// This mirrors libopus unquant_energy_finalise() which takes both start and end parameters.
// For hybrid mode, start should be HybridCELTStartBand (17).
func (d *Decoder) DecodeEnergyFinaliseRange(start, end int, energies []celtGLog, fineQuant []int32, finePriority []int32, bitsLeft int) {
	d.decodeEnergyFinaliseGLogRange(start, end, energies, fineQuant, finePriority, bitsLeft)
}

func (d *Decoder) decodeEnergyFinaliseGLogRange(start, end int, energies []celtGLog, fineQuant []int32, finePriority []int32, bitsLeft int) {
	if d.rangeDecoder == nil {
		return
	}
	if end > MaxBands {
		end = MaxBands
	}
	if start < 0 {
		start = 0
	}
	if end <= start {
		return
	}
	if bitsLeft < 0 {
		bitsLeft = 0
	}
	channels := int(d.channels)
	apply := len(energies) >= end*channels

	for prio := range 2 {
		for band := start; band < end && bitsLeft >= channels; band++ {
			if fineQuant[band] >= maxFineBits || finePriority[band] != int32(prio) {
				continue
			}
			for c := range channels {
				q2 := d.rangeDecoder.DecodeRawBits(1)
				if apply {
					offset := (float32(q2) - float32(0.5)) * float32(uint(1)<<uint(14-fineQuant[band]-1)) * float32(1.0/16384.0)
					idx := c*end + band
					energies[idx] = celtGLog(float32(energies[idx]) + offset)
				}
				bitsLeft--
			}
		}
	}
}
