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

const (
	maxAbsF32SignBit = uint32(1) << 31
	maxAbsF32InfBits = uint32(0x7f800000)
)

func updateMaxAbsBitsF32(maxBits uint32, v float32) uint32 {
	bits := math.Float32bits(v) &^ maxAbsF32SignBit
	if bits <= maxAbsF32InfBits && bits > maxBits {
		return bits
	}
	return maxBits
}

func noFMA32Mul(a, b float32) float32 {
	return mul32(a, b)
}

func noFMA32Add(a, b float32) float32 {
	return add32(a, b)
}

func noFMA32Sub(a, b float32) float32 {
	return sub32(a, b)
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

func (e *Encoder) applyPreemphasisWithScalingAndSilenceCore(pcm []float32, output []float32, frameSize, overlap int) bool {
	if frameSize <= 0 || e.channels <= 0 || len(pcm) == 0 {
		e.overlapMax = 0
		return true
	}
	if overlap < 0 {
		overlap = 0
	}
	if overlap > frameSize {
		overlap = frameSize
	}

	channels := int(e.channels)
	total := min(frameSize*channels, len(pcm))
	if total > len(output) {
		total = len(output)
	}
	if total <= 0 {
		e.overlapMax = 0
		return true
	}

	split := min(max((frameSize-overlap)*channels, 0), total)

	// Native 96 kHz HD mode uses libopus's 2-tap pre-emphasis
	// (celt_preemphasis() coef[1] != 0 path). hd96kPreemph[1] == 0 selects the
	// single-tap 48 kHz path below, keeping it byte-identical.
	if e.hd96kPreemph[1] != 0 {
		return e.applyPreemphasis2TapAndSilenceCore(pcm, output, total, split, channels)
	}

	coef := float32(PreemphCoef)
	var firstMaxBits, overlapMaxBits uint32
	if channels == 1 {
		state := float32(e.preemphState[0])
		for i := 0; i < split; i++ {
			v := pcm[i]
			firstMaxBits = updateMaxAbsBitsF32(firstMaxBits, v)
			scaled := v * float32(CELTSigScale)
			y := scaled - state
			output[i] = y
			state = coef * scaled
		}
		for i := split; i < total; i++ {
			v := pcm[i]
			overlapMaxBits = updateMaxAbsBitsF32(overlapMaxBits, v)
			scaled := v * float32(CELTSigScale)
			y := scaled - state
			output[i] = y
			state = coef * scaled
		}
		e.preemphState[0] = celtSig(state)
	} else {
		stateL := float32(e.preemphState[0])
		stateR := float32(e.preemphState[1])
		i := 0
		for ; i+1 < split; i += 2 {
			vL := pcm[i]
			vR := pcm[i+1]
			firstMaxBits = updateMaxAbsBitsF32(firstMaxBits, vL)
			firstMaxBits = updateMaxAbsBitsF32(firstMaxBits, vR)
			scaledL := vL * float32(CELTSigScale)
			scaledR := vR * float32(CELTSigScale)
			yL := scaledL - stateL
			yR := scaledR - stateR
			output[i] = yL
			output[i+1] = yR
			stateL = coef * scaledL
			stateR = coef * scaledR
		}
		for ; i+1 < total; i += 2 {
			vL := pcm[i]
			vR := pcm[i+1]
			overlapMaxBits = updateMaxAbsBitsF32(overlapMaxBits, vL)
			overlapMaxBits = updateMaxAbsBitsF32(overlapMaxBits, vR)
			scaledL := vL * float32(CELTSigScale)
			scaledR := vR * float32(CELTSigScale)
			yL := scaledL - stateL
			yR := scaledR - stateR
			output[i] = yL
			output[i+1] = yR
			stateL = coef * scaledL
			stateR = coef * scaledR
		}
		e.preemphState[0] = celtSig(stateL)
		e.preemphState[1] = celtSig(stateR)
	}

	e.overlapMax = float32(0)
	if overlapMaxBits != 0 {
		e.overlapMax = math.Float32frombits(overlapMaxBits)
	}
	sampleMax := e.overlapMax
	firstMax := math.Float32frombits(firstMaxBits)
	if firstMax > sampleMax {
		sampleMax = firstMax
	}
	silenceThreshold := float32(math.Ldexp(1, -int(e.lsbDepth)))
	return sampleMax <= silenceThreshold
}

// applyPreemphasis2TapAndSilenceCore applies libopus's 2-tap CELT pre-emphasis
// (celt_preemphasis() coef[1] != 0 path) used by the native 96 kHz HD mode,
// while tracking the overlap-region silence max exactly as the single-tap path.
//
// Float build (SIG_SHIFT=0, RES2SIG = CELT_SIG_SCALE*x):
//
//	x      = CELT_SIG_SCALE * pcm[i]
//	tmp    = coef2 * x
//	out[i] = tmp + m
//	m      = coef1*out[i] - coef0*tmp
//
// with coef = HD96kMode.Preemph = {coef0, coef1, coef2, coef3}.
func (e *Encoder) applyPreemphasis2TapAndSilenceCore(pcm, output []float32, total, split, channels int) bool {
	coef0 := e.hd96kPreemph[0]
	coef1 := e.hd96kPreemph[1]
	coef2 := e.hd96kPreemph[2]

	var firstMaxBits, overlapMaxBits uint32
	if channels == 1 {
		m := float32(e.preemphState[0])
		for i := range total {
			v := pcm[i]
			if i < split {
				firstMaxBits = updateMaxAbsBitsF32(firstMaxBits, v)
			} else {
				overlapMaxBits = updateMaxAbsBitsF32(overlapMaxBits, v)
			}
			x := v * float32(CELTSigScale)
			tmp := noFMA32Mul(coef2, x)
			y := noFMA32Add(tmp, m)
			output[i] = y
			m = noFMA32Sub(noFMA32Mul(coef1, y), noFMA32Mul(coef0, tmp))
		}
		e.preemphState[0] = celtSig(m)
	} else {
		mL := float32(e.preemphState[0])
		mR := float32(e.preemphState[1])
		i := 0
		for ; i+1 < total; i += 2 {
			vL := pcm[i]
			vR := pcm[i+1]
			if i < split {
				firstMaxBits = updateMaxAbsBitsF32(firstMaxBits, vL)
				firstMaxBits = updateMaxAbsBitsF32(firstMaxBits, vR)
			} else {
				overlapMaxBits = updateMaxAbsBitsF32(overlapMaxBits, vL)
				overlapMaxBits = updateMaxAbsBitsF32(overlapMaxBits, vR)
			}
			xL := vL * float32(CELTSigScale)
			xR := vR * float32(CELTSigScale)
			tmpL := noFMA32Mul(coef2, xL)
			tmpR := noFMA32Mul(coef2, xR)
			yL := noFMA32Add(tmpL, mL)
			yR := noFMA32Add(tmpR, mR)
			output[i] = yL
			output[i+1] = yR
			mL = noFMA32Sub(noFMA32Mul(coef1, yL), noFMA32Mul(coef0, tmpL))
			mR = noFMA32Sub(noFMA32Mul(coef1, yR), noFMA32Mul(coef0, tmpR))
		}
		e.preemphState[0] = celtSig(mL)
		e.preemphState[1] = celtSig(mR)
	}

	e.overlapMax = float32(0)
	if overlapMaxBits != 0 {
		e.overlapMax = math.Float32frombits(overlapMaxBits)
	}
	sampleMax := e.overlapMax
	firstMax := math.Float32frombits(firstMaxBits)
	if firstMax > sampleMax {
		sampleMax = firstMax
	}
	silenceThreshold := float32(math.Ldexp(1, -int(e.lsbDepth)))
	return sampleMax <= silenceThreshold
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

// ApplyPreemphasisWithScalingScratch applies pre-emphasis with scaling using
// pre-allocated scratch buffers. This is the zero-allocation version of
// ApplyPreemphasisWithScaling, suitable for use from the hybrid encoding path.
func (e *Encoder) ApplyPreemphasisWithScalingScratch(pcm []float32) []float32 {
	return e.applyPreemphasisWithScalingScratch(pcm)
}

// applyPreemphasisWithScalingScratch applies pre-emphasis with scaling using scratch buffer.
func (e *Encoder) applyPreemphasisWithScalingScratch(pcm []float32) []float32 {
	if len(pcm) == 0 {
		return nil
	}

	// Use scratch buffer
	output := e.scratch.preemph
	if len(output) < len(pcm) {
		output = make([]float32, len(pcm))
		e.scratch.preemph = output
	}
	output = output[:len(pcm)]

	e.applyPreemphasisWithScalingCore(pcm, output)
	return output
}
