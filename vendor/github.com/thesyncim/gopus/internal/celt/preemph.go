// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file provides the pre-emphasis filter and DC rejection for encoding.

package celt

import "math"

// DCRejectCutoffHz is the cutoff frequency for the DC rejection high-pass filter.
// libopus uses 3 Hz at the Opus encoder level.
// Reference: libopus src/opus_encoder.c line 2008
const DCRejectCutoffHz = 3

// DelayCompensation is the number of samples of lookahead for CELT.
// libopus uses Fs/250 = 192 samples at 48kHz (4ms).
// This provides a lookahead that allows for better transient handling.
// Reference: libopus src/opus_encoder.c delay_compensation
const DelayCompensation = 192

// CELTSigScale is the internal signal scale used by CELT.
// Input samples in float range [-1.0, 1.0] are scaled up by this factor
// for internal processing, matching libopus CELT_SIG_SCALE.
const CELTSigScale = 32768.0

func rawMaxAbsStep(maxVal, minVal, sample float32) (float32, float32) {
	// celt_maxabs16 updates both extrema with MAX16/MIN16. Their second
	// operand wins a tie or an unordered comparison, including signed zero.
	if !(maxVal > sample) {
		maxVal = sample
	}
	if !(minVal < sample) {
		minVal = sample
	}
	return maxVal, minVal
}

// rawMaxMinScanScalar is celt_maxabs16's sequential MAX16/MIN16 loop.
func rawMaxMinScanScalar(x []float32, maxVal, minVal float32) (float32, float32) {
	for _, sample := range x {
		maxVal, minVal = rawMaxAbsStep(maxVal, minVal, sample)
	}
	return maxVal, minVal
}

// rawMaxMinScanStuffed folds native samples [from, to) into the running
// MAX16/MIN16 extrema. pcm is the zero-stuffed core frame, where native
// sample i of the channels-interleaved input sits at
// (i/channels)*upsample*channels + i%channels; the scan stops at the end of
// pcm.
func rawMaxMinScanStuffed(pcm []float32, from, to, channels, upsample int, maxVal, minVal float32) (float32, float32) {
	if from >= to {
		return maxVal, minVal
	}
	c := from % channels
	index := (from/channels)*upsample*channels + c
	skip := (upsample - 1) * channels
	for i := from; i < to && index < len(pcm); i++ {
		maxVal, minVal = rawMaxAbsStep(maxVal, minVal, pcm[index])
		index++
		if c++; c == channels {
			c = 0
			index += skip
		}
	}
	return maxVal, minVal
}

// preemphMonoScalar is celt_preemphasis's single-tap loop over mono pcm: m =
// coef*s carries into the next output.
func preemphMonoScalar(pcm, out []float32, coef, m float32) float32 {
	out = out[:len(pcm)]
	for i, v := range pcm {
		scaled := v * float32(CELTSigScale)
		out[i] = scaled - m
		m = coef * scaled
	}
	return m
}

// preemphStereoPlanarScalar is celt_preemphasis's single-tap loop for both
// channels of interleaved stereo pcm, writing each channel to its own planar
// output the way celt_encode_with_ec fills in+c*(N+overlap)+overlap.
func preemphStereoPlanarScalar(pcm, outL, outR []float32, coef float32, state [2]float32) [2]float32 {
	outR = outR[:len(outL)]
	src := pcm[:2*len(outL)]
	mL, mR := state[0], state[1]
	for i := range outL {
		// src advances one sample pair per step, so each iteration checks
		// its bounds once.
		_ = src[1]
		scaledL := src[0] * float32(CELTSigScale)
		scaledR := src[1] * float32(CELTSigScale)
		src = src[2:]
		outL[i] = scaledL - mL
		outR[i] = scaledR - mR
		mL = coef * scaledL
		mR = coef * scaledR
	}
	return [2]float32{mL, mR}
}

func rawMaxAbsResult(maxVal, minVal float32) float32 {
	negativeMin := -minVal
	if maxVal > negativeMin {
		return maxVal
	}
	return negativeMin
}

