package celt

import (
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// BitrateMax mirrors OPUS_BITRATE_MAX: the encoder fills its payload budget
// instead of targeting a bitrate.
const BitrateMax = -1

// celtPacketSizeCap is the 1275-byte main-payload cap of celt_encode_with_ec
// (packet_size_cap in celt/celt_encoder.c).
const celtPacketSizeCap = 1275

// opusMaxDataBytes is the 1276-byte per-frame packet cap opus_encode_frame_native
// applies before handing nb_compr_bytes = max_data_bytes-1 to CELT
// (src/opus_encoder.c).
const opusMaxDataBytes = 1276

// bitrateToBits mirrors bitrate_to_bits() (celt/celt.h): the bits a frame of
// frameSize samples carries at bitrate on a mode running at fs.
func bitrateToBits(bitrate, fs, frameSize int32) int32 {
	return bitrate * 6 / (6 * fs / frameSize)
}

// FrameBudget holds the celt_encode_with_ec byte budget of one frame. The
// fields mirror the C locals of the same names (celt/celt_encoder.c).
type FrameBudget struct {
	nbCompressedBytes int32 // payload bytes the range coder may fill
	nbAvailableBytes  int32 // nbCompressedBytes minus the bytes already filled
	nbFilledBytes     int32 // bytes a shared range coder holds on entry
	effectiveBytes    int32 // rate-derived budget seen by the analysis stages
	vbrRate           int32 // VBR target in 1/8 bits per frame; 0 codes CBR
	equivRate         int32 // equivalent 20 ms bitrate for stereo/trim/bandwidth
	tell0Frac         int32 // ec_tell_frac on entry
}

// TotalBits returns total_bits = nbCompressedBytes*8.
func (b *FrameBudget) TotalBits() int { return int(b.nbCompressedBytes) * 8 }

// celtModeFs returns mode->Fs: 48 kHz for the standard modes, 96 kHz for the native
// HD mode, and the custom mode's rate for an Opus Custom mode.
func (e *Encoder) celtModeFs() int {
	if e.customScaleBase > 0 {
		return int(e.sampleRate)
	}
	if e.hd96kOverlap > 0 && e.sampleRate == 96000 {
		return 96000
	}
	return 48000
}

// modeMaxLM returns mode->maxLM. The standard and HD modes code up to 20 ms
// (LM 3); an Opus Custom encoder codes the mode's full frame, so its maxLM is
// the frame's LM.
func (e *Encoder) modeMaxLM(lm int) int {
	if e.customScaleBase > 0 {
		return lm
	}
	return 3
}

// modeEBands returns mode->eBands and mode->nbEBands.
func (e *Encoder) modeEBands() ([]int, int) {
	if pm := e.perMode; pm != nil {
		return pm.eBands, pm.nbEBands
	}
	return EBands[:], MaxBands
}

// packetSizeCap returns packet_size_cap: 1275 bytes, or QEXT_PACKET_SIZE_CAP
// while CELT-only QEXT coding is enabled.
func (e *Encoder) packetSizeCap() int32 {
	if extsupport.QEXT && e.qextActive() && !e.hybrid {
		return qextPacketSizeCap
	}
	return celtPacketSizeCap
}

// BitrateToBits returns bitrate_to_bits(st->bitrate, mode->Fs, frameSize) for
// the configured bitrate.
func (e *Encoder) BitrateToBits(frameSize int) int {
	return int(bitrateToBits(e.targetBitrate, int32(e.celtModeFs()), int32(frameSize)))
}

// cbrPayloadBytes returns the CBR payload a frame codes: the budget
// opus_encode_native hands CELT for a 4000-byte output buffer,
// nb_compr_bytes = min(cbr_bytes, 1276)-1 with cbr_bytes =
// (bitrate_to_bits(bitrate)+4)/8 (src/opus_encoder.c), bounded by the caller's
// payload budget. CELT-only QEXT coding lifts the 1276-byte cap
// (nb_compr_bytes = cbr_bytes-1).
func (e *Encoder) cbrPayloadBytes(frameSize int) int {
	cbrBytes := (bitrateToBits(e.targetBitrate, int32(e.celtModeFs()), int32(frameSize)) + 4) / 8
	if !extsupport.QEXT || !e.qextActive() || e.hybrid {
		cbrBytes = min(cbrBytes, opusMaxDataBytes)
	}
	payload := max(cbrBytes-1, 0)
	if e.maxPayloadBytes > 0 {
		payload = min(payload, e.maxPayloadBytes)
	}
	return int(payload)
}

// payloadBudget returns the nbCompressedBytes argument of celt_encode_with_ec:
// the caller's payload budget set with SetMaxPayloadBytes. Without one it is
// the budget opus_encode_native hands CELT for a 4000-byte output buffer: the
// packet size cap in VBR or at BitrateMax, and the rate-derived CBR payload
// otherwise.
func (e *Encoder) payloadBudget(frameSize int) int32 {
	if e.maxPayloadBytes > 0 {
		return e.maxPayloadBytes
	}
	if e.vbr || e.targetBitrate == BitrateMax {
		return e.packetSizeCap()
	}
	return int32(e.cbrPayloadBytes(frameSize))
}

// initFrameBudget ports the celt_encode_with_ec budget setup
// (celt/celt_encoder.c:1873-1927) for a frame coded into re with the payload
// budget nbCompressedBytes: the packet size cap, the VBR rate or the CBR payload
// size, and equiv_rate. A hybrid frame codes after SILK into the shared range
// coder, so the bytes SILK filled count against the budget. The caller shrinks
// the range coder to the returned budget.
func (e *Encoder) initFrameBudget(frameSize, lm, c int, nbCompressedBytes int32, re *rangecoding.Encoder) FrameBudget {
	fs := int32(e.celtModeFs())
	n := int32(frameSize)
	bitrate := e.targetBitrate
	tell := int32(re.Tell())
	b := FrameBudget{
		nbFilledBytes: (tell + 4) >> 3,
		tell0Frac:     int32(re.TellFrac()),
	}
	nbCompressedBytes = min(nbCompressedBytes, e.packetSizeCap())
	if e.vbr && bitrate != BitrateMax {
		b.vbrRate = bitrateToBits(bitrate, fs, n) << bitRes
		if e.customSignalling {
			// C ref: celt/celt_encoder.c celt_encode_with_ec() subtracts the
			// custom header byte from the finite VBR target.
			b.vbrRate -= 8 << bitRes
		}
		b.effectiveBytes = b.vbrRate >> (3 + bitRes)
	} else {
		if bitrate != BitrateMax {
			tmp := bitrate * n
			if tell > 1 {
				tmp += tell * fs
			}
			rateBytes := (tmp + 4*fs) / (8 * fs)
			if e.customSignalling {
				// celt_encode_with_ec() also excludes the custom header from
				// its finite CBR target.
				rateBytes--
			}
			nbCompressedBytes = max(2, min(nbCompressedBytes, rateBytes))
		}
		b.effectiveBytes = nbCompressedBytes - b.nbFilledBytes
	}
	b.nbCompressedBytes = nbCompressedBytes
	b.nbAvailableBytes = nbCompressedBytes - b.nbFilledBytes

	b.equivRate = int32(ComputeEquivRate(int(nbCompressedBytes), c, lm, int(bitrate)))
	return b
}

// constrainVBRBudget ports the constrained-VBR bust prevention of
// celt_encode_with_ec (celt/celt_encoder.c:1929-1957): the frame may not exceed
// the rate plus a one-frame bound minus the reservoir. It reports whether the
// budget shrank.
func (e *Encoder) constrainVBRBudget(b *FrameBudget, tell int) bool {
	if b.vbrRate <= 0 || !e.constrainedVBR {
		return false
	}
	vbrBound := b.vbrRate
	floor := int32(0)
	if tell == 1 {
		floor = 2
	}
	maxAllowed := min(max(floor, (b.vbrRate+vbrBound-e.vbrReservoir)>>(bitRes+3)), b.nbAvailableBytes)
	if maxAllowed >= b.nbAvailableBytes {
		return false
	}
	b.nbCompressedBytes = b.nbFilledBytes + maxAllowed
	b.nbAvailableBytes = maxAllowed
	return true
}

// vbrFrameInputs carries the per-frame analysis compute_vbr and the VBR block
// of celt_encode_with_ec read.
type vbrFrameInputs struct {
	lm              int
	c               int
	tell            int32 // ec_tell_frac after the allocation trim
	totalBoost      int32 // dynalloc boost coded in the bitstream
	totBoost        int32 // dynalloc_analysis boost estimate
	tfEstimate      float32
	pitchChange     bool
	maxDepth        celtGLog
	surroundMasking celtGLog
	temporalVBR     celtGLog
	silence         bool
}

// minAllowedBytes ports min_allowed (celt/celt_encoder.c:2419-2426): the frame
// keeps two bytes of margin beyond what is already coded, and a hybrid frame
// keeps the 37 bits a redundant-frame signal needs.
func (e *Encoder) minAllowedBytes(b *FrameBudget, in vbrFrameInputs) int32 {
	minAllowed := ((in.tell + in.totalBoost + (1 << (bitRes + 3)) - 1) >> (bitRes + 3)) + 2
	if e.hybrid {
		minAllowed = max(minAllowed, (b.tell0Frac+(37<<bitRes)+in.totalBoost+(1<<(bitRes+3))-1)>>(bitRes+3))
	}
	return minAllowed
}

// applyVBR ports the variable-bitrate block of celt_encode_with_ec
// (celt/celt_encoder.c:2427-2533): it sizes the frame from the compute_vbr
// target (or the hybrid target), advances the VBR averaging state, runs the
// constrained-VBR reservoir, and shrinks b.nbCompressedBytes to the frame
// size. A CBR frame (vbrRate == 0) is left unchanged.
func (e *Encoder) applyVBR(b *FrameBudget, in vbrFrameInputs) {
	if b.vbrRate <= 0 {
		return
	}
	lmDiff := e.modeMaxLM(in.lm) - in.lm
	minAllowed := e.minAllowedBytes(b, in)

	// Don't attempt to use more than 510 kb/s, even for frames smaller than 20 ms.
	nbCompressedBytes := min(b.nbCompressedBytes, e.packetSizeCap()>>(3-in.lm))
	var baseTarget int32
	if !e.hybrid {
		baseTarget = b.vbrRate - int32((40*in.c+20)<<bitRes)
	} else {
		baseTarget = max(0, b.vbrRate-int32((9*in.c+4)<<bitRes))
	}
	if e.constrainedVBR {
		baseTarget += e.vbrOffset >> lmDiff
	}

	var target int32
	if !e.hybrid {
		target = e.computeVBR(baseTarget, in.lm, in.c, b.equivRate, in.totBoost, in.tfEstimate,
			in.pitchChange, in.maxDepth, in.surroundMasking, in.temporalVBR)
	} else {
		target = baseTarget
		// Tonal frames (offset<100) need more bits than noisy (offset>100) ones.
		if e.silkOffset < 100 {
			target += 12 << bitRes >> (3 - in.lm)
		}
		if e.silkOffset > 100 {
			target -= 18 << bitRes >> (3 - in.lm)
		}
		// Boost transients and vowels with significant temporal spikes.
		target += int32((in.tfEstimate - 0.25) * float32(50<<bitRes))
		// A strong transient gets enough bits to fold the first two bands.
		if in.tfEstimate > 0.7 {
			target = max(target, 50<<bitRes)
		}
	}
	target += in.tell

	nbAvailableBytes := (target + (1 << (bitRes + 2))) >> (bitRes + 3)
	nbAvailableBytes = max(minAllowed, nbAvailableBytes)
	nbAvailableBytes = min(nbCompressedBytes, nbAvailableBytes)

	// By how much the frame missed the target.
	delta := target - b.vbrRate
	target = nbAvailableBytes << (bitRes + 3)

	// A silent frame leaves the drift alone so the rate does not shoot up after
	// a span of silence, but the reservoir still refills.
	if in.silence {
		nbAvailableBytes = 2
		target = 2 * 8 << bitRes
		delta = 0
	}

	var alpha float32
	if e.vbrCount < 970 {
		e.vbrCount++
		alpha = 1 / float32(e.vbrCount+20)
	} else {
		alpha = 0.001
	}
	if e.constrainedVBR {
		// Bits used in excess of the allowance.
		e.vbrReservoir += target - b.vbrRate
		// The offset that moves the average rate back to the target.
		e.vbrDrift += int32(alpha * float32(delta*(1<<lmDiff)-e.vbrOffset-e.vbrDrift))
		e.vbrOffset = -e.vbrDrift
		if e.vbrReservoir < 0 {
			// Under the minimum rate: spend the missing bits unless coding silence.
			adjust := -e.vbrReservoir / (8 << bitRes)
			if !in.silence {
				nbAvailableBytes += adjust
			}
			e.vbrReservoir = 0
		}
	}
	b.nbAvailableBytes = nbAvailableBytes
	b.nbCompressedBytes = min(nbCompressedBytes, nbAvailableBytes)
}

// computeVBR ports compute_vbr() (celt/celt_encoder.c:1604-1718) for the float
// build, where SHL32/SHR32 are no-ops, MULT16_16/MULT16_32_Q15 are float
// products, and DIV32_16 is a float division. bitrate is equiv_rate.
func (e *Encoder) computeVBR(baseTarget int32, lm, c int, bitrate, totBoost int32, tfEstimate float32,
	pitchChange bool, maxDepth, surroundMasking, temporalVBR celtGLog) int32 {
	eBands, nbEBands := e.modeEBands()
	hasSurroundMask := len(e.energyMask) > 0

	codedBands := int(e.lastCodedBands)
	if codedBands == 0 {
		codedBands = nbEBands
	}
	codedBins := int32(eBands[codedBands] << lm)
	if c == 2 {
		codedBins += int32(eBands[min(int(e.intensity), codedBands)] << lm)
	}

	target := baseTarget
	if e.analysisValid && e.analysisActivity < 0.4 {
		target -= int32(float32(codedBins<<bitRes) * (0.4 - float32(e.analysisActivity)))
	}
	// Stereo savings.
	if c == 2 {
		codedStereoBands := min(int(e.intensity), codedBands)
		codedStereoDOF := int32(eBands[codedStereoBands]<<lm) - int32(codedStereoBands)
		// Maximum fraction of the bits saved when the signal is mono.
		maxFrac := float32(0.8) * float32(codedStereoDOF) / float32(codedBins)
		stereoSaving := min(float32(e.lastStereoSaving), 1)
		target -= int32(min(maxFrac*float32(target), (stereoSaving-0.1)*float32(codedStereoDOF<<bitRes)))
	}
	// Boost the rate according to dynalloc (minus the dynalloc average for calibration).
	target += totBoost - (19 << lm)
	// Apply the transient boost, compensating for the average boost.
	const tfCalibration = float32(0.044)
	target += int32((tfEstimate - tfCalibration) * float32(target))

	// Tonality boost, compensating for the average.
	if e.analysisValid && !e.lfe {
		tonal := max(0, float32(e.analysisTonality)-0.15) - 0.12
		tonalTarget := target + int32(float32(codedBins<<bitRes)*1.2*tonal)
		if pitchChange {
			tonalTarget += int32(float32(codedBins<<bitRes) * 0.8)
		}
		target = tonalTarget
	}

	if hasSurroundMask && !e.lfe {
		surroundTarget := target + int32(float32(surroundMasking)*float32(codedBins<<bitRes))
		target = max(target/4, surroundTarget)
	}

	bins := int32(eBands[nbEBands-2] << lm)
	if extsupport.QEXT && e.qextActive() && !e.hybrid {
		bins = int32(e.modeShortMDCTSize() << lm)
	}
	floorDepth := int32(float32(int32(c)*bins<<bitRes) * float32(maxDepth))
	floorDepth = max(floorDepth, target>>2)
	target = min(target, floorDepth)

	// Constrained VBR can't sustain a higher rate for long, so it follows the
	// target less aggressively.
	if (!hasSurroundMask || e.lfe) && e.constrainedVBR {
		target = baseTarget + int32(0.67*float32(target-baseTarget))
	}

	if !hasSurroundMask && tfEstimate < 0.2 {
		amount := float32(0.0000031) * float32(max(0, min(32000, 96000-bitrate)))
		tvbrFactor := float32(temporalVBR) * amount
		target += int32(tvbrFactor * float32(target))
	}

	// Don't allow more than doubling the rate.
	return min(2*baseTarget, target)
}

// modeShortMDCTSize returns mode->shortMdctSize.
func (e *Encoder) modeShortMDCTSize() int {
	if e.customScaleBase > 0 {
		return e.customScaleBase
	}
	if e.hd96kOverlap > 0 && e.sampleRate == 96000 {
		return 2 * Overlap
	}
	return Overlap
}

// pitchChanged ports the celt_encode_with_ec pitch_change decision
// (celt/celt_encoder.c:2042-2044) from the previous frame's postfilter period and
// gain. run_prefilter raises the previous period to COMBFILTER_MINPERIOD before
// the comparison, and the 1.26/.79 period ratios compare in double precision,
// which the integer products reproduce exactly for comb-filter periods.
func (e *Encoder) pitchChanged(pf prefilterResult, prevPeriod int, prevGain float32) bool {
	prevPeriod = max(prevPeriod, combFilterMinPeriod)
	// analysis.tonality > .3 compares a float against a double: the float bound
	// float32(0.3) is the smallest float above the double 0.3.
	tonal := !e.analysisValid || float32(e.analysisTonality) >= 0.3
	return (pf.gain > 0.4 || prevGain > 0.4) && tonal &&
		(100*pf.pitch > 126*prevPeriod || 100*pf.pitch < 79*prevPeriod)
}
