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

// fillTransientHistoryFromPrefilterF32 loads each channel's overlap head of
// the planar in buffer from the tail of prefilter_mem, as celt_encode_with_ec
// does before transient_analysis.
func (e *Encoder) fillTransientHistoryFromPrefilterF32(overlap, frameSize int, in []float32) {
	if overlap <= 0 {
		return
	}
	channels := int(e.channels)
	stride := frameSize + overlap
	maxPeriod := e.combMaxPeriod()
	for ch := range channels {
		head := in[ch*stride : ch*stride+overlap]
		if len(e.prefilterMem) < (ch+1)*maxPeriod {
			clear(head)
			continue
		}
		copySigToFloat32(head, e.prefilterMem[(ch+1)*maxPeriod-overlap:(ch+1)*maxPeriod])
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

// computeSurroundDynallocFromMask mirrors the libopus surround masking analysis
// in celt_encode_with_ec (celt/celt_encoder.c:2108-2185): it derives the
// per-band dynalloc floors, the surround trim, and surround_masking (the
// compute_vbr surround target offset) from the externally supplied energy mask.
// out holds MaxBands entries for standard modes; the analysis clears the first
// end of them and
// writes floors up to max(2, lastCodedBands), which exceeds end for the frames
// after a bandwidth reduction. Without a usable mask it leaves out untouched and
// returns zero trim, zero masking, and ok=false, as surround_trim and
// surround_masking start at 0.
func (e *Encoder) computeSurroundDynallocFromMask(end int, out []celtGLog) (trim, masking celtGLog, ok bool) {
	if e.lfe || e.hybrid || e.perMode != nil || len(e.energyMask) < MaxBands*int(e.channels) {
		return 0, 0, false
	}
	clear(out[:end])
	channels := e.codedChannels()
	maskEnd := max(2, int(e.lastCodedBands))

	maskAvg := celtGLog(0)
	diff := celtGLog(0)
	count := 0
	for c := range channels {
		for i := range maskEnd {
			mask := max(min(e.energyMask[MaxBands*c+i], 0.25), -2)
			if mask > 0 {
				mask *= 0.5
			}
			width := EBands[i+1] - EBands[i]
			maskAvg += mask * celtGLog(width)
			count += width
			diff += mask * celtGLog(1+2*i-maskEnd)
		}
	}
	maskAvg = maskAvg/celtGLog(count) + 0.2
	diff = (diff * 6 / celtGLog(channels*(maskEnd-1)*(maskEnd+1)*maskEnd)) * 0.5
	// Again, being conservative.
	diff = max(min(diff, 0.031), -0.031)

	// Find the band in the middle of the coded spectrum.
	midband := 0
	for EBands[midband+1] < EBands[maskEnd]/2 {
		midband++
	}

	countDynalloc := 0
	for i := range maskEnd {
		lin := maskAvg + diff*celtGLog(i-midband)
		unmask := e.energyMask[i]
		if channels == 2 {
			unmask = max(unmask, e.energyMask[MaxBands+i])
		}
		unmask = min(unmask, 0) - lin
		if unmask > 0.25 {
			out[i] = unmask - 0.25
			countDynalloc++
		}
	}

	if countDynalloc >= 3 {
		// Dynalloc in many bands means the initial masking rate was too low.
		maskAvg += 0.25
		if maskAvg > 0 {
			// The masking estimate went wrong: disable masking.
			maskAvg = 0
			diff = 0
			clear(out[:maskEnd])
		} else {
			for i := range maskEnd {
				out[i] = max(0, out[i]-0.25)
			}
		}
	}
	maskAvg += 0.2
	// Convert to the 1/64th units used by the trim.
	return 64 * diff, maskAvg, true
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
	clear(coeffs[bound:])
}

// EncodeFrame encodes one CELT frame of float32 PCM into its own range coder
// and returns the packet bytes: celt_encode_with_ec with enc == NULL and the
// payload budget set with SetMaxPayloadBytes (or the budget opus_encode_native
// hands CELT without one). pcm is interleaved when the encoder is stereo.
// frameSize is given at the encoder's API sample rate; at sub-48 kHz rates the
// input is upsampled to the 48 kHz core block internally.
func (e *Encoder) EncodeFrame(pcm []float32, frameSize int) ([]byte, error) {
	return e.encodeWithEC(pcm, frameSize, 0, nil)
}

// EncodeWithEC ports celt_encode_with_ec with a caller-owned range coder
// (enc != NULL): the frame continues the stream enc already holds (the SILK
// layer of a hybrid frame), nbCompressedBytes is the payload budget of the
// whole range coder (nb_compr_bytes), and the bytes enc filled on entry count
// against it. It finishes enc (ec_enc_done) and returns its nbCompressedBytes
// payload bytes.
func (e *Encoder) EncodeWithEC(pcm []float32, frameSize, nbCompressedBytes int, enc *rangecoding.Encoder) ([]byte, error) {
	if enc == nil {
		return nil, ErrEncodingFailed
	}
	return e.encodeWithEC(pcm, frameSize, int32(nbCompressedBytes), enc)
}

// encodeWithEC is celt_encode_with_ec. With enc == nil the frame codes into
// the encoder's own range coder with the payloadBudget budget; otherwise it
// codes into enc with the nbCompressedBytes budget.
func (e *Encoder) encodeWithEC(pcm []float32, frameSize int, nbCompressedBytes int32, enc *rangecoding.Encoder) ([]byte, error) {
	channels := int(e.channels)

	// At sub-48 kHz API rates the caller passes a native-Fs frame size and
	// native-length PCM; the CELT core block is frameSize*upsample (libopus
	// celt_encode_with_ec frame_size *= st->upsample). The input is zero-stuffed
	// to the core size in pre-emphasis below. At 48 kHz upsample==1 so this is the
	// identity and the frame size / input length checks are unchanged.
	upsample := e.effectiveUpsample()
	apiFrameSize := frameSize
	apiPCM := pcm
	// Without the stages that rewrite the core-rate frame (LSB quantization, DC
	// rejection, delay compensation, 2-tap pre-emphasis), pre-emphasis reads
	// the native-rate input and zero-stuffs it as it filters.
	nativeUpsample := upsample > 1 && !e.lsbQuantizationEnabled && !e.dcRejectEnabled &&
		!e.delayCompensationEnabled && e.hd96kPreemph[1] == 0
	if upsample > 1 {
		core := frameSize * upsample
		if !e.validFrameSize(core) {
			return nil, ErrInvalidFrameSize
		}
		if len(pcm) != apiFrameSize*channels {
			return nil, ErrInvalidInputLength
		}
		frameSize = core
		if !nativeUpsample {
			pcm = e.upsampleZeroStuff(apiPCM, apiFrameSize, channels, upsample)
		}
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
		samplesForFrame = e.applyDelayCompensationScratch(samplesForFrame, frameSize)
	}

	// Step 4: Detect transient and compute tf_estimate using PRE-EMPHASIZED signal
	// libopus calls transient_analysis(in, N+overlap, ...) where 'in' contains:
	// - Previous frame's pre-emphasized overlap samples (indices 0 to overlap-1)
	// - Current frame's pre-emphasized samples (indices overlap to overlap+N-1)
	// Reference: libopus celt_encoder.c line 2030
	overlap := min(e.analysisOverlap(), frameSize)

	// in is celt_encode_with_ec's planar buffer: channel c occupies
	// in[c*(N+overlap):(c+1)*(N+overlap)]. Its overlap head holds the tail of
	// prefilter_mem for transient analysis until run_prefilter swaps in the
	// filtered in_mem history for the MDCT.
	stride := frameSize + overlap
	in := ensureFloat32Slice(&e.scratch.planarIn, channels*stride)
	e.fillTransientHistoryFromPrefilterF32(overlap, frameSize, in)
	preemphasisInput := samplesForFrame
	if nativeUpsample {
		preemphasisInput = apiPCM
	}
	preemphasisTrace := e.beginEncodePreemphasisTrace(preemphasisInput, frameSize, overlap, nativeUpsample)
	var isSilence bool
	if nativeUpsample {
		isSilence = e.applyPreemphasisUpsampled(apiPCM, in, frameSize, overlap)
	} else {
		isSilence = e.applyPreemphasisWithScalingAndSilenceCore(samplesForFrame, in, frameSize, overlap)
	}
	e.finishEncodePreemphasisTrace(preemphasisTrace, in, frameSize, overlap)

	// Initialize the range encoder, then the frame budget: byte budget, VBR
	// rate and equiv_rate (celt_encoder.c:1873-1927). VBR starts from the full
	// payload cap and shrinks once dynalloc and the allocation trim are coded.
	e.clearLastQEXTPayload()
	re := enc
	if re == nil {
		buf := ensureByteSlice(&e.scratch.reBuf, int(e.packetSizeCap()))
		re = &e.scratch.rangeEncoder
		re.Init(buf)
		nbCompressedBytes = e.payloadBudget(frameSize)
	}
	e.SetRangeEncoder(re)
	budget := e.initFrameBudget(frameSize, lm, codedChannels, nbCompressedBytes, re)
	// ec_enc_init sizes an own range coder to the budget; a shared one shrinks
	// only to a CBR budget (celt_encoder.c:1911-1932).
	if enc == nil || (budget.vbrRate == 0 && e.targetBitrate != BitrateMax) {
		re.Shrink(uint32(budget.nbCompressedBytes))
	}

	// Reduces the likelihood of energy instability on fricatives at low bitrate
	// in hybrid mode (celt_encoder.c:2028).
	allowWeakTransients := e.hybrid && budget.effectiveBytes < 15 && e.silkSignalType != 2

	// Call transient analysis with the pre-emphasized signal (N+overlap samples)
	// and the libopus hybrid weak-transient gate when a SILK handoff is active.
	// libopus gates transient_analysis() behind st->complexity >= 1 && !st->lfe
	// (celt_encoder.c:2023). tone_detect() always runs (line 2020), so at
	// complexity 0 only tone_freq/toneishness are populated while the transient
	// outputs stay 0.
	var transientResult TransientAnalysisResult
	if e.complexity < 1 || e.lfe {
		transientResult = e.toneDetectOnlyF32(in, stride)
	} else if e.channels == 1 {
		transientResult = e.transientAnalysisMonoFloat32(in, stride, allowWeakTransients)
	} else {
		transientResult = e.TransientAnalysisF32(in, stride, allowWeakTransients)
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

	// Step 5: Encode the early flags (silence/postfilter).
	tell := re.Tell()
	if e.constrainVBRBudget(&budget, tell) {
		re.Shrink(uint32(budget.nbCompressedBytes))
	}
	totalBits := budget.TotalBits()
	e.frameBits = int32(totalBits)
	defer func() {
		e.frameBits = 0
		e.coarseAvailableSet = false
	}()

	if tell == 1 {
		if isSilence {
			re.EncodeBit(1, 15)
		} else {
			re.EncodeBit(0, 15)
		}
	} else {
		isSilence = false
	}
	if isSilence {
		// In VBR a silent frame needs no more than the minimum.
		if budget.vbrRate > 0 {
			budget.nbCompressedBytes = min(budget.nbCompressedBytes, budget.nbFilledBytes+2)
			budget.effectiveBytes = budget.nbCompressedBytes
			totalBits = budget.TotalBits()
			e.frameBits = int32(totalBits)
			budget.nbAvailableBytes = 2
			re.Shrink(uint32(budget.nbCompressedBytes))
		}
		// celt_encode_with_ec pretends the remaining bits are written as zeros
		// and still runs the whole frame: every budget-gated step skips itself
		// while the analysis state advances as for any other frame.
		re.AdvanceTell(int(budget.nbCompressedBytes) * 8)
	}
	start := 0
	if e.IsHybrid() {
		start = max(HybridCELTStartBand, 0)
		if start >= nbBands {
			start = max(nbBands-1, 0)
		}
	}
	prefilterTapset := e.TapsetDecision()
	// Match libopus run_prefilter enable gating (celt_encoder.c:2037).
	nbAvailableBytes := int(budget.nbAvailableBytes)
	enabled := ((e.lfe && nbAvailableBytes > 3) || nbAvailableBytes > 12*codedChannels) &&
		!isSilence &&
		re.Tell()+16 <= totalBits &&
		!e.disablePrefilter
	prevPrefilterPeriod := e.prefilterPeriod
	prevPrefilterGain := e.prefilterGain
	// Match libopus run_prefilter(): scale only when analysis is valid.
	maxPitchRatio := float32(1.0)
	if e.analysisValid {
		maxPitchRatio = e.analysisMaxPitchRatio
	}
	pfResult := e.runPrefilter(in, frameSize, prefilterTapset, enabled, tfEstimate, nbAvailableBytes, toneFreq, toneishness, maxPitchRatio)
	pitchChange := e.pitchChanged(pfResult, prevPrefilterPeriod, prevPrefilterGain)

	if !e.IsHybrid() && start == 0 && re.Tell()+16 <= totalBits {
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
	if lm > 0 && re.Tell()+3 <= totalBits {
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
		mdctLong := e.computeFrameMDCT(in, frameSize, overlap, 1, codedChannels, upsample)
		// Use bandLogE2 scratch buffer to avoid aliasing with energies
		bandLogE2 = ensureGLogSlice(&e.scratch.bandLogE2, nbBands*codedChannels)
		e.computeBandEnergiesGLogActive(mdctLong, nbBands, frameSize, codedChannels, 1<<lm, bandLogE2)
		e.encodeStageTrace.recordBandStage(mdctLong, nil, bandLogE2, frameSize, codedChannels, nbBands, lm)
		if bandLogE2 != nil {
			offset := celtGLog(0.5 * float32(lm))
			for i := range bandLogE2 {
				bandLogE2[i] += offset
			}
		}
	}

	// Step 5: Compute MDCT with proper overlap handling
	mdctCoeffs := e.computeFrameMDCT(in, frameSize, overlap, shortBlocks, codedChannels, upsample)
	if codedChannels < channels {
		tfChannel = 0
	}

	// Step 6: Compute band energies
	energies := ensureGLogSlice(&e.scratch.energies, nbBands*codedChannels)
	bandAmp := e.computeFrameBandEnergies(mdctCoeffs, nbBands, frameSize, codedChannels, lm, energies)
	e.encodeStageTrace.recordBandStage(mdctCoeffs, bandAmp, energies, frameSize, codedChannels, nbBands, lm)
	if e.lfe {
		applyLFEBandLogEClamp(energies, nbBands, codedChannels)
	}
	end := nbBands

	// Temporal VBR (not for LFE) runs on the band energies of the transient
	// analysis block decision, before the transient patch below
	// (celt_encoder.c:2186-2202).
	e.lastTemporalVBR = 0
	if !e.lfe {
		e.lastTemporalVBR = e.updateSpecAvg(energies, start, end, nbBands, codedChannels, shortBlocks > 1, lm)
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
	if lm > 0 && re.Tell()+3 <= totalBits && !transient && e.complexity >= 5 && !e.IsHybrid() && !e.lfe {
		spreadOld := ensureGLogSlice(&e.scratch.transientSpreadOld, end)
		if PatchTransientDecisionWithScratch(energies, e.prevEnergy, nbBands, e.predStride(), 0, end, codedChannels, spreadOld) {
			// Transient patched! Need to recompute MDCT with short blocks
			transient = true
			shortBlocks = mode.ShortBlocks
			tfEstimate = 0.2 // Match libopus: tf_estimate = QCONST16(.2f,14)

			// Recompute MDCT with short blocks
			mdctCoeffs = e.computeFrameMDCT(in, frameSize, overlap, shortBlocks, codedChannels, upsample)
			if codedChannels < channels {
				tfChannel = 0
			}

			// Recompute band energies with short block coefficients
			energies = ensureGLogSlice(&e.scratch.energies, nbBands*codedChannels)
			bandAmp = e.computeFrameBandEnergies(mdctCoeffs, nbBands, frameSize, codedChannels, lm, energies)
			e.encodeStageTrace.recordBandStage(mdctCoeffs, bandAmp, energies, frameSize, codedChannels, nbBands, lm)
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
	if lm > 0 && re.Tell()+3 <= totalBits {
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
		for band := start; band < nbBands; band++ {
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

	// quant_coarse_energy() reads the bytes left after the ones a shared range
	// coder held on entry.
	e.coarseAvailableBytes = budget.nbAvailableBytes
	e.coarseAvailableSet = true
	// quant_coarse_energy() receives the coder before its intra decision and
	// flag, so capture its matching input boundary before Go's trial decision.
	e.encodeStageTrace.recordCoarseInput(energies, nbBands, codedChannels, budget.nbAvailableBytes, re)
	intra := false
	if re.Tell()+3 <= totalBits {
		var kept bool
		intra, kept = e.decideIntraMode(energies, start, nbBands, lm, start == 0)
		if !kept {
			var intraBit int
			if intra {
				intraBit = 1
			}
			re.EncodeBit(intraBit, 3)
		}
	} else {
		intra = false
	}

	var quantizedEnergies []celtGLog
	if start > 0 {
		quantizedEnergies = e.EncodeCoarseEnergyRange(energies, start, nbBands, intra, lm)
	} else {
		quantizedEnergies = e.EncodeCoarseEnergy(energies, nbBands, intra, lm)
	}
	e.encodeStageTrace.recordCoarseOutput(quantizedEnergies, e.scratch.coarseError, re)
	// Step 11.0.5: Normalize bands early for TF analysis
	// TF analysis needs normalized coefficients to determine optimal time-frequency resolution
	var normL, normR []celtNorm
	var bandE []celtEner
	var normBandEScratch []celtEner
	var mdctLeft, mdctRight []float32
	if codedChannels == 2 {
		mdctLeft = mdctCoeffs[:frameSize]
		mdctRight = mdctCoeffs[frameSize : 2*frameSize]
	}
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
		normL, bandE = e.normalizeBandsMonoF32(mdctCoeffs, nbBands, frameSize, bandAmp)
		normBandEScratch = bandE
	} else {
		normL, normR, bandE = e.normalizeBandsStereoF32(mdctLeft, mdctRight, nbBands, frameSize, bandAmp)
	}
	e.recordEncodeNormalizationTrace(normL, normR, bandE, nbBands, lm, codedChannels)
	_ = normBandEScratch
	normLCelt := ensureNormSliceNoClear(&e.scratch.allocTrimNormL, len(normL))
	copy(normLCelt, normL)
	var normRCelt []celtNorm
	if codedChannels == 2 {
		normRCelt = ensureNormSliceNoClear(&e.scratch.allocTrimNormR, len(normR))
		copy(normRCelt, normR)
	}

	// Step 11.1: TF analysis, dynalloc and the stereo decisions read the
	// rate-derived effectiveBytes and equiv_rate of the frame budget.
	effectiveBytes := int(budget.effectiveBytes)
	equivRate := int(budget.equivRate)

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
	for i := range nbBands {
		if e.perMode != nil {
			logN[i] = int16(e.perMode.logN[i])
		} else {
			logN[i] = int16(LogN[i])
		}
	}
	// Determine VBR mode (match encoder settings)
	isVBR := e.vbr
	isConstrainedVBR := e.constrainedVBR
	bandLogE2Use := analysisEnergies
	if bandLogE2 != nil {
		bandLogE2Use = bandLogE2
	}
	// dynalloc_analysis reads oldBandE with the same band stride as bandLogE;
	// the energy history keeps predStride bands per channel.
	oldBandE := ensureGLogSlice(&e.scratch.dynallocOldBandE, nbBands*codedChannels)
	for c := range codedChannels {
		copy(oldBandE[c*nbBands:(c+1)*nbBands], prev1LogE[c*predStride:c*predStride+nbBands])
	}
	surroundTrimForAlloc := celtGLog(0)
	surroundMasking := celtGLog(0)
	var surroundDynalloc []celtGLog
	var surroundDynallocScratch [MaxBands]celtGLog
	if trim, masking, ok := e.computeSurroundDynallocFromMask(nbBands, surroundDynallocScratch[:]); ok {
		surroundTrimForAlloc = trim
		surroundMasking = masking
		surroundDynalloc = surroundDynallocScratch[:nbBands]
	}
	// libopus applies QEXT_SCALE(tone_freq) only in dynalloc tone
	// compensation. Keep the tone detector's raw value for the other analysis
	// stages and scale this local copy at the dynalloc boundary.
	dynallocToneFreq := toneFreq * float32(e.combScale())
	dynallocResult := DynallocAnalysisWithScratch(
		analysisEnergies, bandLogE2Use, oldBandE,
		nbBands, start, end, codedChannels, lsbDepth, lm,
		logN,
		effectiveBytes,
		transient, isVBR, isConstrainedVBR, e.lfe,
		dynallocToneFreq, toneishness,
		surroundDynalloc,
		e.analysisValid, e.dynallocLeakBoost(),
		&e.dynallocScratch,
		e.modeEdges(),
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
		tfRes, tfSelect = TFAnalysisWithScratch(tfInput, len(tfInput), nbBands, transient, lm, opusVal16(tfEstimate), effectiveBytes, importance, &e.tfScratch, e.modeEdges())

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
	// Every branch stores st->spread_decision: it is the hysteresis input of the
	// next frame's spreading_decision().
	var spread int
	if re.Tell()+4 <= totalBits {
		// Match libopus spread control policy:
		// - LFE: fixed normal
		// - Hybrid: fixed none/normal/aggressive based on complexity+transient
		// - CELT-only low-complexity/low-rate/transient shortcuts
		if e.lfe {
			e.tapsetDecision = 0
			spread = spreadNormal
		} else if e.IsHybrid() {
			if e.complexity == 0 {
				spread = spreadNone
			} else if transient {
				spread = spreadNormal
			} else {
				spread = spreadAggressive
			}
		} else if shortBlocks > 1 || e.complexity < 3 || nbAvailableBytes < 10*codedChannels {
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
	e.spreadDecision = int32(spread)
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
	totalBitsQ3ForDynalloc := totalBits << bitRes
	totalBoost := 0
	tellFracDynalloc := re.TellFrac()
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
			dualStereo = stereoAnalysisDecision(normL, normR, lm, nbBands, e.modeEdges())
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
				e.modeEdges(),
			)
			if codedChannels == 2 {
				e.lastStereoSaving = UpdateStereoSaving(e.lastStereoSaving, trimNormL, trimNormR, nbBands, lm, intensity, e.modeEdges())
			}
		}
		re.EncodeICDF(allocTrim, trimICDF, 7)
	}
	// Variable bitrate: size the frame from the compute_vbr target now that the
	// side information is coded (celt_encoder.c:2419-2533).
	vbrIn := vbrFrameInputs{
		lm:              lm,
		c:               codedChannels,
		tell:            int32(re.TellFrac()),
		totalBoost:      int32(totalBoost),
		totBoost:        int32(dynallocResult.TotBoost),
		tfEstimate:      tfEstimate,
		pitchChange:     pitchChange,
		maxDepth:        dynallocResult.MaxDepth,
		surroundMasking: surroundMasking,
		temporalVBR:     e.lastTemporalVBR,
		silence:         isSilence,
	}
	if budget.vbrRate > 0 {
		e.applyVBR(&budget, vbrIn)
		e.frameBits = int32(budget.TotalBits())
		re.Shrink(uint32(budget.nbCompressedBytes))
		if re.Error() != 0 {
			return nil, ErrEncodingFailed
		}
	}
	targetBytes := int(budget.nbCompressedBytes)
	targetBits := targetBytes * 8
	var qextEnc *rangecoding.Encoder
	var qextExtraBits []int32
	var qextFineBits []int32
	qextPayloadBytes := 0
	if extsupport.QEXT && e.qextActive() && !e.IsHybrid() {
		minAllowed := int(e.minAllowedBytes(&budget, vbrIn))
		// For CBR mode, libopus calls compute_vbr(base=initialTarget, tf2=min(1,2*tf), ...)
		// to estimate the natural VBR target, then passes that as the adjustment pivot.
		// For VBR/CVBR, targetBytes is already the VBR target so no extra computation is needed.
		// Reference: celt/celt_encoder.c lines 2543-2556.
		cbrVBRTargetBytes := 0
		if budget.vbrRate == 0 {
			offsetBytes := (codedChannels * 80000 * frameSize) / (e.celtModeFs() * 8)
			initialQextBytes := max(targetBytes-1275, max(0, (targetBytes-offsetBytes)*4/5))
			overheadQ3 := (40*codedChannels + 20) << bitRes
			baseQ3 := max((targetBytes-initialQextBytes/3)*8<<bitRes-overheadQ3, 0)
			vbrQ3 := int(e.computeVBR(int32(baseQ3), lm, codedChannels, budget.equivRate, int32(dynallocResult.TotBoost),
				min(1, 2*tfEstimate), pitchChange, dynallocResult.MaxDepth, surroundMasking, e.lastTemporalVBR))
			vbrQ3 += re.TellFrac()
			// celt_encoder.c uses target/(8<<BITRES), truncating toward zero.
			cbrVBRTargetBytes = max(vbrQ3/(8<<bitRes), 0)
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
	allocResult := e.computeAllocationScratch(
		re,
		totalBitsQ3,
		start,
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
	if e.lastCodedBands != 0 {
		lastCodedBands := int(e.lastCodedBands)
		e.lastCodedBands = int32(min(lastCodedBands+1, max(lastCodedBands-1, allocResult.CodedBands)))
	} else {
		e.lastCodedBands = int32(allocResult.CodedBands)
	}
	e.intensity = int32(allocResult.Intensity)
	intensity = allocResult.Intensity
	// Keep CELT allocation bandwidth gating driven only by explicit external
	// analysis input (SetAnalysisBandwidth), matching libopus behavior where
	// st->analysis.valid is supplied by the top-level analysis pipeline.

	// Step 13: Encode fine energy
	// Keep CELT fine/final energy refinement on the in-place residual state used
	// by coarse quantization. This mirrors libopus quant_fine_energy() ->
	// quant_energy_finalise() operating on the same error[] buffer.
	coarseResidual := e.scratch.coarseError
	var qextErrorBak [MaxBands * 2]celtGLog
	if len(coarseResidual) >= nbBands*codedChannels {
		coarseResidual = coarseResidual[:nbBands*codedChannels]
		if start > 0 {
			e.EncodeFineEnergyRangeFromError(quantizedEnergies, start, nbBands, allocResult.FineBits)
		} else {
			e.encodeFineEnergyFromError(quantizedEnergies, nbBands, nbBands, allocResult.FineBits, coarseResidual)
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
	var qextOldBandE []celtGLog
	var qextError []celtGLog
	var qextNormL []celtNorm
	var qextNormR []celtNorm
	if extsupport.QEXT && qextEnc != nil {
		if cfg, ok := computeQEXTModeConfig(int(e.sampleRate), qextShortMDCTSizeForMode(frameSize, mode)); ok && end == e.predStride() {
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
			qextOldBandE = e.ensureQEXTOldBandE(codedChannels)[:MaxBands*codedChannels]
			clear(qextQuantized)
			clear(qextError)
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
			// libopus passes bandLogE after its coarse-energy stabilization bias.
			energies,
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
		if qextPayloadBytes > 0 && len(coarseResidual) >= nbBands*codedChannels {
			// libopus preserves the residual before the extension refines it.
			copy(qextErrorBak[:nbBands*codedChannels], coarseResidual[:nbBands*codedChannels])
		}
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
	e.recordEncodeQuantInputTrace(normL, normR, bandE, end, lm, codedChannels, re)
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
	e.encodeStageTrace.recordQuantOutput(normL, normR)
	if qextActive {
		qextBandBits := qextFineBits[MaxBands : MaxBands+qextEnd]

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
		// libopus samples ext_balance before quant_fine_energy writes its raw bits.
		e.encodeFineEnergyFromErrorWithEncoder(qextEnc, qextOldBandE, qextEnd, MaxBands, qextBandBits, qextError)
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
	if qextPayloadBytes > 0 && len(coarseResidual) >= nbBands*codedChannels {
		// With extension bytes, libopus emits final raw bits from error_bak
		// with oldBandE=NULL. The refined quantized energy and live residual
		// remain the state used by the next frame.
		encodeEnergyFinaliseResidual(re, nil, qextErrorBak[:nbBands*codedChannels], start, nbBands, codedChannels,
			allocResult.FineBits, allocResult.FinePriority, bitsLeft)
	} else if len(coarseResidual) >= nbBands*codedChannels {
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
	// energyError keeps the refined residual for QEXT and the post-finalise
	// residual otherwise, clipped to [-0.5, 0.5] for the next frame's
	// stabilization. libopus clears all channels/bands before writing the
	// current coded channels (celt_encoder.c:2635).
	clear(e.energyError)
	for c := range codedChannels {
		baseState := c * e.predStride()
		baseFrame := c * nbBands
		for band := start; band < nbBands; band++ {
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
		// libopus ec_enc_done leaves the secondary coder's raw bits at the
		// end of its fixed storage, which the packet copies in full.
		qextEnc.Shrink(uint32(qextEnc.Storage()))
		qextEnc.Done()
		e.setLastQEXTPayload(qextEnc.Buffer()[:qextEnc.Storage()])
		if e.lastQEXTPayloadNonEmpty() {
			e.rng ^= qextEnc.Range()
		}
	}
	e.setPrevEnergyWithPrevCoded(quantizedEnergies, nbBands, codedChannels)
	if isSilence {
		e.resetPrevEnergyToSilence(nbBands, codedChannels)
	}
	e.clearUncodedPrevEnergy(start, nbBands)
	e.updateLogEnergyHistory(start, nbBands, transient)
	e.IncrementFrameCount()
	if transient || transientGotDisabled {
		e.consecTransient++
	} else {
		e.consecTransient = 0
	}

	return bytes, nil
}

// computeFrameBandEnergies fills energies with the frame's band log energies.
// For the standard band layout it runs compute_band_energies() and amp2Log2()
// together and returns the linear amplitudes, which normalise_bands() then
// reuses; otherwise it returns nil.
func (e *Encoder) computeFrameBandEnergies(mdctCoeffs []float32, nbBands, frameSize, channels, lm int, energies []celtGLog) []celtEner {
	if e.perMode == nil && e.hd96kOverlap == 0 && frameSize == Overlap<<lm {
		amp := ensureEnerSlice(&e.scratch.bandAmp, nbBands*channels)
		if computeBandAmplitudesGLogF32(mdctCoeffs, nbBands, frameSize, channels, 1<<lm, amp, energies) {
			return amp
		}
	}
	e.computeBandEnergiesGLogActive(mdctCoeffs, nbBands, frameSize, channels, 1<<lm, energies)
	return nil
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

func (e *Encoder) setPrevEnergyWithPrevCoded(energies []celtGLog, nbBands, codedChannels int) {
	if nbBands > e.predStride() {
		nbBands = e.predStride()
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

// updateLogEnergyHistory follows celt_encoder.c's oldLogE/oldLogE2 update.
// Non-transient frames shift oldLogE into oldLogE2 and replace oldLogE with
// oldBandE. Transient frames keep oldLogE2 and clamp oldLogE downward.
func (e *Encoder) updateLogEnergyHistory(start, end int, transient bool) {
	if !transient {
		copy(e.prevEnergy2, e.prevLogEnergy)
	}
	stride := e.predStride()
	end = min(end, stride)
	for channel := range int(e.channels) {
		base := channel * stride
		for band := range stride {
			index := base + band
			if band >= start && band < end {
				if transient {
					if e.prevEnergy[index] < e.prevLogEnergy[index] {
						e.prevLogEnergy[index] = e.prevEnergy[index]
					}
				} else {
					e.prevLogEnergy[index] = e.prevEnergy[index]
				}
			} else {
				e.prevLogEnergy[index] = -28
				e.prevEnergy2[index] = -28
			}
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

// computeFrameMDCT is compute_mdcts over celt_encode_with_ec's planar in
// buffer (channel stride frameSize+overlap). It returns codedChannels*frameSize
// coefficients in the mdctCoeffsF32 scratch: a stereo input coded as mono
// folds the two channel spectra, and the upsample scaling then applies to each
// coded channel.
func (e *Encoder) computeFrameMDCT(in []float32, frameSize, overlap, shortBlocks, codedChannels, upsample int) []float32 {
	e.recordEncodeMDCTTrace(in, frameSize, overlap, shortBlocks)
	scratch := &e.scratch
	stride := frameSize + overlap
	var coeffs []float32
	if e.channels == 1 || codedChannels == 2 {
		channels := int(e.channels)
		coeffs = ensureFloat32Slice(&scratch.mdctCoeffsF32, channels*frameSize)
		for ch := range channels {
			mdctForwardShortOverlapScratchIntoF32Coeffs(in[ch*stride:(ch+1)*stride], overlap, shortBlocks, coeffs[ch*frameSize:(ch+1)*frameSize], scratch)
		}
	} else {
		left := ensureFloat32Slice(&scratch.mdctLeftF32, frameSize)
		right := ensureFloat32Slice(&scratch.mdctRightF32, frameSize)
		mdctForwardShortOverlapScratchIntoF32Coeffs(in[:stride], overlap, shortBlocks, left, scratch)
		mdctForwardShortOverlapScratchIntoF32Coeffs(in[stride:2*stride], overlap, shortBlocks, right, scratch)
		coeffs = foldStereoMDCTToMonoF32(scratch.mdctCoeffsF32, left, right)
	}
	for ch := range codedChannels {
		applyUpsampleMDCTScaling(coeffs[ch*frameSize:(ch+1)*frameSize], upsample)
	}
	return coeffs
}

// updateSpecAvg ports the temporal-VBR analysis of celt_encode_with_ec
// (celt/celt_encoder.c:2186-2202): it follows the band-energy envelope over
// [start,end), advances the spectral average st->spec_avg, and returns
// temporal_vbr, the frame's clamped deviation from that average. bandLogE holds
// C channels with a channel stride of nbBands; shortBlocks compensates the
// short-MDCT energy scale.
func (e *Encoder) updateSpecAvg(bandLogE []celtGLog, start, end, nbBands, c int, shortBlocks bool, lm int) celtGLog {
	follow := celtGLog(-10)
	frameAvg := float32(0)
	offset := celtGLog(0)
	if shortBlocks {
		offset = 0.5 * celtGLog(lm)
	}
	for i := start; i < end; i++ {
		follow = max(follow-1, bandLogE[i]-offset)
		if c == 2 {
			follow = max(follow, bandLogE[i+nbBands]-offset)
		}
		frameAvg += follow
	}
	frameAvg /= float32(end - start)
	temporalVBR := min(3, max(-1.5, frameAvg-e.specAvg))
	e.specAvg += 0.02 * temporalVBR
	return temporalVBR
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

// clearUncodedPrevEnergy zeroes oldBandE outside the coded bands
// [start,end) of every channel, in case start or end change
// (celt_encoder.c:2790-2803).
func (e *Encoder) clearUncodedPrevEnergy(start, end int) {
	predStride := e.predStride()
	end = min(end, predStride)
	for c := range int(e.channels) {
		band := e.prevEnergy[c*predStride : (c+1)*predStride]
		clear(band[:start])
		clear(band[end:])
	}
}

// resetPrevEnergyToSilence sets the coded bands of the energy history to the
// -28 dB floor after a silent frame (celt_encoder.c: "if (silence)
// oldBandE[i] = -GCONST(28.f)"), mirroring the mono-to-stereo copy of
// setPrevEnergyWithPrevCoded.
func (e *Encoder) resetPrevEnergyToSilence(nbBands, codedChannels int) {
	predStride := e.predStride()
	for c := range codedChannels {
		for band := range nbBands {
			e.prevEnergy[c*predStride+band] = -28
		}
	}
	if e.channels == 2 && codedChannels == 1 {
		for band := range nbBands {
			e.prevEnergy[predStride+band] = -28
		}
	}
}