// rawInputSilence follows celt_encoder.c's sample_max scans. C uses the coded
// channel count for the contiguous raw-input scan even when the physical PCM
// and pre-emphasis use two channels. The scans read the native-rate input:
// with native set pcm holds it directly; otherwise at sub-48 kHz rates pcm is
// zero-stuffed into the core frame and each scanned native sample maps through
// upsample.
func (e *Encoder) rawInputSilence(pcm []float32, frameSize, overlap int, native bool) bool {
	channels := int(e.channels)
	codedChannels := int(e.streamChannels)
	if codedChannels <= 0 || codedChannels > channels {
		codedChannels = channels
	}
	upsample := e.effectiveUpsample()
	firstEnd := codedChannels * (frameSize - overlap) / upsample
	overlapEnd := firstEnd + codedChannels*overlap/upsample
	var firstMaxVal, firstMinVal, overlapMaxVal, overlapMinVal float32
	if upsample == 1 || native {
		firstLimit := min(firstEnd, len(pcm))
		firstMaxVal, firstMinVal = rawMaxMinScan(pcm[:firstLimit], firstMaxVal, firstMinVal)
		overlapLimit := min(overlapEnd, len(pcm))
		overlapMaxVal, overlapMinVal = rawMaxMinScan(pcm[firstLimit:overlapLimit], overlapMaxVal, overlapMinVal)
	} else {
		firstMaxVal, firstMinVal = rawMaxMinScanStuffed(pcm, 0, firstEnd, channels, upsample, firstMaxVal, firstMinVal)
		overlapMaxVal, overlapMinVal = rawMaxMinScanStuffed(pcm, firstEnd, overlapEnd, channels, upsample, overlapMaxVal, overlapMinVal)
	}
	firstMax := rawMaxAbsResult(firstMaxVal, firstMinVal)
	newOverlapMax := rawMaxAbsResult(overlapMaxVal, overlapMinVal)
	sampleMax := e.overlapMax
	if !(sampleMax > firstMax) {
		sampleMax = firstMax
	}
	e.overlapMax = newOverlapMax
	if !(sampleMax > newOverlapMax) {
		sampleMax = newOverlapMax
	}
	silenceThreshold := float32(math.Ldexp(1, -int(e.lsbDepth)))
	return sampleMax <= silenceThreshold
}

// noFMA32Mul, noFMA32Add and noFMA32Sub are mul32, add32 and sub32 spelled
// out, so each call inlines as a single level.
func noFMA32Mul(a, b float32) float32 {
	return float32(a * b)
}

func noFMA32Add(a, b float32) float32 {
	return float32(a + b)
}

func noFMA32Sub(a, b float32) float32 {
	return float32(a - b)
}

// ApplyPreemphasis applies the pre-emphasis filter to PCM input samples.
// Pre-emphasis boosts high frequencies to improve coding efficiency.
//
// The filter equation is:
//
//	y[n] = x[n] - PreemphCoef * x[n-1]
//
// This is the inverse of the decoder's de-emphasis filter:
//
//	y[n] = x[n] + PreemphCoef * y[n-1]
//
// The filter state is maintained in e.preemphState for frame continuity.
//
// Parameters:
//   - pcm: input PCM samples (interleaved if stereo)
//
// Returns: pre-emphasized samples
//
// Reference: RFC 6716 Section 4.3.5, libopus celt/celt_encoder.c
// Uses PreemphCoef = 0.85 (D03-05-03)
func (e *Encoder) ApplyPreemphasis(pcm []float32) []float32 {
	if len(pcm) == 0 {
		return nil
	}

	output := make([]float32, len(pcm))

	coef := float32(PreemphCoef)
	if e.channels == 1 {
		// Mono pre-emphasis
		state := float32(e.preemphState[0])
		for i := range pcm {
			x := pcm[i]
			output[i] = x - state
			state = coef * x
		}
		e.preemphState[0] = celtSig(state)
	} else {
		// Stereo pre-emphasis (interleaved samples)
		stateL := float32(e.preemphState[0])
		stateR := float32(e.preemphState[1])

		for i := 0; i < len(pcm)-1; i += 2 {
			// Left channel
			xL := pcm[i]
			output[i] = xL - stateL
			stateL = coef * xL

			// Right channel
			xR := pcm[i+1]
			output[i+1] = xR - stateR
			stateR = coef * xR
		}

		e.preemphState[0] = celtSig(stateL)
		e.preemphState[1] = celtSig(stateR)
	}

	return output
}

