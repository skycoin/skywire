// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file provides the complete frame encoding pipeline.

package celt

import (
	"errors"
	"math"

	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// Encoding errors
var (
	// ErrInvalidInputLength indicates the PCM input length doesn't match frame size.
	ErrInvalidInputLength = errors.New("celt: invalid input length")

	// ErrEncodingFailed indicates a general encoding failure.
	ErrEncodingFailed = errors.New("celt: encoding failed")
)

// fillMDCTHistoryFromPrefilter mirrors libopus overlap sourcing for CELT MDCT:
// in[0:overlap] comes from the previous filtered output tail (st->in_mem).
func (e *Encoder) fillMDCTHistoryFromPrefilter(channel, overlap int, dst []float32) {
	if overlap <= 0 || len(dst) < overlap || channel < 0 {
		return
	}
	if len(e.overlapBuffer) < (channel+1)*overlap {
		for i := range overlap {
			dst[i] = 0
		}
		return
	}
	start := channel * overlap
	src := e.overlapBuffer[start : start+overlap]
	for i := range overlap {
		dst[i] = float32(src[i])
	}
}

func (e *Encoder) fillTransientHistoryFromPrefilterF32(overlap int, dst []float32) {
	if overlap <= 0 || e.channels <= 0 {
		return
	}
	channels := int(e.channels)
	need := overlap * channels
	if len(dst) < need {
		return
	}
	maxPeriod := e.combMaxPeriod()
	if len(e.prefilterMem) < maxPeriod*channels {
		clear(dst[:need])
		return
	}
	base := maxPeriod - overlap
	for ch := range channels {
		chBase := ch * maxPeriod
		for i := range overlap {
			dst[i*channels+ch] = float32(e.prefilterMem[chBase+base+i])
		}
	}
}

func (e *Encoder) quantizeInputToLSBDepthScratchF32(pcm []float32) []float32 {
	if len(pcm) == 0 {
		return pcm
	}
	depth := e.LSBDepth()
	scale := float32(math.Ldexp(1.0, depth-1))
	invScale := float32(1.0) / scale
	out := ensureFloat32Slice(&e.scratch.quantizedInputF32, len(pcm))
	for i, v := range pcm {
		out[i] = float32(floor32ToInt(float32(0.5)+v*scale)) * invScale
	}
	return out[:len(pcm)]
}

// computeSurroundDynallocFromMask mirrors libopus surround masking reduction in
// celt_encoder.c: derive per-band dynalloc floors and surround trim from
// externally supplied energy masks.
func (e *Encoder) computeSurroundDynallocFromMask(nbBands int, out []celtGLog) (celtGLog, bool) {
	if nbBands <= 0 || len(out) < nbBands {
		return e.surroundTrim, false
	}
	for i := range nbBands {
		out[i] = 0
	}
	if e.lfe || e.hybrid {
		return e.surroundTrim, false
	}

	maskBands := MaxBands
	channels := int(e.channels)
	needed := maskBands * channels
	if len(e.energyMask) < needed {
		return e.surroundTrim, false
	}

	maskEnd := min(max(int(e.lastCodedBands), 2), nbBands)
	if maskEnd > maskBands {
		maskEnd = maskBands
	}
	if maskEnd <= 0 {
		return e.surroundTrim, false
	}

	maskAvg := celtGLog(0)
	diff := celtGLog(0)
	count := 0
	for c := range channels {
		base := c * maskBands
		for i := 0; i < maskEnd; i++ {
			mask := e.energyMask[base+i]
			if mask > 0.25 {
				mask = 0.25
			}
			if mask < -2.0 {
				mask = -2.0
			}
			if mask > 0 {
				mask *= 0.5
			}
			width := EBands[i+1] - EBands[i]
			maskAvg += mask * celtGLog(width)
			count += width
			diff += mask * celtGLog(1+2*i-maskEnd)
		}
	}
	if count <= 0 {
		return e.surroundTrim, false
	}
	maskAvg = maskAvg/celtGLog(count) + 0.2

	denom := celtGLog(channels * (maskEnd - 1) * (maskEnd + 1) * maskEnd)
	if denom > 0 {
		diff = (diff * 6.0 / denom) * 0.5
	} else {
		diff = 0
	}
	if diff > 0.031 {
		diff = 0.031
	}
	if diff < -0.031 {
		diff = -0.031
	}
	surroundTrim := 64.0 * diff

	midband := 0
	for midband+1 < len(EBands) && EBands[midband+1] < EBands[maskEnd]/2 {
		midband++
	}

	countDynalloc := 0
	for i := 0; i < maskEnd; i++ {
		lin := maskAvg + diff*celtGLog(i-midband)
		unmask := e.energyMask[i]
		if e.channels == 2 {
			other := e.energyMask[maskBands+i]
			if other > unmask {
				unmask = other
			}
		}
		if unmask > 0 {
			unmask = 0
		}
		unmask -= lin
		if unmask > 0.25 {
			out[i] = unmask - 0.25
			countDynalloc++
		}
	}

	if countDynalloc >= 3 {
		maskAvg += 0.25
		if maskAvg > 0 {
			for i := 0; i < maskEnd; i++ {
				out[i] = 0
			}
		} else {
			for i := 0; i < maskEnd; i++ {
				v := out[i] - 0.25
				if v < 0 {
					v = 0
				}
				out[i] = v
			}
		}
	}

	return surroundTrim, true
}

// EncodeFrame encodes a complete CELT frame from PCM samples.
// pcm: input samples (interleaved if stereo), length = frameSize * channels
// frameSize: 120, 240, 480, or 960 samples
// Returns: encoded bytes
//
// The encoding pipeline (mirrors decoder's DecodeFrame):
// 1. Validate inputs
// 2. Get mode configuration
// 3. Detect transient
// 4. Apply pre-emphasis
// 5. Compute MDCT
// 6. Compute band energies
// 7. Normalize bands
// 8. Initialize range encoder
// 9. Encode frame flags (silence, transient, intra)
// 10. For stereo: encode stereo params
// 11. Encode coarse energy
// 12. Compute bit allocation
// 13. Encode fine energy
// 14. Encode bands (PVQ)
// 15. Finalize and return bytes
//
// Reference: RFC 6716 Section 4.3, libopus celt/celt_encoder.c
// upsampleZeroStuff produces a core-length (apiFrameSize*upsample) interleaved
// PCM buffer with each native sample placed at stride `upsample` and zeros
// elsewhere, mirroring libopus celt_preemphasis() zero-stuffing (inp[i*upsample]
// = RES2SIG(pcmp[CC*i]); OPUS_CLEAR(inp,N)). The subsequent scaling pre-emphasis
// then matches the 48 kHz path exactly because scaling a zero stays zero.
func (e *Encoder) upsampleZeroStuff(apiPCM []float32, apiFrameSize, channels, upsample int) []float32 {
	core := apiFrameSize * upsample
	buf := ensureFloat32Slice(&e.scratch.upsampleStuff, core*channels)[:core*channels]
	for i := range buf {
		buf[i] = 0
	}
	for i := range apiFrameSize {
		dst := i * upsample * channels
		src := i * channels
		for c := range channels {
			buf[dst+c] = apiPCM[src+c]
		}
	}
	return buf
}

// applyUpsampleMDCTScaling mirrors libopus compute_mdcts() upsample post-step:
// for upsample != 1 it scales the lower B*N/upsample MDCT bins by upsample and
// zeros the upper bins (removing the spectral images created by zero-stuffing).
// coeffs holds one channel's B*N bins (length == core frameSize).
func applyUpsampleMDCTScaling(coeffs []float32, upsample int) {
	if upsample <= 1 || len(coeffs) == 0 {
		return
	}
	bound := len(coeffs) / upsample
	up := float32(upsample)
	for i := range bound {
		coeffs[i] *= up
	}
	for i := bound; i < len(coeffs); i++ {
		coeffs[i] = 0
	}
}

// EncodeFrame encodes one CELT frame of float32 PCM and returns the packet
// bytes. pcm is interleaved when the encoder is stereo. frameSize is given at
// the encoder's API sample rate; at sub-48 kHz rates the input is upsampled to
// the 48 kHz core block internally.
func (e *Encoder) EncodeFrame(pcm []float32, frameSize int) ([]byte, error) {
	channels := int(e.channels)

	// At sub-48 kHz API rates the caller passes a native-Fs frame size and
	// native-length PCM; the CELT core block is frameSize*upsample (libopus
	// celt_encode_with_ec frame_size *= st->upsample). The input is zero-stuffed
	// to the core size in pre-emphasis below. At 48 kHz upsample==1 so this is the
	// identity and the frame size / input length checks are unchanged.
	upsample := e.effectiveUpsample()
	apiFrameSize := frameSize
	apiPCM := pcm
	if upsample > 1 {
		core := frameSize * upsample
		if !e.validFrameSize(core) {
			return nil, ErrInvalidFrameSize
		}
		if len(pcm) != apiFrameSize*channels {
			return nil, ErrInvalidInputLength
		}
		frameSize = core
		pcm = e.upsampleZeroStuff(apiPCM, apiFrameSize, channels, upsample)
	} else {
		// Step 1: Validate inputs
		if !e.validFrameSize(frameSize) {
			return nil, ErrInvalidFrameSize
		}
		if len(pcm) != frameSize*channels {
			return nil, ErrInvalidInputLength
		}
	}

	// Step 2: Get mode configuration
	mode := e.modeConfig(frameSize)
	nbBands := e.effectiveBandCount(frameSize)
	lm := mode.LM
	codedChannels := e.codedChannels()

	// Ensure scratch buffers are properly sized for this frame
	e.ensureScratch(frameSize)

	// Step 3a: Match the top-level encoder's input contract before local CELT
	// preprocessing: quantize to the configured LSB depth, then run dc_reject
	// if this encoder instance owns that stage.
	// Reference: libopus src/opus_encoder.c line 2008: dc_reject(pcm, 3, ...)
	samplesForFrame := pcm
	if e.lsbQuantizationEnabled {
		samplesForFrame = e.quantizeInputToLSBDepthScratchF32(pcm)
	}
	if e.dcRejectEnabled {
		samplesForFrame = e.applyDCRejectScratch(samplesForFrame)
	}

	// Step 3b: Optionally apply Opus-style CELT delay compensation.
	// Standalone CELT keeps this enabled by default.
	// Top-level Opus integration disables it and compensates externally.
	if e.delayCompensationEnabled {
		samplesForFrame = e.ApplyDelayCompensationScratchHybrid(samplesForFrame, frameSize)
	}

	// Step 4: Detect transient and compute tf_estimate using PRE-EMPHASIZED signal
	// libopus calls transient_analysis(in, N+overlap, ...) where 'in' contains:
	// - Previous frame's pre-emphasized overlap samples (indices 0 to overlap-1)
	// - Current frame's pre-emphasized samples (indices overlap to overlap+N-1)
	// Reference: libopus celt_encoder.c line 2030
	overlap := min(e.analysisOverlap(), frameSize)
	mdctPrevL := ensureFloat32Slice(&e.scratch.mdctPrevL, overlap)[:overlap]
	mdctPrevR := ensureFloat32Slice(&e.scratch.mdctPrevR, overlap)[:overlap]
	e.fillMDCTHistoryFromPrefilter(0, overlap, mdctPrevL)
	if e.channels == 2 {
		e.fillMDCTHistoryFromPrefilter(1, overlap, mdctPrevR)
	}

	// Build combined signal for transient analysis: [overlap from previous frame] + [current frame]
	// Total length: (overlap + frameSize) * channels - use scratch buffer
	preemphBufSize := overlap * channels
	transientLen := (overlap + frameSize) * channels
	transientInput := e.scratch.transientInput
	if len(transientInput) < transientLen {
		transientInput = make([]float32, transientLen)
		e.scratch.transientInput = transientInput
	}
	transientInput = transientInput[:transientLen]
	// Match libopus celt_preemphasis() ordering, but write the current frame
	// directly after the overlap history so transient analysis needs no copy.
	preemph := transientInput[preemphBufSize:]
	e.fillTransientHistoryFromPrefilterF32(overlap, transientInput[:preemphBufSize])
	isSilence := e.applyPreemphasisWithScalingAndSilenceCore(samplesForFrame, preemph, frameSize, overlap)

	allowWeakTransients := false
	if e.hybrid {
		effectiveBytes := int(e.maxPayloadBytes)
		if effectiveBytes <= 0 {
			bits := e.BitrateToBits(frameSize)
			effectiveBytes = (bits + 7) / 8
		}
		allowWeakTransients = effectiveBytes < 15 && e.silkSignalType != 2
	}

	// Call transient analysis with the pre-emphasized signal (N+overlap samples)
	// and the libopus hybrid weak-transient gate when a SILK handoff is active.
	// libopus gates transient_analysis() behind st->complexity >= 1 && !st->lfe
	// (celt_encoder.c:2023). tone_detect() always runs (line 2020), so at
	// complexity 0 only tone_freq/toneishness are populated while the transient
	// outputs stay 0.
	var transientResult TransientAnalysisResult
	if e.complexity < 1 || e.lfe {
		transientResult = e.toneDetectOnlyF32(transientInput[:transientLen], frameSize+overlap)
	} else if e.channels == 1 {
		transientResult = e.transientAnalysisMonoFloat32(transientInput[:transientLen], frameSize+overlap, allowWeakTransients)
	} else {
		transientResult = e.TransientAnalysisF32(transientInput, frameSize+overlap, allowWeakTransients)
	}
	transient := transientResult.IsTransient
	weakTransient := transientResult.WeakTransient
	tfEstimate := transientResult.TfEstimate
	tfChannel := transientResult.TfChannel
	toneFreq := transientResult.ToneFreq
	toneishness := transientResult.Toneishness

	// Match libopus line 2033: cap toneishness based on tf_estimate
	// libopus: toneishness = MIN32(toneishness, QCONST32(1.f, 29)-SHL32(tf_estimate, 15))
	// In float: toneishness = min(toneishness, 1.0 - tf_estimate)
	maxToneishness := 1.0 - tfEstimate
	if toneishness > maxToneishness {
		toneishness = maxToneishness
	}

	// For Frame 0, force transient=true to match libopus behavior.
	// libopus detects transient on first frame due to energy increase from silence.
	// Reference: libopus patch_transient_decision() and first frame handling.
	//
	// Do not force first-frame transient here. libopus only applies the
	// tf_estimate=0.2 override through patch_transient_decision() after MDCT
	// analysis, not unconditionally at frame start.

	// Step 5: Initialize range encoder and encode early flags (silence/postfilter).
	// In VBR/CVBR mode libopus starts with the current packet cap, then shrinks
	// after dynalloc and trim once current-frame VBR inputs are known.
	targetBytes := e.computeInitialTargetBytes(frameSize)
	targetBits := targetBytes * 8
	e.frameBits = int32(targetBits)
	defer func() { e.frameBits = 0 }()
	e.clearLastQEXTPayload()

	bufSize := max(targetBytes, 256)
	buf := e.scratch.reBuf
	if len(buf) < bufSize {
		buf = make([]byte, bufSize)
		e.scratch.reBuf = buf
	}
	buf = buf[:bufSize]
	re := &e.scratch.rangeEncoder
	re.Init(buf)
	re.Shrink(uint32(targetBytes))
	e.SetRangeEncoder(re)

	tell := re.Tell()
	if tell == 1 {
		if isSilence {
			re.EncodeBit(1, 15)
			// libopus celt_encode_with_ec does NOT short-circuit a silent frame:
			// after coding the silence flag it pretends the budget is full (no band
			// bits are spent) but still runs the full pipeline, so run_prefilter()
			// shifts prefilter_mem and consec_transient advances via
			// transient_got_disabled. Replicate that state evolution here (the
			// prefilter runs with enabled=false, which only shifts the comb-filter
			// history) so the encoder state carried into the post-silence recovery
			// frame matches libopus and the recovery encode stays byte-exact.
			maxPitchRatio := float32(1.0)
			if e.analysisValid {
				maxPitchRatio = e.analysisMaxPitchRatio
			}
			e.runPrefilter(preemph, frameSize, e.TapsetDecision(), false, tfEstimate, targetBytes, toneFreq, toneishness, maxPitchRatio)
			if !e.IsHybrid() {
				e.updateTemporalVBRSilence(nbBands, codedChannels)
			}
			return e.finishEncodedSilenceFrame(re, frameSize, targetBytes)
		}
		re.EncodeBit(0, 15)
	} else {
		isSilence = false
	}
	start := 0
	if e.IsHybrid() {
		start = max(HybridCELTStartBand, 0)
		if start >= nbBands {
			start = max(nbBands-1, 0)
		}
	}
	prefilterTapset := e.TapsetDecision()
	// Match libopus run_prefilter enable gating.
	enabled := start == 0 &&
		(targetBytes > 12*codedChannels || (e.lfe && targetBytes > 3)) &&
		!e.IsHybrid() &&
		!isSilence &&
		re.Tell()+16 <= targetBits &&
		!e.disablePrefilter
	prevPrefilterPeriod := e.prefilterPeriod
	prevPrefilterGain := e.prefilterGain
	// Match libopus run_prefilter(): scale only when analysis is valid.
	maxPitchRatio := float32(1.0)
	if e.analysisValid {
		maxPitchRatio = e.analysisMaxPitchRatio
	}
	pfResult := e.runPrefilter(preemph, frameSize, prefilterTapset, enabled, tfEstimate, targetBytes, toneFreq, toneishness, maxPitchRatio)
	// Keep stateful prefilter output on float32 precision to match libopus float path.

	e.lastPitchChange = false
	if prevPrefilterPeriod > 0 && (pfResult.gain > 0.4 || prevPrefilterGain > 0.4) {
		upper := int(float32(1.26) * float32(prevPrefilterPeriod))
		lower := int(float32(0.79) * float32(prevPrefilterPeriod))
		e.lastPitchChange = pfResult.pitch > upper || pfResult.pitch < lower
	}

	if !e.IsHybrid() && start == 0 && re.Tell()+16 <= targetBits {
		if !pfResult.on {
			re.EncodeBit(0, 1)
		} else {
			re.EncodeBit(1, 1)
			pitchIndex := pfResult.pitch + 1
			octave := max(ilog32(uint32(pitchIndex))-5, 0)
			re.EncodeUniform(uint32(octave), 6)
			re.EncodeRawBits(uint32(pitchIndex-(16<<octave)), uint(4+octave))
			re.EncodeRawBits(uint32(pfResult.qg), 3)
			re.EncodeICDF(pfResult.tapset, tapsetICDF, 2)
		}
	}

	// Determine short blocks based on bit budget.
	// Match libopus transient_got_disabled cadence: if the transient flag cannot
	// fit, consecutive-transient history still advances even for non-transients.
	transientGotDisabled := false
	shortBlocks := 1
	if lm > 0 && re.Tell()+3 <= targetBits {
		if transient {
			shortBlocks = mode.ShortBlocks
		}
	} else {
		transientGotDisabled = true
		transient = false
		shortBlocks = 1
	}

	// For transients at high complexity, compute long MDCT energies (bandLogE2).
	secondMdct := shortBlocks > 1 && e.complexity >= 8
	var bandLogE2 []celtGLog
	if secondMdct {
		if e.channels == 1 {
			// Use scratch for hist buffer
			hist := e.scratch.leftHist
			if len(hist) < overlap {
				hist = make([]float32, overlap)
				e.scratch.leftHist = hist
			}
			hist = hist[:overlap]
			copy(hist, mdctPrevL[:overlap])
			mdctLong := computeMDCTWithHistoryScratchOverlap(preemph, hist, 1, overlap, &e.scratch)
			applyUpsampleMDCTScaling(mdctLong, upsample)
			// Use bandLogE2 scratch buffer to avoid aliasing with energies
			bandLogE2 = ensureGLogSlice(&e.scratch.bandLogE2, nbBands*codedChannels)
			e.computeBandEnergiesGLogActive(mdctLong, nbBands, frameSize, codedChannels, 1<<lm, bandLogE2)
		} else {
			left, right := deinterleaveStereoScratchF32(preemph, &e.scratch.deintLeft, &e.scratch.deintRight)
			// Use scratch for hist buffers
			leftHist := e.scratch.leftHist
			rightHist := e.scratch.rightHist
			if len(leftHist) < overlap {
				leftHist = make([]float32, overlap)
				e.scratch.leftHist = leftHist
			}
			if len(rightHist) < overlap {
				rightHist = make([]float32, overlap)
				e.scratch.rightHist = rightHist
			}
			leftHist = leftHist[:overlap]
			rightHist = rightHist[:overlap]
			copy(leftHist, mdctPrevL[:overlap])
			copy(rightHist, mdctPrevR[:overlap])
			mdctLeftLong := computeMDCTWithHistoryScratchStereoLOverlap(left, leftHist, 1, overlap, &e.scratch)
			mdctRightLong := computeMDCTWithHistoryScratchStereoROverlap(right, rightHist, 1, overlap, &e.scratch)
			applyUpsampleMDCTScaling(mdctLeftLong, upsample)
			applyUpsampleMDCTScaling(mdctRightLong, upsample)
			mdctLong := e.scratch.mdctCoeffsF32
			if codedChannels == 1 {
				mdctLong = foldStereoMDCTToMonoF32(mdctLong, mdctLeftLong, mdctRightLong)
			} else {
				mdctLongLen := len(mdctLeftLong) + len(mdctRightLong)
				if len(mdctLong) < mdctLongLen {
					mdctLong = make([]float32, mdctLongLen)
					e.scratch.mdctCoeffsF32 = mdctLong
				}
				mdctLong = mdctLong[:mdctLongLen]
				copy(mdctLong, mdctLeftLong)
				copy(mdctLong[len(mdctLeftLong):], mdctRightLong)
			}
			// Use bandLogE2 scratch buffer to avoid aliasing with energies
			bandLogE2 = ensureGLogSlice(&e.scratch.bandLogE2, nbBands*codedChannels)
			e.computeBandEnergiesGLogActive(mdctLong, nbBands, frameSize, codedChannels, 1<<lm, bandLogE2)
		}
		if bandLogE2 != nil {
			offset := celtGLog(0.5 * float32(lm))
			for i := range bandLogE2 {
				bandLogE2[i] += offset
			}
		}
	}

	// Step 5: Compute MDCT with proper overlap handling
	var mdctCoeffs []float32
	var mdctLeft, mdctRight []float32
	if e.channels == 1 {
		hist := ensureFloat32Slice(&e.scratch.leftHist, overlap)
		hist = hist[:overlap]
		copy(hist, mdctPrevL[:overlap])
		mdctCoeffs = computeMDCTWithHistoryScratchOverlap(preemph, hist, shortBlocks, overlap, &e.scratch)
		applyUpsampleMDCTScaling(mdctCoeffs, upsample)
	} else {
		// Stereo: MDCT Left and Right directly - use scratch buffers
		left, right := deinterleaveStereoScratchF32(preemph, &e.scratch.deintLeft, &e.scratch.deintRight)

		leftHistory := ensureFloat32Slice(&e.scratch.leftHist, overlap)
		rightHistory := ensureFloat32Slice(&e.scratch.rightHist, overlap)
		leftHistory = leftHistory[:overlap]
		rightHistory = rightHistory[:overlap]
		copy(leftHistory, mdctPrevL[:overlap])
		copy(rightHistory, mdctPrevR[:overlap])
		// Use overlap-aware MDCT for both channels with scratch buffers
		mdctLeft = computeMDCTWithHistoryScratchStereoLOverlap(left, leftHistory, shortBlocks, overlap, &e.scratch)
		mdctRight = computeMDCTWithHistoryScratchStereoROverlap(right, rightHistory, shortBlocks, overlap, &e.scratch)
		applyUpsampleMDCTScaling(mdctLeft, upsample)
		applyUpsampleMDCTScaling(mdctRight, upsample)

		mdctCoeffs = e.scratch.mdctCoeffsF32
		if codedChannels == 1 {
			mdctCoeffs = foldStereoMDCTToMonoF32(mdctCoeffs, mdctLeft, mdctRight)
			tfChannel = 0
		} else {
			coeffsLen := len(mdctLeft) + len(mdctRight)
			if len(mdctCoeffs) < coeffsLen {
				mdctCoeffs = make([]float32, coeffsLen)
				e.scratch.mdctCoeffsF32 = mdctCoeffs
			}
			mdctCoeffs = mdctCoeffs[:coeffsLen]
			copy(mdctCoeffs[:len(mdctLeft)], mdctLeft)
			copy(mdctCoeffs[len(mdctLeft):], mdctRight)
		}
	}

	// Step 6: Compute band energies
	energies := ensureGLogSlice(&e.scratch.energies, nbBands*codedChannels)
	e.computeBandEnergiesGLogActive(mdctCoeffs, nbBands, frameSize, codedChannels, 1<<lm, energies)
	if e.lfe {
		applyLFEBandLogEClamp(energies, nbBands, codedChannels)
	}
	if !secondMdct {
		bandLogE2 = ensureGLogSlice(&e.scratch.bandLogE2, len(energies))
		copy(bandLogE2, energies)
	}

	// Step 6.5: Patch transient decision based on band energy comparison
	// This is a "second chance" to detect transients that time-domain analysis missed.
	// Particularly important for the first frame where buffer initialization may cause
	// false negatives in transient_analysis().
	// Reference: libopus celt/celt_encoder.c lines 2215-2231
	end := nbBands
	if lm > 0 && !transient && e.complexity >= 5 && !e.IsHybrid() && !e.lfe {
		// Get previous frame's band energies (oldBandE in libopus)
		oldBandE := ensureGLogSlice(&e.scratch.coarseOldStart, len(e.prevEnergy))
		copy(oldBandE, e.prevEnergy)

		spreadOld := ensureGLogSlice(&e.scratch.transientSpreadOld, end)
		if PatchTransientDecisionWithScratch(energies, oldBandE, nbBands, 0, end, codedChannels, spreadOld) {
			// Transient patched! Need to recompute MDCT with short blocks
			transient = true
			shortBlocks = mode.ShortBlocks
			tfEstimate = 0.2 // Match libopus: tf_estimate = QCONST16(.2f,14)

			// Recompute MDCT with short blocks
			if e.channels == 1 {
				hist := ensureFloat32Slice(&e.scratch.leftHist, overlap)
				hist = hist[:overlap]
				copy(hist, mdctPrevL[:overlap])
				mdctCoeffs = computeMDCTWithHistoryScratchOverlap(preemph, hist, shortBlocks, overlap, &e.scratch)
				applyUpsampleMDCTScaling(mdctCoeffs, upsample)
			} else {
				// For stereo, recompute both channels - use scratch buffers
				left, right := deinterleaveStereoScratchF32(preemph, &e.scratch.deintLeft, &e.scratch.deintRight)
				leftHist := e.scratch.leftHist
				rightHist := e.scratch.rightHist
				if len(leftHist) < overlap {
					leftHist = make([]float32, overlap)
					e.scratch.leftHist = leftHist
				}
				if len(rightHist) < overlap {
					rightHist = make([]float32, overlap)
					e.scratch.rightHist = rightHist
				}
				leftHist = leftHist[:overlap]
				rightHist = rightHist[:overlap]
				copy(leftHist, mdctPrevL[:overlap])
				copy(rightHist, mdctPrevR[:overlap])
				mdctLeft = computeMDCTWithHistoryScratchStereoLOverlap(left, leftHist, shortBlocks, overlap, &e.scratch)
				mdctRight = computeMDCTWithHistoryScratchStereoROverlap(right, rightHist, shortBlocks, overlap, &e.scratch)
				applyUpsampleMDCTScaling(mdctLeft, upsample)
				applyUpsampleMDCTScaling(mdctRight, upsample)
				mdctCoeffs = e.scratch.mdctCoeffsF32
				if codedChannels == 1 {
					mdctCoeffs = foldStereoMDCTToMonoF32(mdctCoeffs, mdctLeft, mdctRight)
					tfChannel = 0
				} else {
					coeffsLen := len(mdctLeft) + len(mdctRight)
					if len(mdctCoeffs) < coeffsLen {
						mdctCoeffs = make([]float32, coeffsLen)
						e.scratch.mdctCoeffsF32 = mdctCoeffs
					}
					mdctCoeffs = mdctCoeffs[:coeffsLen]
					copy(mdctCoeffs[:len(mdctLeft)], mdctLeft)
					copy(mdctCoeffs[len(mdctLeft):], mdctRight)
				}
			}

			// Recompute band energies with short block coefficients
			energies = ensureGLogSlice(&e.scratch.energies, nbBands*codedChannels)
			e.computeBandEnergiesGLogActive(mdctCoeffs, nbBands, frameSize, codedChannels, 1<<lm, energies)
			if e.lfe {
				applyLFEBandLogEClamp(energies, nbBands, codedChannels)
			}
			// Compensate for scaling of short vs long MDCTs (libopus adds 0.5*LM to bandLogE2)
			if bandLogE2 != nil {
				offset := celtGLog(0.5 * float32(lm))
				for i := range bandLogE2 {
					bandLogE2[i] += offset
				}
			}
		}
	}

	// Store band log-energies for dynalloc analysis.
	// These are the values passed to DynallocAnalysis.
	e.lastBandLogE = append(e.lastBandLogE[:0], energies...)
	if bandLogE2 != nil {
		e.lastBandLogE2 = append(e.lastBandLogE2[:0], bandLogE2...)
	} else {
		e.lastBandLogE2 = e.lastBandLogE2[:0]
	}

	// Step 9: Encode transient and intra flags (silence/postfilter already encoded)

	// Transient flag: only encode if LM>0 and budget allows
	// Reference: libopus celt_encoder.c line 2063-2069
	// if (LM>0 && ec_tell(enc)+3<=total_bits)
	if lm > 0 && re.Tell()+3 <= targetBits {
		var transientBit int
		if transient {
			transientBit = 1
		}
		re.EncodeBit(transientBit, 3)
	} else if lm > 0 {
		// Budget doesn't allow transient flag, force non-transient
		transientGotDisabled = true
		transient = false
		shortBlocks = 1
	}
	// Step 10: Prepare stereo params (encoded during allocation).
	// Defaults mirror libopus behavior: MS stereo, no intensity.
	intensity := 0
	dualStereo := false

	// Step 11.0: Snapshot previous energies used by dynalloc/coarse decisions.
	prev1LogE := ensureGLogSlice(&e.scratch.prev1LogE, len(e.prevEnergy))
	copy(prev1LogE, e.prevEnergy)

	// dynalloc/spread analysis in libopus uses pre-stabilization energies.
	analysisEnergies := ensureGLogSlice(&e.scratch.analysisEnergies, len(energies))
	copy(analysisEnergies, energies)
	// Step 11: Encode coarse energy
	// Match libopus pre-coarse stabilization:
	// if abs(bandLogE-oldBandE) < 2, bias current energy toward previous quant error.
	// Keep this feedback path in float32 precision to mirror libopus float behavior.
	// Reference: celt_encoder.c before quant_coarse_energy().
	predStride := e.predStride()
	for c := range codedChannels {
		baseState := c * predStride
		baseFrame := c * nbBands
		for band := range nbBands {
			stateIdx := baseState + band
			frameIdx := baseFrame + band
			if frameIdx >= len(energies) || stateIdx >= len(e.energyError) || stateIdx >= len(e.prevEnergy) {
				continue
			}
			oldE := float32(e.prevEnergy[stateIdx])
			curE := float32(energies[frameIdx])
			diff := curE - oldE
			if diff < 0 {
				diff = -diff
			}
			if diff < 2.0 {
				energies[frameIdx] = celtGLog(curE - 0.25*float32(e.energyError[stateIdx]))
			}
		}
	}

	intra := false
	if re.Tell()+3 <= targetBits {
		intra = e.DecideIntraMode(energies, start, nbBands, lm)
		var intraBit int
		if intra {
			intraBit = 1
		}
		re.EncodeBit(intraBit, 3)
	} else {
		intra = false
	}

	var quantizedEnergies []celtGLog
	if start > 0 {
		quantizedEnergies = e.EncodeCoarseEnergyRange(energies, start, nbBands, intra, lm)
	} else {
		quantizedEnergies = e.EncodeCoarseEnergy(energies, nbBands, intra, lm)
	}
	// Step 11.0.5: Normalize bands early for TF analysis
	// TF analysis needs normalized coefficients to determine optimal time-frequency resolution
	var normL, normR []celtNorm
	var bandE []celtEner
	var normBandEScratch []celtEner
	if e.hd96kOverlap > 0 {
		// Native 96 kHz HD mode: band edges are eBands[i]*M with M=1<<LM
		// (compute_band_energies/normalise_bands), not frameSize/120, which would
		// double the per-band bin reach and corrupt the normalised spectrum used
		// by tf_analysis/spreading_decision/alloc_trim and quant_all_bands.
		if codedChannels == 1 {
			normL, bandE = e.normalizeBandsMonoBinMulF32(mdctCoeffs, nbBands, 1<<lm)
			normBandEScratch = bandE
		} else {
			normL, normR, bandE = e.normalizeBandsStereoBinMulF32(mdctLeft, mdctRight, nbBands, 1<<lm)
		}
	} else if codedChannels == 1 {
		normL, bandE = e.NormalizeBandsToArrayMonoWithBandEF32(mdctCoeffs, nbBands, frameSize)
		normBandEScratch = bandE
	} else {
		normL, normR, bandE = e.NormalizeBandsToArrayStereoWithBandEF32(mdctLeft, mdctRight, nbBands, frameSize)
	}
	_ = normBandEScratch
	normLCelt := ensureNormSliceNoClear(&e.scratch.allocTrimNormL, len(normL))
	copy(normLCelt, normL)
	var normRCelt []celtNorm
	if codedChannels == 2 {
		normRCelt = ensureNormSliceNoClear(&e.scratch.allocTrimNormR, len(normR))
		copy(normRCelt, normR)
	}

	// Step 11.0.7: Compute temporal VBR from current frame band energies.
	// Reference: libopus celt_encoder.c lines 2186-2202.
	// Stores the result for next frame's VBR target (one-frame lag is negligible
	// due to the slow IIR coefficient of 0.02).
	if !e.lfe {
		follow := float32(-10.0)
		frameAvg := float32(0.0)
		offset := float32(0.0)
		if shortBlocks > 1 {
			offset = float32(lm) * 0.5
		}
		bandEnd := min(end, nbBands)
		for i := start; i < bandEnd; i++ {
			v := float32(analysisEnergies[i]) - offset
			if follow-1.0 > v {
				follow = follow - 1.0
			} else {
				follow = v
			}
			if codedChannels == 2 && nbBands+i < len(analysisEnergies) {
				v2 := float32(analysisEnergies[nbBands+i]) - offset
				if v2 > follow {
					follow = v2
				}
			}
			frameAvg += follow
		}
		if bandEnd > start {
			frameAvg /= float32(bandEnd - start)
		}
		temporalVBR := frameAvg - float32(e.specAvg)
		if temporalVBR > 3.0 {
			temporalVBR = 3.0
		}
		if temporalVBR < -1.5 {
			temporalVBR = -1.5
		}
		e.specAvg = celtGLog(float32(e.specAvg) + float32(0.02)*temporalVBR)
		e.lastTemporalVBR = celtGLog(temporalVBR)
	}

	// Step 11.1: Compute and encode TF (time-frequency) resolution
	// Note: 'end' was already set earlier during patch_transient_decision
	//
	// effectiveBytes for VBR is the bitrate-derived budget (vbr_rate>>(3+BITRES)),
	// used by dynalloc/TF analysis. libopus celt_encoder.c line 1908/1922.
	effectiveBytes := 0
	if e.vbr {
		baseBits := e.bitrateToBits(frameSize)
		effectiveBytes = baseBits / 8
	} else {
		effectiveBytes = e.cbrPayloadBytes(frameSize)
	}
	// equiv_rate is computed from nbCompressedBytes (the per-frame output-byte cap),
	// NOT effectiveBytes: for VBR they differ (cap can be a byte below the
	// bitrate-derived budget for multi-frame packets), and the stereo intensity
	// hysteresis keys off equiv_rate/1000, so the byte count must match libopus.
	// Reference: libopus celt/celt_encoder.c line 1925.
	equivRateBytes := effectiveBytes
	if e.vbr {
		equivRateBytes = e.vbrMaxPayloadBytes(frameSize)
	}
	equivRate := ComputeEquivRate(equivRateBytes, codedChannels, lm, int(e.targetBitrate))

	// Step 11.0.7: Compute dynalloc analysis for VBR and bit allocation
	// This computes maxDepth, offsets, importance, and spread_weight.
	// The results are stored for next frame's VBR target computation.
	// Reference: libopus celt/celt_encoder.c dynalloc_analysis()
	//
	// libopus defaults to 24 for float input (see celt_encoder.c: st->lsb_depth=24).
	// Our encoder operates on float32 samples, so match the float path.
	lsbDepth := e.LSBDepth()
	// Use scratch buffer for logN
	logN := e.scratch.logN
	if len(logN) < nbBands {
		logN = make([]int16, nbBands)
		e.scratch.logN = logN
	}
	logN = logN[:nbBands]
	for i := 0; i < nbBands && i < len(LogN); i++ {
		logN[i] = int16(LogN[i])
	}
	// Determine VBR mode (match encoder settings)
	isVBR := e.vbr
	isConstrainedVBR := e.constrainedVBR
	bandLogE2Use := analysisEnergies
	if bandLogE2 != nil {
		bandLogE2Use = bandLogE2
	}
	oldBandELen := min(nbBands*codedChannels, len(prev1LogE))
	surroundTrimForAlloc := e.surroundTrim
	var surroundDynalloc []celtGLog
	var surroundDynallocScratch [MaxBands]celtGLog
	if trim, ok := e.computeSurroundDynallocFromMask(nbBands, surroundDynallocScratch[:nbBands]); ok {
		surroundTrimForAlloc = trim
		surroundDynalloc = surroundDynallocScratch[:nbBands]
	}
	dynallocResult := DynallocAnalysisWithScratch(
		analysisEnergies, bandLogE2Use, prev1LogE[:oldBandELen],
		nbBands, start, end, codedChannels, lsbDepth, lm,
		logN,
		effectiveBytes,
		transient, isVBR, isConstrainedVBR, e.lfe,
		toneFreq, toneishness,
		surroundDynalloc,
		e.analysisValid, e.dynallocLeakBoost(),
		&e.dynallocScratch,
	)
	// Store for next frame's VBR computation
	e.lastDynalloc = dynallocResult

	// Step 11.2: Compute and encode TF (time-frequency) resolution.
	// Enable TF analysis when we have enough bits and reasonable complexity.
	// Reference: libopus enable_tf_analysis = effectiveBytes>=15*C && !hybrid && st->complexity>=2 && !st->lfe && toneishness < QCONST32(.98f, 29)
	// Note: libopus does NOT have an LM>0 check here - TF analysis runs for all frame sizes including LM=0
	// CRITICAL: toneishness >= 0.98 disables TF analysis (pure tones use simple fallback)
	enableTFAnalysis := effectiveBytes >= 15*codedChannels && !e.IsHybrid() && e.complexity >= 2 && !e.lfe && toneishness < 0.98

	var tfRes []int32
	var tfSelect int

	if enableTFAnalysis {
		// Use importance from dynalloc analysis for TF decision weighting
		// This weights perceptually important bands higher in the Viterbi search
		// Reference: libopus celt/celt_encoder.c dynalloc_analysis() -> importance
		importance := dynallocResult.Importance

		// Use the normalized coefficients for TF analysis (zero-alloc version).
		// In stereo, match libopus by selecting the channel flagged by transient analysis.
		tfInput := normLCelt
		if codedChannels == 2 && tfChannel == 1 {
			tfInput = normRCelt
		}
		tfRes, tfSelect = TFAnalysisWithScratch(tfInput, len(tfInput), nbBands, transient, lm, opusVal16(tfEstimate), effectiveBytes, importance, &e.tfScratch)

		// Encode TF decisions using the computed values
		TFEncodeWithSelect(re, start, end, transient, tfRes, lm, tfSelect)
	} else {
		// Use default TF settings when analysis is disabled
		tfRes = ensureInt32Slice(&e.scratch.tfRes, nbBands)
		// Zero all entries: ensureInt32Slice reuses memory without zeroing,
		// so stale values (e.g. -1 from TFAnalysis) can survive and cause
		// an index-out-of-range panic in the tfSelectTable lookup below.
		for i := range tfRes {
			tfRes[i] = 0
		}
		if e.IsHybrid() {
			// Match libopus hybrid fixed-TF fallback when TF analysis is disabled.
			tfSelect = FillHybridTFResolution(tfRes, end, transient, weakTransient, allowWeakTransients)
		} else {
			tfSelect = 0
			if transient {
				// For transients without analysis, use tf_res=1 (favor time resolution)
				for i := range nbBands {
					tfRes[i] = 1
				}
			}
		}
		// tf_encode applies the budget guard, tf_select reservation, and the
		// tfSelectTable conversion in one pass, matching libopus tf_encode().
		TFEncodeWithSelect(re, start, end, transient, tfRes, lm, tfSelect)
	}
	// Step 11.2: Compute and encode spread decision
	// Match libopus gating: only encode if there's budget for the decision.
	// Reference: libopus celt_encoder.c line 2302-2345
	normSpread := normLCelt
	if codedChannels == 2 {
		// spreading_decision() expects both channels in one contiguous buffer.
		normSpread = ensureNormSliceNoClear(&e.scratch.normStereo, len(normLCelt)+len(normRCelt))
		copy(normSpread[:len(normLCelt)], normLCelt)
		copy(normSpread[len(normLCelt):], normRCelt)
	}
	var spread int
	if re.Tell()+4 <= targetBits {
		// Match libopus spread control policy:
		// - LFE: fixed normal
		// - Hybrid: fixed none/normal/aggressive based on complexity+transient
		// - CELT-only low-complexity/low-rate/transient shortcuts
		if e.lfe {
			spread = spreadNormal
		} else if e.IsHybrid() {
			if e.complexity == 0 {
				spread = spreadNone
			} else if transient {
				spread = spreadNormal
			} else {
				spread = spreadAggressive
			}
		} else if shortBlocks > 1 || e.complexity < 3 || effectiveBytes < 10*codedChannels {
			if e.complexity == 0 {
				spread = spreadNone
			} else {
				spread = spreadNormal
			}
		} else {
			// For non-transient frames with sufficient bits, analyze the signal
			// to determine optimal spreading.
			// Reference: libopus celt_encoder.c spreading_decision() call with
			// pf_on&&!shortBlocks as updateHF condition.
			updateHF := pfResult.on && shortBlocks == 1
			// Use spread weights from dynalloc_analysis(), matching libopus wiring.
			spreadWeights := dynallocResult.SpreadWeight
			if len(spreadWeights) < nbBands {
				// Defensive fallback for unexpected sizing issues.
				spreadWeights = computeSpreadWeights(analysisEnergies, nbBands, codedChannels, lsbDepth)
			}
			spread = e.SpreadingDecisionWithWeights(normSpread, nbBands, codedChannels, frameSize, updateHF, spreadWeights)
		}
		re.EncodeICDF(spread, spreadICDF, 5)
	} else {
		spread = spreadNormal
	}
	// Step 11.3: Initialize caps for allocation (zero-alloc)
	caps := ensureInt32Slice(&e.scratch.caps, nbBands)
	if pm := e.perMode; pm != nil {
		initCapsIntoMode(caps, nbBands, lm, codedChannels, pm)
	} else {
		initCapsInto(caps, nbBands, lm, codedChannels)
	}

	// Step 11.4: Encode dynamic allocation
	// Reference: libopus celt/celt_encoder.c lines 2356-2389
	//
	// For each band, we encode a series of bits indicating boost allocation:
	// - First bit uses logp=6 (or current dynallocLogp)
	// - Subsequent bits use logp=1 (very likely to continue boosting)
	// - A 0-bit terminates the boost for that band
	// - If offsets[i] == 0, we just encode one 0-bit and move on
	//
	// The offsets come from dynalloc_analysis() and represent how many
	// "quanta" of boost bits to allocate to each band.
	offsets := dynallocResult.Offsets
	if offsets == nil || len(offsets) < nbBands {
		offsets = make([]int32, nbBands)
	}
	if e.lfe && len(offsets) > 0 {
		offsets[0] = int32(min(8, effectiveBytes/3))
	}
	dynallocLogp := 6
	totalBitsQ3ForDynalloc := targetBits << bitRes
	totalBoost := 0
	tellFracDynalloc := re.TellFrac()
	// Store tell at the VBR decision point for next frame's target estimation.
	// In libopus, compute_vbr receives tell at this point and adds it to the target.
	e.lastTellFrac = tellFracDynalloc
	for i := start; i < end; i++ {
		// Compute band width and quanta (how many bits per boost step)
		// Reference: libopus lines 2366-2369
		// width = C*(eBands[i+1]-eBands[i])<<LM
		var width int
		if pm := e.perMode; pm != nil {
			width = codedChannels * (e.modeBandWidth(i) << lm)
		} else {
			width = codedChannels * ScaledBandWidth(i, 120<<lm)
		}
		if width <= 0 {
			width = 1
		}
		// quanta = min(width<<BITRES, max(6<<BITRES, width))
		// This means quanta is between 6 bits and width bits, scaled by BITRES
		innerMax := max(width, 6<<bitRes)
		quanta := min(width<<bitRes, innerMax)

		dynallocLoopLogp := dynallocLogp
		boost := 0
		j := 0

		// Loop encoding boost bits while j < offsets[i]
		for ; tellFracDynalloc+(dynallocLoopLogp<<bitRes) < totalBitsQ3ForDynalloc-totalBoost && boost < int(caps[i]); j++ {
			flag := 0
			if j < int(offsets[i]) {
				flag = 1
			}
			re.EncodeBit(flag, uint(dynallocLoopLogp))
			tellFracDynalloc = re.TellFrac()
			if flag == 0 {
				break
			}
			boost += quanta
			totalBoost += quanta
			dynallocLoopLogp = 1 // After first bit, use logp=1
		}

		// Match libopus: make dynalloc more likely if we encoded any dynalloc bit
		// for this band (even if the first flag was 0).
		if j > 0 {
			if dynallocLogp > 2 {
				dynallocLogp--
			}
		}

		// Update offsets[i] to reflect actual boost applied (for allocation)
		offsets[i] = int32(boost)
	}
	// Step 11.4.5: Decide stereo mode parameters (libopus hysteresis + stereo analysis).
	if codedChannels == 2 {
		// Always use MS for LM=0 (2.5ms), matching libopus.
		if lm != 0 {
			dualStereo = stereoAnalysisDecision(normL, normR, lm, nbBands)
		} else {
			dualStereo = false
		}
		e.intensity = int32(hysteresisDecisionInt(
			equivRate/1000,
			celtIntensityThresholds[:],
			celtIntensityHysteresis[:],
			int(e.intensity),
		))
		if int(e.intensity) < start {
			e.intensity = int32(start)
		}
		if int(e.intensity) > end {
			e.intensity = int32(end)
		}
		intensity = int(e.intensity)
	}

	// Step 11.5: Compute and encode allocation trim (only if budget allows)
	// Reference: libopus celt_encoder.c line 2408-2417
	// The trim value affects bit allocation bias between lower and higher frequency bands.
	allocTrim := 5
	tellForTrim := re.TellFrac()
	if tellForTrim+(6<<bitRes) <= totalBitsQ3ForDynalloc-totalBoost {
		if start > 0 || e.lfe {
			e.lastStereoSaving = 0
			allocTrim = 5
		} else {
			tonalitySlope := opusVal16(0)
			if e.analysisValid {
				tonalitySlope = e.analysisTonalitySlope
			}
			trimNormL := normLCelt
			var trimNormR []celtNorm
			if codedChannels == 2 {
				trimNormR = normRCelt
			}
			trimBandLogE := e.scratch.allocTrimBandLogE[:nbBands*codedChannels]
			for i := range trimBandLogE {
				trimBandLogE[i] = energies[i]
			}
			allocTrim, _ = allocTrimAnalysisDetailed(
				trimNormL,
				trimBandLogE,
				nbBands,
				lm,
				codedChannels,
				trimNormR,
				intensity,
				opusVal16(tfEstimate),
				equivRate,
				surroundTrimForAlloc,
				tonalitySlope,
				e.analysisValid,
			)
			if codedChannels == 2 {
				e.lastStereoSaving = UpdateStereoSaving(e.lastStereoSaving, trimNormL, trimNormR, nbBands, lm, intensity)
			}
		}
		re.EncodeICDF(allocTrim, trimICDF, 7)
	}
	if e.vbr {
		targetBytes = e.computeFinalVBRTargetBytes(frameSize, tfEstimate, e.lastPitchChange, re.TellFrac(), totalBoost, targetBytes)
		targetBits = targetBytes * 8
		e.frameBits = int32(targetBits)
		re.Shrink(uint32(targetBytes))
		if re.Error() != 0 {
			return nil, ErrEncodingFailed
		}
	}
	var qextEnc *rangecoding.Encoder
	var qextExtraBits []int32
	var qextFineBits []int32
	qextPayloadBytes := 0
	if extsupport.QEXT && e.qextActive() && !e.IsHybrid() {
		minAllowed := ((re.TellFrac() + totalBoost + (1 << (bitRes + 3)) - 1) >> (bitRes + 3)) + 2
		// For CBR mode, libopus calls compute_vbr(base=initialTarget, tf2=min(1,2*tf), ...)
		// to estimate the natural VBR target, then passes that as the adjustment pivot.
		// For VBR/CVBR, targetBytes is already the VBR target so no extra computation is needed.
		// Reference: celt/celt_encoder.c lines 2543-2556.
		cbrVBRTargetBytes := 0
		if !e.vbr {
			tf2 := 2 * tfEstimate
			if tf2 > 1.0 {
				tf2 = 1.0
			}
			offsetBytes := (codedChannels * 80000 * frameSize) / (e.celtModeFs() * 8)
			initialQextBytes := max(targetBytes-1275, max(0, (targetBytes-offsetBytes)*4/5))
			overheadQ3 := (40*codedChannels + 20) << bitRes
			baseQ3 := max((targetBytes-initialQextBytes/3)*8<<bitRes-overheadQ3, 0)
			vbrQ3 := e.computeVBRTargetWithBoost(baseQ3, frameSize, tf2, e.lastPitchChange, totalBoost)
			vbrQ3 += re.TellFrac()
			cbrVBRTargetBytes = max((vbrQ3+(1<<(bitRes+2)))>>(bitRes+3), 0)
		}
		mainBytes, payloadBytes, _ := computeQEXTReservation(targetBytes, minAllowed, frameSize, codedChannels, e.celtModeFs(), toneishness, cbrVBRTargetBytes)
		qextPayloadBytes = payloadBytes
		if qextPayloadBytes > 0 && mainBytes > 0 {
			qs := e.scratch.ensureQEXTScratch()
			re.Shrink(uint32(mainBytes))
			if re.Error() != 0 {
				return nil, ErrEncodingFailed
			}

			targetBytes = mainBytes
			targetBits = mainBytes * 8

			qextBuf := qs.buf[:qextPayloadBytes]
			clear(qextBuf)
			qs.encoder.Init(qextBuf)
			qextEnc = &qs.encoder

			qextExtraBits = qs.extraBits[:MaxBands+nbQEXTBands]
			qextFineBits = qs.fineBits[:MaxBands+nbQEXTBands]
			clear(qextExtraBits)
			clear(qextFineBits)
		}
	}

	// Step 12: Compute bit allocation
	bitsUsed := re.TellFrac()
	totalBitsQ3 := (targetBits << bitRes) - bitsUsed - 1
	antiCollapseRsv := 0
	if transient && lm >= 2 && totalBitsQ3 >= (lm+2)<<bitRes {
		antiCollapseRsv = 1 << bitRes
	}
	totalBitsQ3 -= antiCollapseRsv

	signalBandwidth := max(nbBands-1, 0)
	if e.analysisValid {
		minBandwidth := celtMinSignalBandwidth(equivRate, codedChannels)
		signalBandwidth = max(e.analysisBandwidth, minBandwidth)
	}
	if e.lfe {
		signalBandwidth = 1
	}
	var allocResult *AllocationResult
	if e.IsHybrid() {
		allocResult = e.ComputeAllocationHybridScratch(
			re,
			totalBitsQ3,
			nbBands,
			caps,
			offsets,
			allocTrim,
			intensity,
			dualStereo,
			lm,
			int(e.lastCodedBands),
			signalBandwidth,
		)
	} else {
		allocResult = e.computeAllocationScratch(
			re,
			totalBitsQ3,
			nbBands,
			caps,
			offsets,
			allocTrim,
			intensity,
			dualStereo,
			lm,
			int(e.lastCodedBands),
			signalBandwidth,
		)
	}
	if e.lastCodedBands != 0 {
		lastCodedBands := int(e.lastCodedBands)
		e.lastCodedBands = int32(min(lastCodedBands+1, max(lastCodedBands-1, allocResult.CodedBands)))
	} else {
		e.lastCodedBands = int32(allocResult.CodedBands)
	}
	if codedChannels == 2 {
		e.intensity = int32(allocResult.Intensity)
		intensity = allocResult.Intensity
	}
	// Keep CELT allocation bandwidth gating driven only by explicit external
	// analysis input (SetAnalysisBandwidth), matching libopus behavior where
	// st->analysis.valid is supplied by the top-level analysis pipeline.

	// Step 13: Encode fine energy
	// Keep CELT fine/final energy refinement on the in-place residual state used
	// by coarse quantization. This mirrors libopus quant_fine_energy() ->
	// quant_energy_finalise() operating on the same error[] buffer.
	coarseResidual := e.scratch.coarseError
	if len(coarseResidual) >= nbBands*codedChannels {
		coarseResidual = coarseResidual[:nbBands*codedChannels]
		if start > 0 {
			e.EncodeFineEnergyRangeFromError(quantizedEnergies, start, nbBands, allocResult.FineBits)
		} else {
			e.encodeFineEnergyFromError(quantizedEnergies, nbBands, allocResult.FineBits, coarseResidual)
		}
	} else {
		// Defensive fallback for unexpected sizing issues.
		if start > 0 {
			e.EncodeFineEnergyRange(energies, quantizedEnergies, start, nbBands, allocResult.FineBits)
		} else {
			e.EncodeFineEnergy(energies, quantizedEnergies, nbBands, allocResult.FineBits)
		}
	}
	var qextCfg qextModeConfig
	qextActive := false
	qextEnd := 0
	var qextBandE []celtEner
	var qextBandLogE []celtGLog
	var qextQuantized []celtGLog
	var qextError []celtGLog
	var qextNormL []celtNorm
	var qextNormR []celtNorm
	if extsupport.QEXT && qextEnc != nil {
		if cfg, ok := computeQEXTModeConfig(int(e.sampleRate), qextShortMDCTSize(frameSize)); ok && end == nbBands {
			qextCfg = cfg
			qextEnd = qextCfg.EffBands
			qextActive = qextEnd > 0
		}
		if qextActive {
			qs := e.scratch.ensureQEXTScratch()
			qextBandLogE = qs.bandLogE[:qextEnd*codedChannels]
			qextBandE = qs.bandE[:qextEnd*codedChannels]
			clear(qextBandLogE)
			clear(qextBandE)
			if codedChannels == 1 {
				computeQEXTBandLogEF32Into(mdctCoeffs, &qextCfg, qextEnd, lm, qextBandE, qextBandLogE)
				qextNormL = qs.normL[:frameSize]
				normalizeQEXTBandsF32Into(mdctCoeffs, &qextCfg, qextEnd, lm, qextBandE, qextNormL)
			} else {
				computeQEXTBandLogEF32Into(mdctLeft, &qextCfg, qextEnd, lm, qextBandE[:qextEnd], qextBandLogE[:qextEnd])
				computeQEXTBandLogEF32Into(mdctRight, &qextCfg, qextEnd, lm, qextBandE[qextEnd:], qextBandLogE[qextEnd:])
				qextNormL = qs.normL[:frameSize]
				qextNormR = qs.normR[:frameSize]
				normalizeQEXTBandsF32Into(mdctLeft, &qextCfg, qextEnd, lm, qextBandE[:qextEnd], qextNormL)
				normalizeQEXTBandsF32Into(mdctRight, &qextCfg, qextEnd, lm, qextBandE[qextEnd:], qextNormR)
			}

			hdr := qextHeader{
				EndBands: qextEnd,
			}
			if codedChannels == 2 {
				hdr.Intensity = qextEnd
				hdr.DualStereo = allocResult.DualStereo
			}
			encodeQEXTHeader(qextEnc, codedChannels, hdr)

			qextQuantized = qs.quantized[:qextEnd*codedChannels]
			qextError = qs.qerr[:qextEnd*codedChannels]
			qextOldBandE := qs.oldBandE[:MaxBands*codedChannels]
			clear(qextQuantized)
			clear(qextError)
			clear(qextOldBandE)
			var qextDelayedIntra float32
			e.encodeQEXTCoarseEnergyWithEncoder(qextEnc, qextBandLogE, qextEnd, lm, qextPayloadBytes, qextOldBandE, qextQuantized, qextError, &qextDelayedIntra)
		}

		qextBitsQ3 := max((qextEnc.StorageBits()<<bitRes)-re.TellFrac()-1, 0)
		computeQEXTExtraAllocationEncode(
			start,
			end,
			qextEnd,
			qextBitsQ3,
			codedChannels,
			lm,
			analysisEnergies,
			qextBandLogE,
			func() *qextModeConfig {
				if !qextActive {
					return nil
				}
				return &qextCfg
			}(),
			toneFreq,
			toneishness,
			qextEnc,
			qextExtraBits,
			qextFineBits,
		)
		if len(coarseResidual) >= nbBands*codedChannels {
			if start > 0 {
				e.encodeFineEnergyRangeFromErrorWithEncoder(qextEnc, quantizedEnergies, start, nbBands, qextFineBits)
			} else {
				e.encodeFineEnergyFromErrorWithPrevWithEncoder(qextEnc, quantizedEnergies, nbBands, allocResult.FineBits, qextFineBits, coarseResidual)
			}
		}
	}

	// Note: normL/normR and tfRes were already computed before encoding spread
	// to ensure the same normalized coefficients are used for analysis and quantization

	// Step 14: Encode bands (quant_all_bands)
	// The tapset decision is computed during spreading_decision and used here
	// for state tracking. While tapset primarily affects the comb filter
	// (prefilter/postfilter), it's tracked in the quantization context for
	// encoder state consistency and future prefilter integration.
	totalBitsAllQ3 := (targetBits << bitRes) - antiCollapseRsv
	dualStereoVal := 0
	if allocResult.DualStereo {
		dualStereoVal = 1
	}
	tapset := e.TapsetDecision()
	if pm := e.perMode; pm != nil {
		quantAllBandsEncodeScratchWithMode(
			re,
			codedChannels,
			frameSize,
			lm,
			start,
			end,
			normL,
			normR,
			allocResult.BandBits,
			shortBlocks,
			spread,
			tapset,
			dualStereoVal,
			allocResult.Intensity,
			tfRes,
			totalBitsAllQ3,
			allocResult.Balance,
			allocResult.CodedBands,
			e.phaseInversionDisabled,
			&e.rng,
			int(e.complexity),
			bandE,
			qextEnc,
			qextExtraBits,
			&e.bandEncScratch,
			pm.eBands,
			pm.logN,
			pm.cacheIndex,
			pm.cacheBits,
		)
	} else {
		quantAllBandsEncodeScratch(
			re,
			codedChannels,
			frameSize,
			lm,
			start,
			end,
			normL,
			normR,
			allocResult.BandBits,
			shortBlocks,
			spread, // Spreading parameter for PVQ rotation
			tapset, // Tapset decision for comb filter taper (tracked for state)
			dualStereoVal,
			allocResult.Intensity,
			tfRes,
			totalBitsAllQ3,
			allocResult.Balance,
			allocResult.CodedBands,
			e.phaseInversionDisabled,
			&e.rng,
			int(e.complexity),
			bandE,
			qextEnc,
			qextExtraBits,
			&e.bandEncScratch,
		)
	}
	if qextActive {
		qextBandBits := qextFineBits[MaxBands : MaxBands+qextEnd]
		e.encodeFineEnergyFromErrorWithEncoder(qextEnc, qextQuantized, qextEnd, qextBandBits, qextError)

		qextDualStereoVal := 0
		if allocResult.DualStereo {
			qextDualStereoVal = 1
		}
		qextTotalBitsQ3 := qextPayloadBytes * (8 << bitRes)
		qextBalance := qextTotalBitsQ3 - qextEnc.TellFrac()
		fineQ3 := 0
		if qextEnd > 1 {
			fineQ3 = codedChannels * int(qextBandBits[1]<<bitRes)
		}
		for i := 0; i < qextEnd; i++ {
			qextBalance -= int(qextExtraBits[MaxBands+i])
			qextBalance -= fineQ3
		}
		// Pass the signed ext_balance to quant_all_bands (no clamp at 0),
		// mirroring the decode-side QEXT path (decodeQEXTBands).
		// Match libopus: extra-band quant_all_bands() still receives a real
		// secondary coder context, but that nested coder is a zero-sized dummy
		// and the per-band extension budgets are all zero.
		zeroTFRes := ensureInt32Slice(&e.scratch.tfRes, qextEnd)
		clear(zeroTFRes)
		var dummyEnc rangecoding.Encoder
		dummyEnc.Init(nil)
		quantAllBandsEncodeScratchWithMode(
			qextEnc,
			codedChannels,
			frameSize,
			lm,
			0,
			qextEnd,
			qextNormL,
			qextNormR,
			qextExtraBits[MaxBands:MaxBands+qextEnd],
			shortBlocks,
			spread,
			tapset,
			qextDualStereoVal,
			qextEnd,
			zeroTFRes,
			qextTotalBitsQ3,
			qextBalance,
			qextEnd,
			e.phaseInversionDisabled,
			&e.rng,
			int(e.complexity),
			qextBandE,
			&dummyEnc,
			zeroTFRes,
			&e.bandEncScratch,
			qextCfg.EBands,
			qextCfg.LogN,
			qextCfg.CacheIndex,
			qextCfg.CacheBits,
		)
	}

	// Step 14.5: Encode anti-collapse flag if reserved
	if antiCollapseRsv > 0 {
		antiCollapseOn := 0
		if e.consecTransient < 2 {
			antiCollapseOn = 1
		}
		re.EncodeRawBits(uint32(antiCollapseOn), 1)
	}
	// Step 14.6: Encode energy finalization bits (leftover budget)
	bitsLeft := max(targetBits-re.Tell(), 0)
	if len(coarseResidual) >= nbBands*codedChannels {
		if start > 0 {
			e.EncodeEnergyFinaliseRangeFromError(quantizedEnergies, start, nbBands, allocResult.FineBits, allocResult.FinePriority, bitsLeft)
		} else {
			e.encodeEnergyFinaliseFromError(quantizedEnergies, nbBands, allocResult.FineBits, allocResult.FinePriority, bitsLeft, coarseResidual)
		}
	} else {
		if start > 0 {
			e.EncodeEnergyFinaliseRange(energies, quantizedEnergies, start, nbBands, allocResult.FineBits, allocResult.FinePriority, bitsLeft)
		} else {
			e.EncodeEnergyFinalise(energies, quantizedEnergies, nbBands, allocResult.FineBits, allocResult.FinePriority, bitsLeft)
		}
	}
	// Match libopus energyError update timing and range:
	// store post-finalise error[] residual, clipped to [-0.5, 0.5], for
	// next-frame stabilization.
	// Keep this in float32 precision to mirror libopus float behavior.
	// Reference: celt_encoder.c after quant_energy_finalise().
	for i := range e.energyError {
		e.energyError[i] = 0
	}
	for c := range codedChannels {
		baseState := c * e.predStride()
		baseFrame := c * nbBands
		for band := range nbBands {
			stateIdx := baseState + band
			if stateIdx >= len(e.energyError) {
				continue
			}
			frameIdx := baseFrame + band
			if frameIdx >= len(energies) || frameIdx >= len(quantizedEnergies) {
				continue
			}
			err := energies[frameIdx] - quantizedEnergies[frameIdx]
			if frameIdx < len(coarseResidual) {
				err = coarseResidual[frameIdx]
			}
			if err < -0.5 {
				err = -0.5
			} else if err > 0.5 {
				err = 0.5
			}
			e.energyError[stateIdx] = celtGLog(err)
		}
	}

	// Step 15: Finalize and update state
	// Capture final range BEFORE Done(), matching libopus celt_encoder.c:2809
	// This is critical for verification - the final range must be captured before
	// ec_enc_done() flushes the remaining bytes.
	e.rng = re.Range()
	bytes := re.Done()
	if extsupport.QEXT && qextEnc != nil {
		qextEnc.Done()
		e.setLastQEXTPayload(qextEnc.Buffer()[:qextEnc.Storage()])
		if e.lastQEXTPayloadNonEmpty() {
			e.rng ^= qextEnc.Range()
		}
	}
	e.setPrevEnergyWithPrevCoded(prev1LogE, quantizedEnergies, nbBands, codedChannels)
	e.IncrementFrameCount()
	if transient || transientGotDisabled {
		e.consecTransient++
	} else {
		e.consecTransient = 0
	}

	return bytes, nil
}

func foldStereoMDCTToMonoF32(dst, left, right []float32) []float32 {
	n := min(len(right), len(left))
	if len(dst) < n {
		dst = make([]float32, n)
	}
	dst = dst[:n]
	for i := 0; i < n; i++ {
		dst[i] = 0.5*left[i] + 0.5*right[i]
	}
	return dst
}

func (e *Encoder) setPrevEnergyWithPrevCoded(prev []celtGLog, energies []celtGLog, nbBands, codedChannels int) {
	if len(prev) == len(e.prevEnergy2) {
		copy(e.prevEnergy2, prev)
	} else {
		copy(e.prevEnergy2, e.prevEnergy)
	}
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if nbBands < 0 {
		nbBands = 0
	}
	if codedChannels < 1 {
		codedChannels = 1
	}
	if codedChannels > int(e.channels) {
		codedChannels = int(e.channels)
	}
	predStride := e.predStride()
	for c := 0; c < codedChannels; c++ {
		for band := 0; band < nbBands; band++ {
			src := c*nbBands + band
			dst := c*predStride + band
			if src < len(energies) && dst < len(e.prevEnergy) {
				e.prevEnergy[dst] = energies[src]
			}
		}
	}
	if e.channels == 2 && codedChannels == 1 {
		for band := 0; band < nbBands; band++ {
			e.prevEnergy[predStride+band] = e.prevEnergy[band]
		}
	}
}

// ComputeMDCTWithHistory computes MDCT using a history buffer for overlap.
// samples: current frame samples
// history: buffer containing previous frame's tail (will be updated with current frame's tail)
// shortBlocks: number of short blocks for transient mode
func ComputeMDCTWithHistory(samples, history []float32, shortBlocks int) []float32 {
	if len(samples) == 0 {
		return nil
	}

	overlap := min(Overlap, len(samples))
	input := make([]float32, len(samples)+overlap)

	// Copy history overlap into the head of the input buffer.
	if overlap > 0 && len(history) > 0 {
		if len(history) >= overlap {
			copy(input[:overlap], history[len(history)-overlap:])
		} else {
			copy(input[overlap-len(history):overlap], history)
		}
	}

	// Append current frame samples after the overlap.
	copy(input[overlap:], samples)

	// Update history with the current frame tail (overlap samples).
	if overlap > 0 && len(history) > 0 {
		if len(history) >= overlap {
			copy(history, samples[len(samples)-overlap:])
		} else {
			copy(history, samples[len(samples)-len(history):])
		}
	}

	if shortBlocks > 1 {
		return MDCTShort(input, shortBlocks)
	}
	return MDCT(input)
}

// computeMDCTWithHistoryScratch computes MDCT using a history buffer with scratch buffers.
// This is the zero-allocation version that uses pre-allocated buffers.
func computeMDCTWithHistoryScratch(samples, history []float32, shortBlocks int, scratch *encoderScratch) []float32 {
	return computeMDCTWithHistoryScratchOverlap(samples, history, shortBlocks, Overlap, scratch)
}

// computeMDCTWithHistoryScratchOverlap is the overlap-parametric form of
// computeMDCTWithHistoryScratch. The 48 kHz path passes overlap=Overlap and is
// byte-identical; the native 96 kHz HD mode passes overlap=240.
func computeMDCTWithHistoryScratchOverlap(samples, history []float32, shortBlocks, overlap int, scratch *encoderScratch) []float32 {
	if len(samples) == 0 {
		return nil
	}

	if overlap > len(samples) {
		overlap = len(samples)
	}

	// Use scratch input buffer
	inputLen := len(samples) + overlap
	input := scratch.mdctInput
	if len(input) < inputLen {
		input = make([]float32, inputLen)
		scratch.mdctInput = input
	}
	input = input[:inputLen]

	// Copy history overlap into the head of the input buffer.
	if overlap > 0 && len(history) > 0 {
		if len(history) >= overlap {
			copy(input[:overlap], history[len(history)-overlap:])
		} else {
			copy(input[overlap-len(history):overlap], history)
		}
	}

	// Append current frame samples after the overlap.
	copy(input[overlap:], samples)

	// Update history with the current frame tail (overlap samples).
	if overlap > 0 && len(history) > 0 {
		if len(history) >= overlap {
			copy(history, samples[len(samples)-overlap:])
		} else {
			copy(history, samples[len(samples)-len(history):])
		}
	}

	if shortBlocks > 1 {
		return mdctForwardShortOverlapScratchF32Coeffs(input, overlap, shortBlocks, scratch)
	}
	return mdctForwardOverlapScratchF32Coeffs(input, overlap, scratch)
}

// computeMDCTWithHistoryScratchStereoL computes MDCT for the left channel with scratch buffers.
// Uses mdctLeft scratch buffer for output.
func computeMDCTWithHistoryScratchStereoL(samples, history []float32, shortBlocks int, scratch *encoderScratch) []float32 {
	return computeMDCTWithHistoryScratchStereoLOverlap(samples, history, shortBlocks, Overlap, scratch)
}

func computeMDCTWithHistoryScratchStereoLOverlap(samples, history []float32, shortBlocks, overlap int, scratch *encoderScratch) []float32 {
	if len(samples) == 0 {
		return nil
	}

	if overlap > len(samples) {
		overlap = len(samples)
	}

	// Use scratch input buffer (shared, but reused between L and R sequentially)
	inputLen := len(samples) + overlap
	input := scratch.mdctInput
	if len(input) < inputLen {
		input = make([]float32, inputLen)
		scratch.mdctInput = input
	}
	input = input[:inputLen]

	// Copy history overlap into the head of the input buffer.
	if overlap > 0 && len(history) > 0 {
		if len(history) >= overlap {
			copy(input[:overlap], history[len(history)-overlap:])
		} else {
			copy(input[overlap-len(history):overlap], history)
		}
	}

	// Append current frame samples after the overlap.
	copy(input[overlap:], samples)

	// Update history with the current frame tail (overlap samples).
	if overlap > 0 && len(history) > 0 {
		if len(history) >= overlap {
			copy(history, samples[len(samples)-overlap:])
		} else {
			copy(history, samples[len(samples)-len(history):])
		}
	}

	// Use mdctLeft for output
	frameSize := len(samples)
	coeffs := ensureFloat32Slice(&scratch.mdctLeftF32, frameSize)

	if shortBlocks > 1 {
		return mdctForwardShortOverlapScratchIntoF32Coeffs(input, overlap, shortBlocks, coeffs[:frameSize], scratch)
	}
	mdctForwardOverlapF32Scratch(input, overlap, coeffs[:frameSize],
		scratch.mdctF, scratch.mdctFFTIn, scratch.mdctFFTOut, scratch.mdctFFTTmp)
	return coeffs[:frameSize]
}

// computeMDCTWithHistoryScratchStereoR computes MDCT for the right channel with scratch buffers.
// Uses mdctRight scratch buffer for output.
func computeMDCTWithHistoryScratchStereoR(samples, history []float32, shortBlocks int, scratch *encoderScratch) []float32 {
	return computeMDCTWithHistoryScratchStereoROverlap(samples, history, shortBlocks, Overlap, scratch)
}

func computeMDCTWithHistoryScratchStereoROverlap(samples, history []float32, shortBlocks, overlap int, scratch *encoderScratch) []float32 {
	if len(samples) == 0 {
		return nil
	}

	if overlap > len(samples) {
		overlap = len(samples)
	}

	// Use scratch input buffer (shared, but reused between L and R sequentially)
	inputLen := len(samples) + overlap
	input := scratch.mdctInput
	if len(input) < inputLen {
		input = make([]float32, inputLen)
		scratch.mdctInput = input
	}
	input = input[:inputLen]

	// Copy history overlap into the head of the input buffer.
	if overlap > 0 && len(history) > 0 {
		if len(history) >= overlap {
			copy(input[:overlap], history[len(history)-overlap:])
		} else {
			copy(input[overlap-len(history):overlap], history)
		}
	}

	// Append current frame samples after the overlap.
	copy(input[overlap:], samples)

	// Update history with the current frame tail (overlap samples).
	if overlap > 0 && len(history) > 0 {
		if len(history) >= overlap {
			copy(history, samples[len(samples)-overlap:])
		} else {
			copy(history, samples[len(samples)-len(history):])
		}
	}

	// Use mdctRight for output
	frameSize := len(samples)
	coeffs := ensureFloat32Slice(&scratch.mdctRightF32, frameSize)

	if shortBlocks > 1 {
		return mdctForwardShortOverlapScratchIntoF32Coeffs(input, overlap, shortBlocks, coeffs[:frameSize], scratch)
	}
	mdctForwardOverlapF32Scratch(input, overlap, coeffs[:frameSize],
		scratch.mdctF, scratch.mdctFFTIn, scratch.mdctFFTOut, scratch.mdctFFTTmp)
	return coeffs[:frameSize]
}

// updateTemporalVBRSilence advances the temporal-VBR running average (st->spec_avg)
// for a silent frame. libopus does NOT short-circuit a silent frame: it runs the
// full pipeline (compute_mdcts on the ~zero input, compute_band_energies, ...,
// the temporal-VBR block at celt_encoder.c lines 2186-2202) so spec_avg keeps
// decaying toward the silence floor while DTX/silence frames are emitted. The
// gopus silence fast path returns early, so this reproduces just that spec_avg
// update on the silence-floor band energies, keeping the value carried into the
// post-silence recovery frame's VBR target bit-exact with libopus. start is 0
// (CELT-only) and a silent frame is never transient, so offset is 0.
func (e *Encoder) updateTemporalVBRSilence(nbBands, codedChannels int) {
	if e.lfe || nbBands <= 0 {
		return
	}
	bandEnd := nbBands
	silenceFreq := ensureFloat32Slice(&e.scratch.silenceFreqVBR, nbBands*codedChannels)
	for i := range silenceFreq {
		silenceFreq[i] = 0
	}
	silenceE := ensureGLogSlice(&e.scratch.silenceEnergyVBR, nbBands*codedChannels)
	e.computeBandEnergiesGLogActive(silenceFreq, nbBands, nbBands, codedChannels, 1, silenceE)

	follow := float32(-10.0)
	frameAvg := float32(0.0)
	for i := range bandEnd {
		v := float32(silenceE[i])
		if follow-1.0 > v {
			follow = follow - 1.0
		} else {
			follow = v
		}
		if codedChannels == 2 && nbBands+i < len(silenceE) {
			v2 := float32(silenceE[nbBands+i])
			if v2 > follow {
				follow = v2
			}
		}
		frameAvg += follow
	}
	if bandEnd > 0 {
		frameAvg /= float32(bandEnd)
	}
	temporalVBR := frameAvg - float32(e.specAvg)
	if temporalVBR > 3.0 {
		temporalVBR = 3.0
	}
	if temporalVBR < -1.5 {
		temporalVBR = -1.5
	}
	e.specAvg = celtGLog(float32(e.specAvg) + float32(0.02)*temporalVBR)
	e.lastTemporalVBR = celtGLog(temporalVBR)
}

func (e *Encoder) finishEncodedSilenceFrame(re *rangecoding.Encoder, frameSize, targetBytes int) ([]byte, error) {
	if e.vbr && e.targetBitrate != opusBitrateMax {
		targetBytes = e.applyVBRSilenceTarget(frameSize, targetBytes)
		re.Shrink(uint32(targetBytes))
		if re.Error() != 0 {
			return nil, ErrEncodingFailed
		}
		e.frameBits = int32(targetBytes * 8)
	}
	e.lastTellFrac = targetBytes * 8 << bitRes

	prev1LogE := e.scratch.prev1LogE
	if len(prev1LogE) < len(e.prevEnergy) {
		prev1LogE = make([]celtGLog, len(e.prevEnergy))
		e.scratch.prev1LogE = prev1LogE
	}
	prev1LogE = prev1LogE[:len(e.prevEnergy)]
	copy(prev1LogE, e.prevEnergy)

	silenceE := ensureGLogSlice(&e.scratch.coarseOldStart, len(e.prevEnergy))
	for i := range silenceE {
		silenceE[i] = -28.0
	}
	e.setPrevEnergyWithPrevGLog(prev1LogE, silenceE)
	for i := range e.energyError {
		e.energyError[i] = 0
	}
	e.lastDynalloc = DynallocResult{}
	// libopus codes a silent frame with the budget pretended full, so the
	// transient flag never fits: transient_got_disabled=1 and consec_transient
	// advances (celt_encoder.c: "if (isTransient || transient_got_disabled)
	// st->consec_transient++"). The shortcut previously reset it to 0, which
	// diverged the post-silence recovery frame's transient/anti-collapse state.
	e.consecTransient++
	// libopus runs clt_compute_allocation on the silent frame too (with the
	// 2-byte budget), which yields codedBands==1, then slews lastCodedBands toward
	// it by at most ±1 (celt_encoder.c: lastCodedBands = IMIN(lcb+1, IMAX(lcb-1,
	// codedBands))). Over a silence run this decays lastCodedBands down to 1; the
	// gopus silence fast path skips the allocator, so apply the same slew with the
	// silent-frame codedBands==1 to keep the value (which feeds the post-silence
	// recovery frame's compute_vbr coded_bins) bit-exact with libopus.
	const silenceCodedBands = 1
	if e.lastCodedBands != 0 {
		lcb := int(e.lastCodedBands)
		e.lastCodedBands = int32(min(lcb+1, max(lcb-1, silenceCodedBands)))
	} else {
		e.lastCodedBands = silenceCodedBands
	}
	e.IncrementFrameCount()
	e.rng = re.Range()
	return re.Done(), nil
}

func (e *Encoder) applyVBRSilenceTarget(frameSize, targetBytes int) int {
	if targetBytes > 2 {
		targetBytes = 2
	}
	vbrRateQ3 := e.bitrateToBits(frameSize) << bitRes
	if vbrRateQ3 <= 0 {
		return targetBytes
	}
	alpha := float32(0.001)
	if e.vbrCount < 970 {
		e.vbrCount++
		alpha = 1.0 / float32(e.vbrCount+20)
	}
	if e.constrainedVBR {
		targetQ3 := 2 * 8 << bitRes
		e.vbrReservoir = int32(int(e.vbrReservoir) + targetQ3 - vbrRateQ3)
		driftDelta := -int(e.vbrOffset) - int(e.vbrDrift)
		e.vbrDrift += int32(alpha * float32(driftDelta))
		e.vbrOffset = -e.vbrDrift
		if e.vbrReservoir < 0 {
			e.vbrReservoir = 0
		}
	}
	return targetBytes
}

// EncodeFrameWithOptions encodes a frame with additional control options.
func (e *Encoder) EncodeFrameWithOptions(pcm []float32, frameSize int, opts EncodeOptions) ([]byte, error) {
	// Apply options
	if opts.ForceIntra {
		// Temporarily set frame count to 0 for intra mode
		savedCount := e.frameCount
		e.frameCount = 0
		defer func() { e.frameCount = savedCount }()
	}

	return e.EncodeFrame(pcm, frameSize)
}

// EncodeOptions provides encoding control options.
type EncodeOptions struct {
	ForceIntra bool // Force intra mode (no inter-frame prediction)
	Bitrate    int  // Target bitrate in bits per second (0 = default)
}

// EncodeStereoFrame encodes a stereo frame from separate L/R channels.
// left: left channel samples
// right: right channel samples
// frameSize: 120, 240, 480, or 960 samples per channel
func (e *Encoder) EncodeStereoFrame(left, right []float32, frameSize int) ([]byte, error) {
	if e.channels != 2 {
		return nil, errors.New("celt: encoder not configured for stereo")
	}

	if len(left) != frameSize || len(right) != frameSize {
		return nil, ErrInvalidInputLength
	}

	// Interleave for standard encoding path
	interleaved := InterleaveStereoF32(left, right)
	return e.EncodeFrame(interleaved, frameSize)
}

// bitrateToBits returns the base target bits from bitrate and frame size.
// This mirrors libopus bitrate_to_bits() for CELT frames.
func (e *Encoder) bitrateToBits(frameSize int) int {
	bitrate := int(e.targetBitrate)
	if bitrate <= 0 {
		if e.channels == 2 {
			bitrate = 128000
		} else {
			bitrate = 64000
		}
	}
	if bitrate < 6000 {
		bitrate = 6000
	}
	if bitrate > 510000 {
		bitrate = 510000
	}
	return bitrate * frameSize / e.celtModeFs()
}

// celtModeFs returns the CELT mode sample rate used by the bitrate<->bytes
// conversions (libopus mode->Fs). It is 48000 for the standard modes and 96000
// for the native 96 kHz HD mode. The 48 kHz path is unchanged.
func (e *Encoder) celtModeFs() int {
	if e.hd96kOverlap > 0 && e.sampleRate == 96000 {
		return 96000
	}
	return 48000
}

// cbrPayloadBytes computes the CBR payload size (excluding TOC).
// This matches libopus's CBR byte formula and subtracts the TOC byte.
func (e *Encoder) cbrPayloadBytes(frameSize int) int {
	fs := e.celtModeFs()
	bitrate := int(e.targetBitrate)
	if bitrate == opusBitrateMax {
		packetSizeCap := 1275
		if extsupport.QEXT && e.qextActive() && !e.hybrid {
			packetSizeCap = qextPacketSizeCap
		}
		payload := packetSizeCap - 1
		if e.maxPayloadBytes > 0 && payload > int(e.maxPayloadBytes) {
			payload = int(e.maxPayloadBytes)
		}
		if payload < 0 {
			payload = 0
		}
		return payload
	}
	if bitrate <= 0 {
		if e.channels == 2 {
			bitrate = 128000
		} else {
			bitrate = 64000
		}
	}
	if bitrate < 6000 {
		bitrate = 6000
	}
	nbCompressed := max((bitrate*frameSize+4*fs)/(8*fs), 2)
	packetSizeCap := 1275
	if extsupport.QEXT && e.qextActive() && !e.hybrid {
		packetSizeCap = qextPacketSizeCap
	}
	if nbCompressed > packetSizeCap {
		nbCompressed = packetSizeCap
	}
	payload := max(
		// subtract TOC byte
		nbCompressed-1, 0)
	if e.maxPayloadBytes > 0 && payload > int(e.maxPayloadBytes) {
		payload = int(e.maxPayloadBytes)
	}
	return payload
}

func (e *Encoder) vbrMaxPayloadBytes(frameSize int) int {
	mode := e.modeConfig(frameSize)
	lm := mode.LM
	packetSizeCap := 1275
	if extsupport.QEXT && e.qextActive() && !e.hybrid {
		packetSizeCap = qextPacketSizeCap
	}
	maxBytes := max((packetSizeCap>>(3-lm))-1, 2)
	if e.maxPayloadBytes > 0 && maxBytes > int(e.maxPayloadBytes) {
		maxBytes = int(e.maxPayloadBytes)
	}
	return maxBytes
}

func (e *Encoder) computeInitialTargetBytes(frameSize int) int {
	if !e.vbr {
		return e.cbrPayloadBytes(frameSize)
	}
	targetBytes := e.vbrMaxPayloadBytes(frameSize)
	if e.constrainedVBR {
		vbrRateQ3 := e.bitrateToBits(frameSize) << bitRes
		boundScale := e.constrainedVBRBoundScale
		if boundScale < 0 {
			boundScale = 0
		} else if boundScale > 1 {
			boundScale = 1
		}
		vbrBoundQ3 := int(float32(vbrRateQ3) * boundScale)
		maxAllowedBytes := max((vbrRateQ3+vbrBoundQ3-int(e.vbrReservoir))>>(bitRes+3), 2)
		if targetBytes > maxAllowedBytes {
			targetBytes = maxAllowedBytes
		}
	}
	if targetBytes < 2 {
		targetBytes = 2
	}
	return targetBytes
}

func (e *Encoder) computeFinalVBRTargetBytes(frameSize int, tfEstimate float32, pitchChange bool, tellFrac, totalBoost, limitBytes int) int {
	mode := e.modeConfig(frameSize)
	lm := mode.LM
	lmDiff := max(3-lm, 0)

	vbrRateQ3 := e.bitrateToBits(frameSize) << bitRes
	channels := e.codedChannels()
	overheadQ3 := (40*channels + 20) << bitRes
	if e.hybrid {
		overheadQ3 = (9*channels + 4) << bitRes
	}
	baseTargetQ3 := max(vbrRateQ3-overheadQ3, 0)
	if e.constrainedVBR {
		baseTargetQ3 += int(e.vbrOffset) >> lmDiff
	}

	targetQ3 := baseTargetQ3
	if e.hybrid {
		if e.silkOffset < 100 {
			targetQ3 += (12 << bitRes) >> lmDiff
		} else if e.silkOffset > 100 {
			targetQ3 -= (18 << bitRes) >> lmDiff
		}
		tfBoost := int((tfEstimate - 0.25) * float32(50<<bitRes))
		targetQ3 += tfBoost
		if tfEstimate > 0.7 {
			minHybridTarget := 50 << bitRes
			if targetQ3 < minHybridTarget {
				targetQ3 = minHybridTarget
			}
		}
	} else {
		targetQ3 = e.computeVBRTargetWithBoost(baseTargetQ3, frameSize, tfEstimate, pitchChange, totalBoost)
	}
	targetQ3 += tellFrac

	targetBytes := (targetQ3 + (1 << (bitRes + 2))) >> (bitRes + 3)
	minAllowed := ((tellFrac + totalBoost + (1 << (bitRes + 3)) - 1) >> (bitRes + 3)) + 2
	if e.hybrid {
		hybridMin := (tellFrac + (37 << bitRes) + totalBoost + (1 << (bitRes + 3)) - 1) >> (bitRes + 3)
		if minAllowed < hybridMin {
			minAllowed = hybridMin
		}
	}
	if targetBytes < minAllowed {
		targetBytes = minAllowed
	}
	if targetBytes > limitBytes {
		targetBytes = limitBytes
	}
	if targetBytes < 2 {
		targetBytes = 2
	}
	if e.constrainedVBR {
		deltaQ3 := targetQ3 - vbrRateQ3
		targetQ3 = targetBytes << (bitRes + 3)
		if e.vbrCount < 970 {
			e.vbrCount++
			alpha := float32(1.0) / float32(e.vbrCount+20)
			driftDelta := (deltaQ3 << lmDiff) - int(e.vbrOffset) - int(e.vbrDrift)
			e.vbrDrift += int32(alpha * float32(driftDelta))
		} else {
			driftDelta := (deltaQ3 << lmDiff) - int(e.vbrOffset) - int(e.vbrDrift)
			e.vbrDrift += int32(float32(0.001) * float32(driftDelta))
		}
		e.vbrReservoir = int32(int(e.vbrReservoir) + targetQ3 - vbrRateQ3)
		e.vbrOffset = -e.vbrDrift
		if e.vbrReservoir < 0 {
			adjust := int(-e.vbrReservoir) / (8 << bitRes)
			targetBytes += adjust
			e.vbrReservoir = 0
			if targetBytes > limitBytes {
				targetBytes = limitBytes
			}
		}
	}

	return targetBytes
}

// computeTargetBits computes the target CELT bit budget in bits.
// Reference: libopus celt/celt_encoder.c compute_vbr().
func (e *Encoder) computeTargetBits(frameSize int, tfEstimate float32, pitchChange bool) int {
	// CBR path uses fixed payload size.
	if !e.vbr {
		targetBits := e.cbrPayloadBytes(frameSize) * 8
		return targetBits
	}

	mode := e.modeConfig(frameSize)
	lm := mode.LM
	lmDiff := max(3-lm, 0)

	baseBits := e.bitrateToBits(frameSize)

	// Frame-size-dependent maximum payload bytes (510 kb/s physical limit).
	// Reference: libopus celt_encoder.c line 2445:
	//   nbCompressedBytes = IMIN(nbCompressedBytes, packet_size_cap >> (3-LM))
	// packet_size_cap = 1275 (total); subtract 1 for TOC-excluded payload.
	// In libopus VBR mode, nbCompressedBytes is the buffer cap (not bitrate-derived).
	// The per-bitrate constraint comes from CVBR reservoir tracking.
	packetSizeCap := 1275
	if extsupport.QEXT && e.qextActive() && !e.hybrid {
		packetSizeCap = qextPacketSizeCap
	}
	vbrMaxBytes := max((packetSizeCap>>(3-lm))-1, 2)

	// Convert to Q3 format (8ths of bits) for VBR computation
	// Reference: libopus celt_encoder.c line 1903
	vbrRateQ3 := baseBits << bitRes

	// Compute base_target with overhead subtraction
	// Reference: libopus celt_encoder.c line 2448
	// base_target = vbr_rate - ((40*C+20)<<BITRES)
	channels := e.codedChannels()
	overheadQ3 := (40*channels + 20) << bitRes
	baseTargetQ3 := max(vbrRateQ3-overheadQ3, 0)
	if e.constrainedVBR {
		// libopus line 2453-2454: base_target += (vbr_offset >> lm_diff)
		baseTargetQ3 += int(e.vbrOffset) >> lmDiff
	}

	// For VBR mode, apply boost based on signal characteristics.
	targetQ3 := e.computeVBRTarget(baseTargetQ3, frameSize, tfEstimate, pitchChange)

	// libopus adds ec_tell_frac(enc) to the VBR target before converting to
	// bytes (line 2478). This accounts for side information already written
	// (silence, postfilter, TF, trim, energy, etc.). Since gopus computes
	// the VBR target before encoding side information, we use the previous
	// frame's tell as an estimate. For frame 0, fall back to the static
	// overhead estimate.
	tellEstQ3 := e.lastTellFrac
	if tellEstQ3 == 0 {
		tellEstQ3 = overheadQ3
	}
	targetQ3 += tellEstQ3

	// libopus line 2480: nbAvailableBytes = (target+(1<<(BITRES+2)))>>(BITRES+3)
	targetBytes := max((targetQ3+(1<<(bitRes+2)))>>(bitRes+3), 2)

	// libopus line 2428: min_allowed = ((tell+total_boost+(1<<(BITRES+3))-1)>>(BITRES+3)) + 2
	// Use estimated tell and previous frame's total_boost since gopus computes
	// VBR target before encoding side information.
	totalBoostEst := e.lastDynalloc.TotBoost
	minAllowed := ((tellEstQ3 + totalBoostEst + (1 << (bitRes + 3)) - 1) >> (bitRes + 3)) + 2
	if targetBytes < minAllowed {
		targetBytes = minAllowed
	}
	// libopus line 2482: nbAvailableBytes = IMIN(nbCompressedBytes, nbAvailableBytes)
	// Cap VBR target to CBR-derived maximum payload.
	if targetBytes > vbrMaxBytes {
		targetBytes = vbrMaxBytes
	}

	if e.constrainedVBR {
		// libopus line 1949-1952: bound packet size from reservoir state.
		boundScale := e.constrainedVBRBoundScale
		if boundScale <= 0 {
			boundScale = 0
		} else if boundScale > 1 {
			boundScale = 1
		}
		vbrBoundQ3 := int(float32(vbrRateQ3) * boundScale)
		maxAllowedBytes := max((vbrRateQ3+vbrBoundQ3-int(e.vbrReservoir))>>(bitRes+3), 2)
		if targetBytes > maxAllowedBytes {
			targetBytes = maxAllowedBytes
		}

		// libopus line 2485: delta = target - vbr_rate (Q3 units).
		deltaQ3 := targetQ3 - vbrRateQ3
		targetQ3 = targetBytes << (bitRes + 3)

		// libopus line 2501-2506: adaptive smoothing factor.
		if e.vbrCount < 970 {
			e.vbrCount++
		}
		alpha := float32(0.001)
		if e.vbrCount < 970 {
			alpha = 1.0 / float32(e.vbrCount+20)
		}

		// libopus line 2508-2517: reservoir and drift/offset update.
		e.vbrReservoir = int32(int(e.vbrReservoir) + targetQ3 - vbrRateQ3)
		driftDelta := (deltaQ3 << lmDiff) - int(e.vbrOffset) - int(e.vbrDrift)
		e.vbrDrift += int32(alpha * float32(driftDelta))
		e.vbrOffset = -e.vbrDrift

		// libopus line 2520-2528: refill from min reservoir if needed.
		if e.vbrReservoir < 0 {
			adjust := int(-e.vbrReservoir) / (8 << bitRes)
			targetBytes += adjust
			e.vbrReservoir = 0
		}
		// libopus line 2529: nbCompressedBytes = IMIN(nbCompressedBytes, nbAvailableBytes)
		// Final cap after reservoir adjustment.
		if targetBytes > vbrMaxBytes {
			targetBytes = vbrMaxBytes
		}
	}

	targetBits := targetBytes * 8
	if e.maxPayloadBytes > 0 {
		maxBits := int(e.maxPayloadBytes) * 8
		if targetBits > maxBits {
			targetBits = maxBits
		}
	}

	// Clamp to reasonable bounds.
	// Minimum: 2 bytes (16 bits).
	// Maximum: frame-size-dependent cap (libopus packet_size_cap >> (3-LM)).
	if targetBits < 16 {
		targetBits = 16
	}
	maxBits := vbrMaxBytes * 8
	if targetBits > maxBits {
		targetBits = maxBits
	}
	return targetBits
}

// computeVBRTarget applies libopus-style CELT VBR shaping in Q3 units.
func (e *Encoder) computeVBRTarget(baseTargetQ3, frameSize int, tfEstimate float32, pitchChange bool) int {
	return e.computeVBRTargetWithBoost(baseTargetQ3, frameSize, tfEstimate, pitchChange, e.lastDynalloc.TotBoost)
}

func (e *Encoder) computeVBRTargetWithBoost(baseTargetQ3, frameSize int, tfEstimate float32, pitchChange bool, totalBoost int) int {
	mode := e.modeConfig(frameSize)
	lm := mode.LM
	nbBands := e.effectiveBandCount(frameSize)
	channels := e.codedChannels()

	codedBands := nbBands
	lastCodedBands := int(e.lastCodedBands)
	if lastCodedBands > 0 && lastCodedBands < nbBands {
		codedBands = lastCodedBands
	}
	if codedBands < 0 {
		codedBands = 0
	}
	if codedBands >= len(EBands) {
		codedBands = len(EBands) - 1
	}
	codedBins := EBands[codedBands] << lm
	if channels == 2 {
		codedStereoBands := codedBands
		intensity := int(e.intensity)
		if intensity < codedStereoBands {
			codedStereoBands = intensity
		}
		if codedStereoBands < 0 {
			codedStereoBands = 0
		}
		codedBins += EBands[codedStereoBands] << lm
	}

	targetQ3 := baseTargetQ3
	activity := float32(e.analysisActivity)
	if e.analysisValid && activity < 0.4 {
		targetQ3 -= int(float32(codedBins<<bitRes) * (0.4 - activity))
	}

	// Stereo savings (libopus compute_vbr(): applied before dynalloc boost).
	if channels == 2 && codedBins > 0 {
		codedStereoBands := codedBands
		intensity := int(e.intensity)
		if intensity < codedStereoBands {
			codedStereoBands = intensity
		}
		if codedStereoBands < 0 {
			codedStereoBands = 0
		}
		codedStereoDof := (EBands[codedStereoBands] << lm) - codedStereoBands
		if codedStereoDof > 0 {
			maxFrac := float32(0.8) * float32(codedStereoDof) / float32(codedBins)
			stereoSaving := float32(e.lastStereoSaving)
			if stereoSaving > 1 {
				stereoSaving = 1
			}
			saveA := int(maxFrac * float32(targetQ3))
			saveB := int((stereoSaving - 0.1) * float32(codedStereoDof<<bitRes))
			saving := min(saveB, saveA)
			targetQ3 -= saving
		}
	}

	// Boost the rate according to dynalloc (minus the dynalloc average for calibration).
	calibration := 19 << lm
	dynallocBoost := totalBoost - calibration
	targetQ3 += dynallocBoost

	// Transient boost with average compensation.
	tfCalibration := float32(0.044)
	if tfEstimate < 0 {
		tfEstimate = 0
	}
	if tfEstimate > 1 {
		tfEstimate = 1
	}
	tfBoost := int((tfEstimate - tfCalibration) * float32(targetQ3))
	targetQ3 += tfBoost

	// Tonality boost.
	tonality := float32(e.analysisTonality)
	if tonality < 0 {
		tonality = 0
	}
	if tonality > 1 {
		tonality = 1
	}
	if e.analysisValid && !e.lfe {
		tonal := tonality - 0.15
		if tonal < 0 {
			tonal = 0
		}
		tonal -= 0.12
		tonalTarget := targetQ3
		tonalTarget += int(float32(codedBins<<bitRes) * 1.2 * tonal)
		if pitchChange {
			tonalTarget += int(float32(codedBins<<bitRes) * 0.8)
		}
		targetQ3 = tonalTarget
	}

	// floor_depth limit from maxDepth.
	maxDepth := float32(e.lastDynalloc.MaxDepth)
	bins := 0
	if extsupport.QEXT && e.qextActive() && !e.hybrid {
		bins = qextShortMDCTSize(frameSize) << lm
	} else if nbBands >= 2 {
		bins = EBands[nbBands-2] << lm
	}
	floorDepth := int(float32((channels*bins)<<bitRes) * maxDepth)
	if floorDepth < (targetQ3 >> 2) {
		floorDepth = targetQ3 >> 2
	}
	if targetQ3 > floorDepth {
		targetQ3 = floorDepth
	}

	// Constrained VBR makes target changes less aggressive.
	if e.constrainedVBR && (len(e.energyMask) == 0 || e.lfe) {
		targetQ3 = baseTargetQ3 + int(float32(0.67)*float32(targetQ3-baseTargetQ3))
	}

	// Temporal VBR: adjust target based on frame-to-frame spectral variation.
	// Reference: libopus celt_encoder.c lines 1703-1710.
	// In float domain: target += temporal_vbr * 0.0000031 * clamp(96000-bitrate,0,32000) * target
	if len(e.energyMask) == 0 && tfEstimate < 0.2 {
		bitrate := e.bitrateToBits(frameSize) * (e.celtModeFs() / frameSize) // approximate bps
		clampedBR := min(max(96000-bitrate, 0), 32000)
		amount := float32(0.0000031) * float32(clampedBR)
		targetQ3 += int(float32(e.lastTemporalVBR) * amount * float32(targetQ3))
	}

	// Don't allow more than doubling the base target.
	maxTarget := 2 * baseTargetQ3
	if targetQ3 > maxTarget {
		targetQ3 = maxTarget
	}
	if targetQ3 < 0 {
		targetQ3 = 0
	}

	return targetQ3
}

// updateTonalityAnalysis computes tonality metrics from the current frame's MDCT coefficients
// and updates encoder state for use in the next frame's VBR decisions.
//
// This uses Spectral Flatness Measure (SFM) to distinguish tonal signals (music, speech)
// from noisy signals. The analysis is stored for the next frame because VBR target
// computation happens before MDCT in the encoding pipeline (matching libopus behavior).
//
// Parameters:
//   - normCoeffs: normalized MDCT coefficients (left channel for stereo)
//   - energies: band energies (log-domain) for spectral flux computation
//   - nbBands: number of frequency bands
//   - frameSize: frame size in samples (unused but kept for API consistency)
func (e *Encoder) updateTonalityAnalysis(normCoeffs []celtNorm, energies []celtGLog, nbBands, frameSize int) {
	// Compute tonality using Spectral Flatness Measure (zero-alloc version)
	tonalityResult := computeTonalityWithBandsScratch(normCoeffs, nbBands, frameSize, &e.tonalityScratch)

	// Compute spectral flux (frame-to-frame change) for smoothing decisions
	spectralFlux := computeSpectralFluxGLog(energies, e.prevBandLogEnergy, nbBands)

	// Update previous band log-energies for next frame's flux computation
	for i := 0; i < nbBands && i < len(energies) && i < len(e.prevBandLogEnergy); i++ {
		e.prevBandLogEnergy[i] = energies[i]
	}

	// Apply smoothing to tonality estimate
	// High spectral flux (transients) should reduce the smoothing factor
	// to allow faster adaptation to signal changes.
	// Low flux (stationary signals) uses more smoothing for stability.
	//
	// Smoothing formula: tonality = alpha * new + (1-alpha) * old
	// where alpha = 0.3 + 0.4 * spectralFlux (range: 0.3 to 0.7)
	alpha := opusVal16(0.3) + opusVal16(0.4)*opusVal16(spectralFlux)
	if alpha > 0.7 {
		alpha = 0.7
	}

	// Update tonality with smoothing
	lastTonality := alpha*opusVal16(tonalityResult.Tonality) + (1-alpha)*e.lastTonality

	// Clamp to valid range
	if lastTonality < 0 {
		lastTonality = 0
	}
	if lastTonality > 1 {
		lastTonality = 1
	}
	e.lastTonality = opusVal16(lastTonality)
}
