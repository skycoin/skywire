//go:build gopus_fixed_point

package fixedpoint

import (
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// This file assembles the FIXED_POINT celt_encode_with_ec driver
// (celt/celt_encoder.c) for the static 48000/960 custom mode, orchestrating the
// already-ported integer kernels into a full frame encode that is bit-exact
// with the reference MODE_ENCODE / MODE_ENCODE_SEQ oracles (the produced packet
// bytes), for CBR, VBR and constrained-VBR (CVBR).
//
// Scope: a fresh or sequential encode with signalling disabled and the float
// analysis invalid, matching a plain celt_encoder_init + CELT_SET_SIGNALLING(0)
// encoder under OPUS_SET_VBR(0/1) and OPUS_SET_VBR_CONSTRAINT(0/1). It supports
// the full-band path (start==0), the hybrid-CELT band subset (start>0), the LFE
// path (st->lfe) and the surround energy_mask path (st->energy_mask). QEXT
// remains out of scope.

// spreadICDFEnc / trimICDFEnc mirror celt/celt.c spread_icdf[4] and trim_icdf[11].
var spreadICDFEnc = []uint8{25, 23, 2, 0}
var trimICDFEnc = []uint8{126, 124, 119, 109, 87, 41, 19, 9, 4, 2, 0}

// EncodeWithEC ports celt_encode_with_ec for one frame on the static 48000/960
// mode (CBR, VBR or constrained-VBR per SetVBR/SetConstrainedVBR). pcm is
// channels*frameSize interleaved int16 PCM, frameSize the 48k-core per-channel
// sample count (shortMdctSize<<LM). enc must be initialised against a buffer of
// nbCompressedBytes (the max payload size; the VBR rate control resizes the
// stream below that). It returns the number of packet bytes produced (the caller
// reads enc.Done()). The encoder's cross-frame state is advanced.
func (e *CELTEncoder) EncodeWithEC(pcm []int16, frameSize int, enc *rangecoding.Encoder, nbCompressedBytes int) int {
	nbEBands := celtNbEBands
	overlap := celtOverlap
	shortMdctSize := celtShortMdctSize
	eBands := e.eBands
	CC := e.channels
	C := e.channels
	start := e.start
	end := e.end
	hybrid := start != 0

	maxPeriod := combFilterMaxPeriod

	upsample := e.upsample
	if upsample < 1 {
		upsample = 1
	}
	// frame_size *= st->upsample: the public frame_size is at the API rate; the
	// CELT core always runs at 48 kHz, so the MDCT layout is sized from the
	// upsampled count and all bitrate/Fs arithmetic below uses mode->Fs==48000.
	frameSize *= upsample

	LM := 0
	for LM = 0; LM <= celtMaxLM; LM++ {
		if shortMdctSize<<LM == frameSize {
			break
		}
	}
	M := 1 << LM
	N := M * shortMdctSize

	sc := e.ensureScratch()

	tell0Frac := enc.TellFrac()
	tell := enc.Tell()
	nbFilledBytes := (tell + 4) >> 3

	const packetSizeCap = 1275
	if nbCompressedBytes > packetSizeCap {
		nbCompressedBytes = packetSizeCap
	}

	vbrRate := 0
	var effectiveBytes int
	if e.vbr && e.bitrate != opusBitrateMax {
		vbrRate = bitrateToBits(e.bitrate, frameSize) << bitRes
		effectiveBytes = vbrRate >> (3 + bitRes)
	} else {
		vbrRate = 0
		tmp := e.bitrate * frameSize
		if tell > 1 {
			tmp += tell * 48000
		}
		if e.bitrate != opusBitrateMax {
			v := (tmp + 4*48000) / (8 * 48000)
			if v < nbCompressedBytes {
				nbCompressedBytes = v
			}
			if nbCompressedBytes < 2 {
				nbCompressedBytes = 2
			}
			enc.Shrink(uint32(nbCompressedBytes))
		}
		effectiveBytes = nbCompressedBytes - nbFilledBytes
	}
	nbAvailableBytes := nbCompressedBytes - nbFilledBytes

	equivRate := nbCompressedBytes*8*50<<(3-LM) - (40*C+20)*((400>>LM)-50)
	if e.bitrate != opusBitrateMax {
		if v := e.bitrate - (40*C+20)*((400>>LM)-50); v < equivRate {
			equivRate = v
		}
	}

	// Constrained-VBR up-front max_allowed clamp (bust prevention).
	if vbrRate > 0 && e.constrainedVBR {
		vbrBound := vbrRate
		maxAllowedLo := 0
		if tell == 1 {
			maxAllowedLo = 2
		}
		maxAllowed := (vbrRate + vbrBound - int(e.vbrReservoir)) >> (bitRes + 3)
		if maxAllowedLo > maxAllowed {
			maxAllowed = maxAllowedLo
		}
		if nbAvailableBytes < maxAllowed {
			maxAllowed = nbAvailableBytes
		}
		if maxAllowed < nbAvailableBytes {
			nbCompressedBytes = nbFilledBytes + maxAllowed
			nbAvailableBytes = maxAllowed
			enc.Shrink(uint32(nbCompressedBytes))
		}
	}

	totalBits := nbCompressedBytes * 8

	effEnd := end
	if effEnd > nbEBands {
		effEnd = nbEBands
	}

	// in buffer (CC*(N+overlap)): overlap prefix from prefilter_mem, body from
	// pre-emphasised input.
	in := ensureInt32(&sc.in, CC*(N+overlap))

	// sample_max / silence over the res-domain (API-rate) input. The pcm buffer
	// holds C*(N/upsample) samples, so the overlap split uses /upsample counts to
	// match celt_maxabs_res(pcm, C*(N-overlap)/st->upsample) and the trailing
	// celt_maxabs_res(pcm + C*(N-overlap)/upsample, C*overlap/upsample).
	bodyLen := C * (N - overlap) / upsample
	sampleMax := e.overlapMax
	if v := maxabsRes(pcm, bodyLen); v > sampleMax {
		sampleMax = v
	}
	e.overlapMax = maxabsRes(pcm[bodyLen:], C*overlap/upsample)
	if e.overlapMax > sampleMax {
		sampleMax = e.overlapMax
	}
	silence := sampleMax == 0

	if tell == 1 {
		enc.EncodeBit(boolToInt(silence), 15)
	} else {
		silence = false
	}
	if silence {
		if vbrRate > 0 {
			if v := nbFilledBytes + 2; v < nbCompressedBytes {
				nbCompressedBytes = v
			}
			effectiveBytes = nbCompressedBytes
			totalBits = nbCompressedBytes * 8
			nbAvailableBytes = 2
			enc.Shrink(uint32(nbCompressedBytes))
		}
		tell = nbCompressedBytes * 8
		enc.SkipToTell(tell)
	}

	for c := 0; c < CC; c++ {
		e.preemphasis(pcm[c:], in[c*(N+overlap)+overlap:], N, CC, c)
		// in[c*(N+overlap) .. +overlap] = prefilter_mem[(1+c)*maxPeriod-overlap ..]
		copy(in[c*(N+overlap):c*(N+overlap)+overlap],
			e.prefilterMem[(1+c)*maxPeriod-overlap:(1+c)*maxPeriod])
	}

	toneFreq, toneishness := ToneDetect(in, CC, N+overlap, 48000, sc)

	isTransient := false
	tfEstimate := int16(0)
	tfChan := 0
	weakTransient := false
	if e.complexity >= 1 && !e.lfe {
		// allow_weak_transients = hybrid && effectiveBytes<15 && silk signalType!=2.
		// The CELT-only encoder leaves silk_info.signalType at 0, so the type test
		// holds whenever hybrid && effectiveBytes < 15.
		allowWeak := hybrid && effectiveBytes < 15
		ta := TransientAnalysis(in, N+overlap, CC, allowWeak, toneFreq, toneishness, sc)
		isTransient = ta.IsTransient
		tfEstimate = ta.TFEstimate
		tfChan = ta.TFChan
		weakTransient = ta.WeakTransient
	}
	// toneishness = MIN32(toneishness, QCONST32(1,29) - SHL32(tf_estimate,15))
	if v := gconstQ(1, 29) - shl32(int32(tfEstimate), 15); v < toneishness {
		toneishness = v
	}

	// run_prefilter: pitch/gain decision + comb-filter the time-domain in[].
	enabled := ((e.lfe && nbAvailableBytes > 3) || nbAvailableBytes > 12*C) &&
		!hybrid && !silence && tell+16 <= totalBits
	pfRes := e.runPrefilter(in, CC, N, overlap, enabled,
		toneFreq, toneishness, tfEstimate, nbAvailableBytes)
	pfOn := pfRes.PFOn
	pitchIndex := pfRes.PitchIndex
	gain1 := pfRes.Gain
	prefilterTapset := pfRes.Tapset
	// pitch_change (analysis invalid here so the tonality test is always true).
	pitchChange := false
	if (gain1 > 13107 || e.prefilterGain > 13107) &&
		(float64(pitchIndex) > 1.26*float64(e.prefilterPeriod) ||
			float64(pitchIndex) < 0.79*float64(e.prefilterPeriod)) {
		pitchChange = true
	}
	EmitPrefilterParams(enc, pfRes, hybrid, tell, totalBits)

	shortBlocks := 0
	if LM > 0 && enc.Tell()+3 <= totalBits {
		if isTransient {
			shortBlocks = M
		}
	} else {
		isTransient = false
	}
	transientGotDisabled := false
	if !(LM > 0 && enc.Tell()+3 <= totalBits) {
		transientGotDisabled = true
	}

	freq := ensureInt32(&sc.freq, CC*N)
	bandE := ensureInt32(&sc.bandE, nbEBands*CC)
	bandLogE := ensureInt32(&sc.bandLogE, nbEBands*CC)

	secondMdct := shortBlocks != 0 && e.complexity >= 8
	bandLogE2 := ensureInt32(&sc.bandLogE2, C*nbEBands)
	if secondMdct {
		e.computeMDCTs(0, in, freq, C, CC, LM)
		ComputeBandEnergies(freq, eBands, e.logN, bandE, nbEBands, shortMdctSize, effEnd, C, LM)
		Amp2Log2(bandE, bandLogE2, nbEBands, effEnd, end, C)
		for c := 0; c < C; c++ {
			for i := 0; i < end; i++ {
				bandLogE2[nbEBands*c+i] += half32(shl32(int32(LM), dbShift))
			}
		}
	}

	e.computeMDCTs(shortBlocks, in, freq, C, CC, LM)
	if CC == 2 && C == 1 {
		tfChan = 0
	}
	ComputeBandEnergies(freq, eBands, e.logN, bandE, nbEBands, shortMdctSize, effEnd, C, LM)
	if e.lfe {
		// For LFE, everything above band 0 is forced 80 dB below band 0.
		for i := 2; i < end; i++ {
			bandE[i] = min32(bandE[i], mult16x32Q15(3, bandE[0])) // QCONST16(1e-4f,15)=3
			bandE[i] = max32(bandE[i], epsilon)
		}
	}
	Amp2Log2(bandE, bandLogE, nbEBands, effEnd, end, C)

	// surround_dynalloc / surround_masking / surround_trim from energy_mask.
	// surroundMasking only writes surroundDynalloc when the energy mask is
	// active; otherwise it must stay all-zero, so clear the reused buffer.
	surroundDynalloc := ensureInt32(&sc.surroundDynalloc, C*nbEBands)
	clearInt32(surroundDynalloc)
	var surroundMasking, surroundTrim int32
	if !hybrid && e.energyMask != nil && !e.lfe {
		surroundMasking, surroundTrim = e.surroundMasking(surroundDynalloc, eBands, nbEBands, end, C)
	}
	// Temporal VBR is maintained except for LFE.
	temporalVBRValue := int32(0)
	if !e.lfe {
		temporalVBRValue = e.temporalVBR(bandLogE, start, end, nbEBands, C, shortBlocks, LM)
	}

	if !secondMdct {
		copy(bandLogE2, bandLogE[:C*nbEBands])
	}

	// Last-chance transient detection.
	if LM > 0 && enc.Tell()+3 <= totalBits && !isTransient && e.complexity >= 5 && !e.lfe && !hybrid {
		if PatchTransientDecision(bandLogE, e.oldBandE, nbEBands, start, end, C) {
			isTransient = true
			shortBlocks = M
			e.computeMDCTs(shortBlocks, in, freq, C, CC, LM)
			ComputeBandEnergies(freq, eBands, e.logN, bandE, nbEBands, shortMdctSize, effEnd, C, LM)
			Amp2Log2(bandE, bandLogE, nbEBands, effEnd, end, C)
			for c := 0; c < C; c++ {
				for i := 0; i < end; i++ {
					bandLogE2[nbEBands*c+i] += half32(shl32(int32(LM), dbShift))
				}
			}
			tfEstimate = 3277 // QCONST16(.2f,14)
		}
	}

	if LM > 0 && enc.Tell()+3 <= totalBits {
		enc.EncodeBit(boolToInt(isTransient), 3)
	}

	X := ensureInt32(&sc.bandX, C*N)
	NormaliseBands(freq, X, bandE, eBands, nbEBands, shortMdctSize, effEnd, C, M)

	enableTFAnalysis := effectiveBytes >= 15*C && !hybrid && e.complexity >= 2 && !e.lfe && toneishness < gconstQ(0.98, 29)

	offsets := ensureInt(&sc.offsets, nbEBands)
	importance := ensureInt(&sc.importance, nbEBands)
	spreadWeight := ensureInt(&sc.spreadWeight, nbEBands)

	var totBoost int
	maxDepth := DynallocAnalysis(bandLogE, bandLogE2, e.oldBandE, nbEBands, start, end, C,
		offsets, e.lsbDepth, e.logN, isTransient, e.vbr, e.constrainedVBR,
		eBands, LM, effectiveBytes, e.lfe, surroundDynalloc,
		importance, spreadWeight, toneFreq, toneishness, &totBoost, sc)

	tfRes := ensureInt(&sc.tfRes, nbEBands)
	tfSelect := 0
	if enableTFAnalysis {
		lambda := imax(80, 20480/effectiveBytes+2)
		tfSelect = TFAnalysis(eBands, effEnd, isTransient, tfRes, lambda, X, N, LM, tfEstimate, tfChan, importance, sc)
		for i := effEnd; i < end; i++ {
			tfRes[i] = tfRes[effEnd-1]
		}
	} else if hybrid && weakTransient {
		// Weak transients rely on TF on a long window not collapsing energy.
		for i := 0; i < end; i++ {
			tfRes[i] = 1
		}
		tfSelect = 0
	} else if hybrid && effectiveBytes < 15 {
		// Low-bitrate hybrid forces 5 ms temporal resolution rather than 2.5 ms.
		for i := 0; i < end; i++ {
			tfRes[i] = 0
		}
		tfSelect = boolToInt(isTransient)
	} else {
		for i := 0; i < end; i++ {
			tfRes[i] = boolToInt(isTransient)
		}
		tfSelect = 0
	}

	// Energy-error bias before coarse energy.
	errBuf := ensureInt32(&sc.energyErr, C*nbEBands)
	for c := 0; c < C; c++ {
		for i := start; i < end; i++ {
			if abs32(bandLogE[i+c*nbEBands]-e.oldBandE[i+c*nbEBands]) < gconst(2) {
				bandLogE[i+c*nbEBands] -= mult16x32Q15(8192, e.energyError[i+c*nbEBands]) // QCONST16(0.25,15)=8192
			}
		}
	}
	QuantCoarseEnergy(enc, bandLogE, e.oldBandE, errBuf, start, end, effEnd, nbEBands, C, LM,
		totalBits, nbAvailableBytes, false, e.complexity >= 4, 0, e.lfe, &e.delayedIntra, sc)

	TFEncode(start, end, isTransient, tfRes, LM, tfSelect, enc)

	// Spread decision.
	if enc.Tell()+4 <= totalBits {
		switch {
		case e.lfe:
			e.spreading.TapsetDecision = 0
			e.spreadDecision = spreadNormal
		case hybrid:
			if e.complexity == 0 {
				e.spreadDecision = spreadNone
			} else if isTransient {
				e.spreadDecision = spreadNormal
			} else {
				e.spreadDecision = spreadAggressive
			}
		case shortBlocks != 0 || e.complexity < 3 || nbAvailableBytes < 10*C:
			if e.complexity == 0 {
				e.spreadDecision = spreadNone
			} else {
				e.spreadDecision = spreadNormal
			}
		default:
			e.spreadDecision = SpreadingDecision(X, eBands, nbEBands, e.spreadDecision,
				&e.spreading, boolToInt(pfOn && shortBlocks == 0), effEnd, C, M, spreadWeight)
		}
		enc.EncodeICDF(e.spreadDecision, spreadICDFEnc, 5)
	} else {
		e.spreadDecision = spreadNormal
	}

	// For LFE, everything interesting is in the first band.
	if e.lfe {
		offsets[0] = imin(8, effectiveBytes/3)
	}

	cap := celt.InitCaps(nbEBands, LM, C)

	// Dynalloc boost coding.
	dynallocLogp := 6
	totalBitsQ3 := totalBits << bitRes
	totalBoost := 0
	tellFrac := enc.TellFrac()
	for i := start; i < end; i++ {
		width := C * (int(eBands[i+1]) - int(eBands[i])) << LM
		quanta := imin(width<<bitRes, imax(6<<bitRes, width))
		dynallocLoopLogp := dynallocLogp
		boost := 0
		j := 0
		for tellFrac+(dynallocLoopLogp<<bitRes) < totalBitsQ3-totalBoost && boost < int(cap[i]) {
			flag := j < offsets[i]
			enc.EncodeBit(boolToInt(flag), uint(dynallocLoopLogp))
			tellFrac = enc.TellFrac()
			if !flag {
				break
			}
			boost += quanta
			totalBoost += quanta
			dynallocLoopLogp = 1
			j++
		}
		if j != 0 {
			dynallocLogp = imax(2, dynallocLogp-1)
		}
		offsets[i] = boost
	}

	dualStereo := 0
	if C == 2 {
		intensityThresholds := []int{1, 2, 3, 4, 5, 6, 7, 8, 16, 24, 36, 44, 50, 56, 62, 67, 72, 79, 88, 106, 134}
		intensityHisteresis := []int{1, 1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 2, 2, 2, 3, 3, 4, 5, 6, 8, 8}
		if LM != 0 {
			dualStereo = boolToInt(StereoAnalysis(eBands, X, LM, N, nbEBands))
		}
		e.intensity = hysteresisDecision(equivRate/1000, intensityThresholds, intensityHisteresis, 21, e.intensity)
		e.intensity = imin(end, imax(start, e.intensity))
	}

	allocTrim := 5
	if tellFrac+(6<<bitRes) <= totalBitsQ3-totalBoost {
		if start > 0 || e.lfe {
			e.stereoSaving = 0
			allocTrim = 5
		} else {
			res := AllocTrimAnalysis(eBands, X, bandLogE, end, LM, C, N, nbEBands,
				e.stereoSaving, tfEstimate, e.intensity, surroundTrim, int32(equivRate), false, 0)
			allocTrim = res.TrimIndex
			e.stereoSaving = res.StereoSaving
		}
		enc.EncodeICDF(allocTrim, trimICDFEnc, 7)
		tellFrac = enc.TellFrac()
	}

	// min_allowed: the frame must not shrink so far the encoder runs out of bits.
	minAllowed := ((tellFrac + totalBoost + (1 << (bitRes + 3)) - 1) >> (bitRes + 3)) + 2
	if hybrid {
		if v := (tell0Frac + (37 << bitRes) + totalBoost + (1 << (bitRes + 3)) - 1) >> (bitRes + 3); v > minAllowed {
			minAllowed = v
		}
	}

	// Variable bitrate rate control.
	if vbrRate > 0 {
		lmDiff := celtMaxLM - LM
		if v := packetSizeCap >> (3 - LM); v < nbCompressedBytes {
			nbCompressedBytes = v
		}
		var baseTarget int
		if !hybrid {
			baseTarget = vbrRate - ((40*C + 20) << bitRes)
		} else {
			baseTarget = imax(0, vbrRate-((9*C+4)<<bitRes))
		}
		if e.constrainedVBR {
			baseTarget += int(e.vbrOffset) >> lmDiff
		}

		var target int
		if !hybrid {
			target = computeVBR(eBands, baseTarget, LM, equivRate, e.lastCodedBands, C, e.intensity,
				e.constrainedVBR, e.stereoSaving, totBoost, tfEstimate, pitchChange,
				maxDepth, temporalVBRValue, nbEBands, e.lfe, e.energyMask != nil, surroundMasking)
		} else {
			target = baseTarget
			target += int(mult16x16Q14(int32(tfEstimate)-gconstQ(0.25, 14), int32(50<<bitRes)))
			if tfEstimate > 11469 { // QCONST16(.7f,14)
				target = imax(target, 50<<bitRes)
			}
		}

		// tell here is ec_tell_frac(enc) (1/8-bit units), matching the C reuse of
		// the tell variable after alloc_trim coding.
		target += tellFrac
		nbAvailableBytes = (target + (1 << (bitRes + 2))) >> (bitRes + 3)
		nbAvailableBytes = imax(minAllowed, nbAvailableBytes)
		nbAvailableBytes = imin(nbCompressedBytes, nbAvailableBytes)

		delta := int32(target - vbrRate)
		target = nbAvailableBytes << (bitRes + 3)

		if silence {
			nbAvailableBytes = 2
			target = 2 * 8 << bitRes
			delta = 0
		}

		var alpha int32
		if e.vbrCount < 970 {
			e.vbrCount++
			alpha = CeltRcp(shl32(e.vbrCount+20, 16))
		} else {
			alpha = 33 // QCONST16(.001f,15)
		}
		if e.constrainedVBR {
			e.vbrReservoir += int32(target - vbrRate)
		}
		if e.constrainedVBR {
			e.vbrDrift += mult16x32Q15(int16(alpha), (delta*(1<<lmDiff))-e.vbrOffset-e.vbrDrift)
			e.vbrOffset = -e.vbrDrift
		}
		if e.constrainedVBR && e.vbrReservoir < 0 {
			adjust := int(-e.vbrReservoir) / (8 << bitRes)
			if !silence {
				nbAvailableBytes += adjust
			}
			e.vbrReservoir = 0
		}
		if nbAvailableBytes < nbCompressedBytes {
			nbCompressedBytes = nbAvailableBytes
		}
		enc.Shrink(uint32(nbCompressedBytes))
	}

	// Bit allocation.
	bits := (int32(nbCompressedBytes)*8)<<bitRes - int32(enc.TellFrac()) - 1
	antiCollapseRsv := 0
	if isTransient && LM >= 2 && bits >= int32((LM+2)<<bitRes) {
		antiCollapseRsv = 1 << bitRes
	}
	bits -= int32(antiCollapseRsv)
	signalBandwidth := end - 1
	if e.lfe {
		signalBandwidth = 1
	}

	offsets32 := ensureInt32(&sc.offsets32, nbEBands)
	for i := range offsets32 {
		offsets32[i] = int32(offsets[i])
	}
	alloc := celt.ComputeAllocationWithEncoderStartInto(&sc.allocScratch, enc, start, int(bits), end, C, cap, offsets32,
		allocTrim, e.intensity, dualStereo != 0, LM, e.lastCodedBands, signalBandwidth)
	codedBands := alloc.CodedBands
	e.intensity = alloc.Intensity
	dualStereo = boolToInt(alloc.DualStereo)
	if e.lastCodedBands != 0 {
		e.lastCodedBands = imin(e.lastCodedBands+1, imax(e.lastCodedBands-1, codedBands))
	} else {
		e.lastCodedBands = codedBands
	}

	fineQuant := ensureInt32(&sc.fineQuant, nbEBands)
	finePriority := ensureInt32(&sc.finePriority, nbEBands)
	copy(fineQuant, alloc.FineBits)
	copy(finePriority, alloc.FinePriority)
	pulses := ensureInt(&sc.pulses, nbEBands)
	for i := 0; i < len(alloc.BandBits) && i < nbEBands; i++ {
		pulses[i] = int(alloc.BandBits[i])
	}

	QuantFineEnergy(enc, e.oldBandE, errBuf, start, end, nbEBands, C, nil, fineQuant)
	for i := 0; i < nbEBands*CC; i++ {
		e.energyError[i] = 0
	}

	// Residual quantisation.
	var y []int32
	if C == 2 {
		y = X[N:]
	}
	seed := e.rng
	collapse := QuantAllBandsEncode(enc, C, N, LM, start, end, X, y, bandE,
		pulses, tfRes, shortBlocks, e.spreadDecision, dualStereo, e.intensity,
		nbCompressedBytes*(8<<bitRes)-antiCollapseRsv, alloc.Balance, codedBands,
		e.complexity, false, &seed, sc)
	e.rng = seed
	_ = collapse

	antiCollapseOn := false
	if antiCollapseRsv > 0 {
		antiCollapseOn = e.consecTransient < 2
		enc.EncodeRawBits(uint32(boolToInt(antiCollapseOn)), 1)
	}

	QuantEnergyFinalise(enc, e.oldBandE, errBuf, start, end, nbEBands, C, fineQuant, finePriority, nbCompressedBytes*8-enc.Tell())

	for c := 0; c < C; c++ {
		for i := start; i < end; i++ {
			ee := errBuf[i+c*nbEBands]
			ee = max32(-gconstF(0.5), min32(gconstF(0.5), ee))
			e.energyError[i+c*nbEBands] = ee
		}
	}

	if silence {
		for i := 0; i < C*nbEBands; i++ {
			e.oldBandE[i] = -gconst(28)
		}
	}

	// Cross-frame state updates.
	e.prefilterPeriod = pitchIndex
	e.prefilterGain = gain1
	e.prefilterTapset = prefilterTapset
	_ = tell0Frac

	if CC == 2 && C == 1 {
		copy(e.oldBandE[nbEBands:2*nbEBands], e.oldBandE[:nbEBands])
	}
	if !isTransient {
		copy(e.oldLogE2[:CC*nbEBands], e.oldLogE[:CC*nbEBands])
		copy(e.oldLogE[:CC*nbEBands], e.oldBandE[:CC*nbEBands])
	} else {
		for i := 0; i < CC*nbEBands; i++ {
			e.oldLogE[i] = min32(e.oldLogE[i], e.oldBandE[i])
		}
	}
	for c := 0; c < CC; c++ {
		for i := 0; i < start; i++ {
			e.oldBandE[c*nbEBands+i] = 0
			e.oldLogE[c*nbEBands+i] = -gconst(28)
			e.oldLogE2[c*nbEBands+i] = -gconst(28)
		}
		for i := end; i < nbEBands; i++ {
			e.oldBandE[c*nbEBands+i] = 0
			e.oldLogE[c*nbEBands+i] = -gconst(28)
			e.oldLogE2[c*nbEBands+i] = -gconst(28)
		}
	}
	if isTransient || transientGotDisabled {
		e.consecTransient++
	} else {
		e.consecTransient = 0
	}
	e.rng = enc.Range()

	enc.Done()
	return nbCompressedBytes
}

// runPrefilter ports celt/celt_encoder.c run_prefilter for the FIXED_POINT,
// non-QEXT build: it assembles the per-channel pre[] history buffers, runs the
// pitch/gain analysis (PrefilterAnalysis), comb-filters the time-domain in[] in
// place (matching the encoder's pre-MDCT prefiltering, including the cancel-pitch
// fallback), and updates in_mem / prefilter_mem. It returns the PrefilterResult.
func (e *CELTEncoder) runPrefilter(in []int32, CC, N, overlap int, enabled bool,
	toneFreq int16, toneishness int32, tfEstimate int16, nbAvailableBytes int) PrefilterResult {

	maxPeriod := combFilterMaxPeriod
	offset := celtShortMdctSize - overlap

	sc := e.scratch
	row := N + maxPeriod
	var pre [][]int32
	if sc != nil {
		store := ensureInt32(&sc.preStorage, CC*row)
		if cap(sc.prePeriodic) < CC {
			sc.prePeriodic = make([][]int32, CC)
		} else {
			sc.prePeriodic = sc.prePeriodic[:CC]
		}
		pre = sc.prePeriodic
		for c := 0; c < CC; c++ {
			pre[c] = store[c*row : (c+1)*row]
		}
	} else {
		pre = make([][]int32, CC)
		for c := 0; c < CC; c++ {
			pre[c] = make([]int32, row)
		}
	}
	for c := 0; c < CC; c++ {
		copy(pre[c][:maxPeriod], e.prefilterMem[c*maxPeriod:(c+1)*maxPeriod])
		copy(pre[c][maxPeriod:], in[c*(N+overlap)+overlap:c*(N+overlap)+overlap+N])
	}

	res := PrefilterAnalysis(pre, CC, N, PrefilterParams{
		Enabled:                 enabled,
		Complexity:              e.complexity,
		Toneishness:             toneishness,
		ToneFreq:                toneFreq,
		TFEstimate:              tfEstimate,
		NbAvailableBytes:        nbAvailableBytes,
		PrefilterPeriod:         e.prefilterPeriod,
		PrefilterGain:           e.prefilterGain,
		PrefilterTapset:         e.prefilterTapset,
		PrefilterTapsetDecision: e.spreading.TapsetDecision,
		LossRate:                0,
		AnalysisValid:           false,
		MaxPitchRatio:           0,
	}, sc)

	prefilterPeriod := imax(e.prefilterPeriod, combFilterMinPeriod)
	pitchIndex := res.PitchIndex
	gain1 := res.Gain

	var before, after []int32
	if sc != nil {
		before = ensureInt32(&sc.prefBefore, CC)
		after = ensureInt32(&sc.prefAfter, CC)
		clearInt32(before)
		clearInt32(after)
	} else {
		before = make([]int32, CC)
		after = make([]int32, CC)
	}
	for c := 0; c < CC; c++ {
		base := c * (N + overlap)
		copy(in[base:base+overlap], e.inMem[c*overlap:(c+1)*overlap])
		for i := 0; i < N; i++ {
			before[c] += abs32(shr32(in[base+overlap+i], 12))
		}
		if offset != 0 {
			combFilterPF(in, base+overlap, pre[c], maxPeriod,
				prefilterPeriod, prefilterPeriod, offset, -e.prefilterGain, -e.prefilterGain,
				e.prefilterTapset, e.prefilterTapset, nil, 0)
		}
		combFilterPF(in, base+overlap+offset, pre[c], maxPeriod+offset,
			prefilterPeriod, pitchIndex, N-offset, -e.prefilterGain, -gain1,
			e.prefilterTapset, res.Tapset, e.window, overlap)
		for i := 0; i < N; i++ {
			after[c] += abs32(shr32(in[base+overlap+i], 12))
		}
	}

	cancelPitch := false
	if CC == 2 {
		thresh0 := mult16x32Q15(mult16x16q15(8192, gain1), before[0]) + mult16x32Q15(328, before[1])
		thresh1 := mult16x32Q15(mult16x16q15(8192, gain1), before[1]) + mult16x32Q15(328, before[0])
		if after[0]-before[0] > thresh0 || after[1]-before[1] > thresh1 {
			cancelPitch = true
		}
		if before[0]-after[0] < thresh0 && before[1]-after[1] < thresh1 {
			cancelPitch = true
		}
	} else {
		if after[0] > before[0] {
			cancelPitch = true
		}
	}
	if cancelPitch {
		for c := 0; c < CC; c++ {
			base := c * (N + overlap)
			copy(in[base+overlap:base+overlap+N], pre[c][maxPeriod:maxPeriod+N])
			combFilterPF(in, base+overlap+offset, pre[c], maxPeriod+offset,
				prefilterPeriod, pitchIndex, overlap, -e.prefilterGain, 0,
				e.prefilterTapset, res.Tapset, e.window, overlap)
		}
		gain1 = 0
		res.PFOn = false
		res.QG = 0
		res.Gain = 0
	}

	for c := 0; c < CC; c++ {
		base := c * (N + overlap)
		copy(e.inMem[c*overlap:(c+1)*overlap], in[base+N:base+N+overlap])
		if N > maxPeriod {
			copy(e.prefilterMem[c*maxPeriod:(c+1)*maxPeriod], pre[c][N:N+maxPeriod])
		} else {
			copy(e.prefilterMem[c*maxPeriod:c*maxPeriod+maxPeriod-N], e.prefilterMem[c*maxPeriod+N:c*maxPeriod+maxPeriod])
			copy(e.prefilterMem[c*maxPeriod+maxPeriod-N:(c+1)*maxPeriod], pre[c][maxPeriod:maxPeriod+N])
		}
	}
	res.PitchIndex = pitchIndex
	return res
}

// temporalVBR ports the temporal-VBR spec_avg update from celt_encode_with_ec
// (the !lfe branch). It maintains st->spec_avg and returns the computed
// temporal_vbr (celt_glog) for compute_vbr.
func (e *CELTEncoder) temporalVBR(bandLogE []int32, start, end, nbEBands, C, shortBlocks, LM int) int32 {
	follow := -gconstQ(10, dbShift-5)
	frameAvg := int32(0)
	var offset int32
	if shortBlocks != 0 {
		offset = half32(shl32(int32(LM), dbShift-5))
	}
	for i := start; i < end; i++ {
		follow = max32(follow-gconstQ(1, dbShift-5), shr32(bandLogE[i], 5)-offset)
		if C == 2 {
			follow = max32(follow, shr32(bandLogE[i+nbEBands], 5)-offset)
		}
		frameAvg += follow
	}
	frameAvg /= int32(end - start)
	temporalVBR := shl32(frameAvg, 5) - e.specAvg
	temporalVBR = min32(gconstF(3), max32(-gconstF(1.5), temporalVBR))
	e.specAvg += mult16x32Q15(655, temporalVBR) // QCONST16(.02f,15)=655
	return temporalVBR
}

// bitrateToBits ports celt.h bitrate_to_bits for the 48000 Hz core:
// bitrate*6/(6*48000/frame_size), with the inner division evaluated first.
func bitrateToBits(bitrate, frameSize int) int {
	return bitrate * 6 / (6 * 48000 / frameSize)
}

// surroundMasking ports the energy_mask-driven surround masking block of
// celt_encode_with_ec (the !hybrid && energy_mask && !lfe branch). It fills
// surroundDynalloc[0..mask_end) and returns (surround_masking, surround_trim)
// in celt_glog / 1-64th-units, matching the reference's mask_avg and
// 64*diff respectively.
func (e *CELTEncoder) surroundMasking(surroundDynalloc []int32, eBands []int16, nbEBands, end, C int) (surroundMasking, surroundTrim int32) {
	mask := e.energyMask
	maskEnd := imax(2, e.lastCodedBands)
	var maskAvg, diff int32
	count := 0
	for c := 0; c < C; c++ {
		for i := 0; i < maskEnd; i++ {
			m := max32(min32(mask[nbEBands*c+i], gconstF(0.25)), -gconstF(2.0))
			if m > 0 {
				m = half32(m)
			}
			m16 := shr32(m, dbShift-10)
			w := int32(eBands[i+1] - eBands[i])
			maskAvg += mult16x16(m16, w)
			count += int(w)
			diff += mult16x16(m16, int32(1+2*i-maskEnd))
		}
	}
	maskAvg = shl32(maskAvg/int32(count), dbShift-10)
	maskAvg += gconstF(0.2)
	diff = shl32(diff*6/int32(C*(maskEnd-1)*(maskEnd+1)*maskEnd), dbShift-10)
	diff = half32(diff)
	diff = max32(min32(diff, gconstF(0.031)), -gconstF(0.031))

	midband := 0
	for eBands[midband+1] < eBands[maskEnd]/2 {
		midband++
	}
	countDynalloc := 0
	for i := 0; i < maskEnd; i++ {
		lin := maskAvg + diff*int32(i-midband)
		var unmask int32
		if C == 2 {
			unmask = max32(mask[i], mask[nbEBands+i])
		} else {
			unmask = mask[i]
		}
		unmask = min32(unmask, gconstF(0))
		unmask -= lin
		if unmask > gconstF(0.25) {
			surroundDynalloc[i] = unmask - gconstF(0.25)
			countDynalloc++
		}
	}
	if countDynalloc >= 3 {
		maskAvg += gconstF(0.25)
		if maskAvg > 0 {
			maskAvg = 0
			diff = 0
			for i := 0; i < maskEnd; i++ {
				surroundDynalloc[i] = 0
			}
		} else {
			for i := 0; i < maskEnd; i++ {
				surroundDynalloc[i] = max32(0, surroundDynalloc[i]-gconstF(0.25))
			}
		}
	}
	maskAvg += gconstF(0.2)
	surroundTrim = 64 * diff
	surroundMasking = maskAvg
	return surroundMasking, surroundTrim
}

// computeVBR ports celt/celt_encoder.c compute_vbr for the FIXED_POINT,
// non-QEXT build with the float analysis invalid. surround masking and LFE are
// supported via has_surround_mask/lfe. It returns the target rate in 8th-bits
// per frame.
func computeVBR(eBands []int16, baseTarget, LM, equivRate, lastCodedBands, C, intensity int,
	constrainedVBR bool, stereoSaving int16, totBoost int, tfEstimate int16,
	pitchChange bool, maxDepth, temporalVBR int32, nbEBands int,
	lfe, hasSurroundMask bool, surroundMasking int32) int {

	codedBands := lastCodedBands
	if codedBands == 0 {
		codedBands = nbEBands
	}
	codedBins := int(eBands[codedBands]) << LM
	if C == 2 {
		codedBins += int(eBands[imin(intensity, codedBands)]) << LM
	}

	target := baseTarget

	if C == 2 {
		codedStereoBands := imin(intensity, codedBands)
		codedStereoDof := (int(eBands[codedStereoBands]) << LM) - codedStereoBands
		maxFrac := div32_16(mult16x16(26214, int32(codedStereoDof)), int16(codedBins)) // QCONST16(0.8f,15)
		ss := stereoSaving
		if ss > 256 { // QCONST16(1.f,8)
			ss = 256
		}
		a := mult16x32Q15(maxFrac, int32(target))
		b := shr32(mult16x16(int32(ss)-26, int32(codedStereoDof<<bitRes)), 8) // QCONST16(0.1f,8)=26
		target -= int(min32(a, b))
	}

	target += totBoost - (19 << LM)

	const tfCalibration = 721 // QCONST16(0.044f,14)
	target += int(shl32(mult16x32Q15(int16(int32(tfEstimate)-tfCalibration), int32(target)), 1))

	// analysis tonality boost is invalid here (analysis->valid == 0).

	if hasSurroundMask && !lfe {
		surroundTarget := target + int(shr32(mult16x16(shr32(surroundMasking, dbShift-10), int32(codedBins<<bitRes)), 10))
		if v := target / 4; v > surroundTarget {
			surroundTarget = v
		}
		target = surroundTarget
	}

	// floor_depth
	bins := int(eBands[nbEBands-2]) << LM
	floorDepth := int(shr32(mult16x32Q15(int16((C*bins)<<bitRes), maxDepth), dbShift-15))
	if v := target >> 2; v > floorDepth {
		floorDepth = v
	}
	if floorDepth < target {
		target = floorDepth
	}

	if (!hasSurroundMask || lfe) && constrainedVBR {
		target = baseTarget + int(mult16x32Q15(21955, int32(target-baseTarget))) // QCONST16(0.67f,15)
	}

	// Temporal VBR. pitch_change is unused on this path (analysis invalid) but
	// kept in the signature to mirror compute_vbr.
	_ = pitchChange
	if !hasSurroundMask && tfEstimate < 3277 { // QCONST16(.2f,14)
		clamp := 96000 - equivRate
		if clamp > 32000 {
			clamp = 32000
		}
		if clamp < 0 {
			clamp = 0
		}
		amount := mult16x16Q15(3329, int32(clamp)) // QCONST16(.0000031f,30)=3329
		tvbrFactor := shr32(mult16x16(shr32(temporalVBR, dbShift-10), amount), 10)
		target += int(mult16x32Q15(int16(tvbrFactor), int32(target)))
	}

	return imin(2*baseTarget, target)
}

// maxabsRes ports celt_maxabs_res for ENABLE_RES24 int16 input: the maximum
// absolute res-domain value over the first n interleaved samples (res = s<<8).
func maxabsRes(pcm []int16, n int) int32 {
	var maxval, minval int32
	for i := 0; i < n; i++ {
		v := int32(pcm[i]) << resShift
		if v > maxval {
			maxval = v
		}
		if v < minval {
			minval = v
		}
	}
	if -minval > maxval {
		return -minval
	}
	return maxval
}

// hysteresisDecision ports celt/bands.c hysteresis_decision over integer
// thresholds (the equiv_rate/1000 intensity decision uses opus_val16 == int but
// the table values fit; the comparisons are exact).
func hysteresisDecision(val int, thresholds, hysteresis []int, n, prev int) int {
	i := 0
	for ; i < n; i++ {
		if val < thresholds[i] {
			break
		}
	}
	if i > prev && val < thresholds[prev]+hysteresis[prev] {
		i = prev
	}
	if i < prev && val > thresholds[prev-1]-hysteresis[prev-1] {
		i = prev
	}
	return i
}