// ApplyPreemphasisInPlace applies pre-emphasis in-place to the input samples.
// This is more efficient when a copy is not needed.
func (e *Encoder) ApplyPreemphasisInPlace(pcm []float32) {
	if len(pcm) == 0 {
		return
	}

	coef := float32(PreemphCoef)
	if e.channels == 1 {
		// Mono pre-emphasis
		state := float32(e.preemphState[0])
		for i := range pcm {
			x := pcm[i]
			pcm[i] = x - state
			state = coef * x
		}
		e.preemphState[0] = celtSig(state)
	} else {
		// Stereo pre-emphasis (interleaved samples)
		stateL := float32(e.preemphState[0])
		stateR := float32(e.preemphState[1])

		for i := 0; i < len(pcm)-1; i += 2 {
			// Left channel
			xL := pcm[i]
			pcm[i] = xL - stateL
			stateL = coef * xL

			// Right channel
			xR := pcm[i+1]
			pcm[i+1] = xR - stateR
			stateR = coef * xR
		}

		e.preemphState[0] = celtSig(stateL)
		e.preemphState[1] = celtSig(stateR)
	}
}

// applyPreemphasisWithScalingCore applies pre-emphasis with signal scaling to the output buffer.
// This is the shared core logic for both allocating and scratch-based versions.
// Input samples are scaled from float range [-1.0, 1.0] to signal scale
// (multiplied by CELTSigScale = 32768), then the pre-emphasis filter is applied.
func (e *Encoder) applyPreemphasisWithScalingCore(pcm []float32, output []float32) {
	coef := float32(PreemphCoef)

	if e.channels == 1 {
		// Mono pre-emphasis with scaling
		state := float32(e.preemphState[0])
		for i := range pcm {
			// Scale input to signal scale and apply pre-emphasis
			// Match libopus float math: cast to float32 before scaling.
			scaled := pcm[i] * float32(CELTSigScale)
			output[i] = scaled - state
			state = coef * scaled
		}
		e.preemphState[0] = celtSig(state)
	} else {
		// Stereo pre-emphasis (interleaved samples) with scaling
		stateL := float32(e.preemphState[0])
		stateR := float32(e.preemphState[1])

		for i := 0; i < len(pcm)-1; i += 2 {
			// Left channel
			scaledL := pcm[i] * float32(CELTSigScale)
			output[i] = scaledL - stateL
			stateL = coef * scaledL

			// Right channel
			scaledR := pcm[i+1] * float32(CELTSigScale)
			output[i+1] = scaledR - stateR
			stateR = coef * scaledR
		}

		e.preemphState[0] = celtSig(stateL)
		e.preemphState[1] = celtSig(stateR)
	}
}

// applyPreemphasisWithScalingAndSilenceCore runs celt_encode_with_ec's
// sample_max silence scan and celt_preemphasis over channels-interleaved pcm.
// in is celt_encode_with_ec's planar buffer: channel c occupies
// in[c*(frameSize+overlap):(c+1)*(frameSize+overlap)], and pre-emphasis fills
// the frameSize samples after that channel's overlap head.
func (e *Encoder) applyPreemphasisWithScalingAndSilenceCore(pcm, in []float32, frameSize, overlap int) bool {
	if frameSize <= 0 || e.channels <= 0 || len(pcm) == 0 {
		e.overlapMax = 0
		return true
	}
	overlap = min(max(overlap, 0), frameSize)
	channels := int(e.channels)
	stride := frameSize + overlap
	n := min(frameSize, len(pcm)/channels, len(in)/channels-overlap)
	if n <= 0 {
		e.overlapMax = 0
		return true
	}
	outL := in[overlap : overlap+n]
	var outR []float32
	if channels == 2 {
		outR = in[stride+overlap : stride+overlap+n]
	}
	pcm = pcm[:n*channels]

	// Native 96 kHz HD mode uses libopus's 2-tap pre-emphasis
	// (celt_preemphasis() coef[1] != 0 path). hd96kPreemph[1] == 0 selects the
	// single-tap 48 kHz path below, keeping it byte-identical.
	if e.hd96kPreemph[1] != 0 {
		silence := e.rawInputSilence(pcm, n, n-min(frameSize-overlap, n), false)
		e.applyPreemphasis2Tap(pcm, outL, outR)
		return silence
	}
	silence := e.rawInputSilence(pcm, frameSize, overlap, false)
	coef := float32(PreemphCoef)
	if channels == 1 {
		e.preemphState[0] = preemphMono(pcm, outL, coef, e.preemphState[0])
		return silence
	}
	state := preemphStereoPlanar(pcm, outL, outR, coef, [2]float32{e.preemphState[0], e.preemphState[1]})
	e.preemphState[0], e.preemphState[1] = state[0], state[1]
	return silence
}

// applyPreemphasisUpsampled is applyPreemphasisWithScalingAndSilenceCore for a
// sub-48 kHz frame whose pcm holds the native-rate input (frameSize/upsample
// samples per channel). celt_encode_with_ec scans that input for sample_max,
// and celt_preemphasis zero-stuffs it into the core frame, so native sample i
// filters at core position i*upsample and the positions between filter zeros.
func (e *Encoder) applyPreemphasisUpsampled(pcm, in []float32, frameSize, overlap int) bool {
	channels := int(e.channels)
	upsample := e.effectiveUpsample()
	overlap = min(max(overlap, 0), frameSize)
	stride := frameSize + overlap
	n := min(frameSize, len(pcm)/channels*upsample, len(in)/channels-overlap)
	if n <= 0 || upsample < 2 {
		e.overlapMax = 0
		return true
	}
	silence := e.rawInputSilence(pcm, frameSize, overlap, true)
	coef := float32(PreemphCoef)
	for c := range channels {
		out := in[c*stride+overlap:][:n]
		e.preemphState[c] = preemphUpsampled(pcm[c:], channels, upsample, out, coef, e.preemphState[c])
	}
	return silence
}

// preemphUpsampled is celt_preemphasis's single-tap loop over one channel of
// zero-stuffed input: out[j] takes native sample src[(j/upsample)*step] when j
// is a multiple of upsample and a zero sample otherwise, with the same scaled
// - m and m = coef*scaled steps as preemphMonoScalar. Past the first stuffed
// zero after a native sample both m and the output are the constants that
// zero input yields. upsample is at least 2. It returns the updated m.
func preemphUpsampled(src []float32, step, upsample int, out []float32, coef, m float32) float32 {
	var zero float32
	mZero := float32(coef * zero)
	outZero := zero - mZero
	groups := len(out) / upsample
	if groups > 0 {
		head := src[:(groups-1)*step+1]
		for g := 0; g < groups; g++ {
			j := g * upsample
			scaled := head[g*step] * float32(CELTSigScale)
			out[j] = scaled - m
			// The explicit conversion rounds m before the subtraction, as
			// the stuffed loop does, so no fused multiply-subtract forms.
			out[j+1] = zero - float32(coef*scaled)
			m = mZero
		}
		if upsample > 2 {
			for g := 0; g < groups; g++ {
				fill := out[g*upsample+2 : (g+1)*upsample]
				for k := range fill {
					fill[k] = outZero
				}
			}
		}
	}
	if j := groups * upsample; j < len(out) {
		// A partial final group filters the same way up to the end of out.
		scaled := src[groups*step] * float32(CELTSigScale)
		out[j] = scaled - m
		m = float32(coef * scaled)
		for k := j + 1; k < len(out); k++ {
			out[k] = zero - m
			m = mZero
		}
	}
	return m
}

// applyPreemphasis2Tap applies libopus's 2-tap CELT pre-emphasis
// (celt_preemphasis() coef[1] != 0 path) used by custom and 96 kHz modes to
// interleaved pcm, writing each channel to its planar output (outR is nil for
// mono).
//
// Float build (SIG_SHIFT=0, RES2SIG = CELT_SIG_SCALE*x):
//
//	x      = CELT_SIG_SCALE * pcm[i]
//	tmp    = coef2 * x
//	out[i] = tmp + m
//	m      = coef1*out[i] - coef0*tmp
//
// The second product rounds before the first product contracts with the
// subtraction, matching the selected C build of celt_preemphasis().
func (e *Encoder) applyPreemphasis2Tap(pcm, outL, outR []float32) {
	coef0 := e.hd96kPreemph[0]
	coef1 := e.hd96kPreemph[1]
	coef2 := e.hd96kPreemph[2]
	if outR == nil {
		m := float32(e.preemphState[0])
		pcm = pcm[:len(outL)]
		for i, v := range pcm {
			x := v * float32(CELTSigScale)
			tmp := noFMA32Mul(coef2, x)
			y := noFMA32Add(tmp, m)
			outL[i] = y
			m = fma32(coef1, y, -noFMA32Mul(coef0, tmp))
		}
		e.preemphState[0] = celtSig(m)
		return
	}
	mL := float32(e.preemphState[0])
	mR := float32(e.preemphState[1])
	pcm = pcm[:2*len(outL)]
	outR = outR[:len(outL)]
	for i := range outL {
		xL := pcm[2*i] * float32(CELTSigScale)
		xR := pcm[2*i+1] * float32(CELTSigScale)
		tmpL := noFMA32Mul(coef2, xL)
		tmpR := noFMA32Mul(coef2, xR)
		yL := noFMA32Add(tmpL, mL)
		yR := noFMA32Add(tmpR, mR)
		outL[i] = yL
		outR[i] = yR
		mL = fma32(coef1, yL, -noFMA32Mul(coef0, tmpL))
		mR = fma32(coef1, yR, -noFMA32Mul(coef0, tmpR))
	}
	e.preemphState[0] = celtSig(mL)
	e.preemphState[1] = celtSig(mR)
}

// ApplyPreemphasisWithScaling applies pre-emphasis with signal scaling.
// Input samples are first scaled from float range [-1.0, 1.0] to signal scale
// (multiplied by CELTSigScale = 32768), then the pre-emphasis filter is applied.
//
// This matches libopus celt_preemphasis() behavior where samples are scaled
// and filtered together. The decoder later divides back by the same scale.
func (e *Encoder) ApplyPreemphasisWithScaling(pcm []float32) []float32 {
	if len(pcm) == 0 {
		return nil
	}

	output := make([]float32, len(pcm))
	e.applyPreemphasisWithScalingCore(pcm, output)
	return output
}

// applyDCRejectCore applies DC rejection filter to the output buffer.
// This is the shared core logic for both allocating and scratch-based versions.
func (e *Encoder) applyDCRejectCore(pcm, output []float32) {
	// Coefficients: coef = 6.3 * cutoff / Fs
	// For 48kHz and 3Hz cutoff: coef = 6.3 * 3 / 48000 = 0.00039375
	// Use float32 math to match libopus float path: coef = 6.3f*cutoff_Hz/Fs.
	coef := float32(6.3) * float32(DCRejectCutoffHz) / float32(e.SampleRate())
	coef2 := float32(1.0) - coef
	verySmall := float32(1e-30) // Matches VERY_SMALL in libopus float build

	if e.channels == 1 {
		m0 := e.hpMem[0]
		for i := range pcm {
			x := pcm[i]
			y := x - m0
			output[i] = y
			m0 = coef*x + verySmall + coef2*m0
		}
		e.hpMem[0] = m0
	} else {
		// Stereo: interleaved samples
		m0 := e.hpMem[0]
		m1 := e.hpMem[1]
		for i := 0; i < len(pcm)-1; i += 2 {
			x0 := pcm[i]
			x1 := pcm[i+1]
			output[i] = x0 - m0
			output[i+1] = x1 - m1
			m0 = coef*x0 + verySmall + coef2*m0
			m1 = coef*x1 + verySmall + coef2*m1
		}
		e.hpMem[0] = m0
		e.hpMem[1] = m1
	}
}

// ApplyDCReject applies a DC rejection (high-pass) filter to remove DC offset.
// This matches libopus dc_reject() which is applied before CELT encoding.
//
// The filter is a simple first-order high-pass:
//
//	coef = 6.3 * cutoffHz / sampleRate
//	out[i] = x[i] - m
//	m = coef*x[i] + (1-coef)*m
//
// Reference: libopus src/opus_encoder.c dc_reject()
func (e *Encoder) ApplyDCReject(pcm []float32) []float32 {
	if len(pcm) == 0 {
		return nil
	}

	// Initialize hpMem if not already done
	channels := int(e.channels)
	if len(e.hpMem) < channels {
		e.hpMem = make([]opusVal32, channels)
	}

	output := make([]float32, len(pcm))
	e.applyDCRejectCore(pcm, output)
	return output
}

// applyDCRejectScratch applies DC rejection using pre-allocated scratch buffer.
// This avoids heap allocations in the hot path.
func (e *Encoder) applyDCRejectScratch(pcm []float32) []float32 {
	if len(pcm) == 0 {
		return nil
	}

	// Initialize hpMem if not already done
	channels := int(e.channels)
	if len(e.hpMem) < channels {
		e.hpMem = make([]opusVal32, channels)
	}

	output := ensureFloat32Slice(&e.scratch.dcRejectedF32, len(pcm))

	e.applyDCRejectCore(pcm, output)
	return output
}
