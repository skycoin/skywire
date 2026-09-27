package celt

// Overlap-add synthesis for CELT frame reconstruction.
// This file implements the final stage of CELT decoding: converting
// frequency-domain coefficients to time-domain audio samples with
// proper windowing and overlap-add for seamless frame concatenation.
//
// Reference: RFC 6716 Section 4.3.5, libopus celt/celt_decoder.c

// synthOverlapLen returns the overlap length the decode synthesis tail must use.
// It defaults to the 48 kHz fullband Overlap constant; the native 96 kHz HD
// mode threads overlap=240 via d.synthOverlap.
func (d *Decoder) synthOverlapLen() int {
	if d.synthOverlap > 0 {
		return d.synthOverlap
	}
	return Overlap
}

// modeConfig returns the frame-size-dependent ModeConfig for the active mode.
// For a custom mode in the Fs==400*shortMdctSize family it derives LM from the
// short-block decomposition (frameSize/customScaleBase), so 20 ms family frames
// (e.g. 24000/480) get LM=3/ShortBlocks=8 like libopus.
func (d *Decoder) modeConfig(frameSize int) ModeConfig {
	if d.customScaleBase > 0 {
		nbShort := frameSize / d.customScaleBase
		lm := 0
		for (1 << lm) < nbShort {
			lm++
		}
		eff := MaxBands
		if d.customEffBands > 0 {
			eff = d.customEffBands
		}
		return ModeConfig{
			FrameSize:   frameSize,
			ShortBlocks: nbShort,
			LM:          lm,
			EffBands:    eff,
			MDCTSize:    frameSize,
		}
	}
	return GetModeConfig(frameSize)
}

// effectiveEndBand returns the decode end band for the active mode, clamped to
// the custom effEBands when a custom mode is active.
func (d *Decoder) effectiveEndBand(frameSize int) int {
	mode := d.modeConfig(frameSize)
	// A per-mode custom layout decodes the full effEBands range (libopus
	// opus_custom_decode sets st->end = mode->effEBands directly); there is no
	// Opus TOC bandwidth to clamp against.
	if d.perMode != nil {
		end := max(mode.EffBands, 1)
		return end
	}
	end := min(EffectiveBandsForFrameSize(d.bandwidth, frameSize), mode.EffBands)
	if end < 1 {
		end = 1
	}
	return end
}

// validFrameSize reports whether frameSize is acceptable for the active mode.
func (d *Decoder) validFrameSize(frameSize int) bool {
	if d.customScaleBase > 0 {
		return frameSize > 0 && frameSize%d.customScaleBase == 0
	}
	return ValidFrameSize(frameSize)
}

// OverlapAdd combines the current frame with the previous overlap.
// This is the core operation for continuous audio reconstruction in CELT.
//
// Parameters:
//   - current: windowed IMDCT output for current frame (2*frameSize samples)
//   - prevOverlap: tail samples from previous frame (overlap region)
//   - overlap: number of overlap samples (typically 120 for CELT)
//
// Returns:
//   - output: reconstructed samples (frameSize = len(current)/2)
//   - newOverlap: tail to save for next frame's overlap-add
//
// The MDCT/IMDCT overlap-add operation per RFC 6716:
// - IMDCT of N coefficients produces 2N windowed samples
// - Output per frame is N samples (frameSize)
// - First 'overlap' samples: sum current[0:overlap] + prevOverlap
// - Middle samples: copy from current[overlap:frameSize]
// - Save current[frameSize:frameSize+overlap] for next frame
func OverlapAdd(current, prevOverlap []float32, overlap int) (output, newOverlap []float32) {
	n := len(current) // 2*frameSize samples from IMDCT
	if n < 2*overlap {
		// Edge case: frame too short for proper overlap
		if n == 0 {
			return nil, prevOverlap
		}
		// For very short frames, output what we can
		frameSize := max(n/2, 1)
		output = make([]float32, frameSize)
		for i := 0; i < frameSize && i < len(prevOverlap); i++ {
			output[i] = prevOverlap[i] + current[i]
		}
		newOverlap = make([]float32, overlap)
		return output, newOverlap
	}

	// Output is frameSize = n/2 samples
	frameSize := n / 2
	output = make([]float32, frameSize)

	// First 'overlap' samples: sum with previous frame's saved tail
	for i := 0; i < overlap && i < len(prevOverlap); i++ {
		output[i] = prevOverlap[i] + current[i]
	}
	// If overlap exceeds prevOverlap length, just copy from current
	for i := len(prevOverlap); i < overlap; i++ {
		output[i] = current[i]
	}

	// Middle samples: direct copy from current[overlap : frameSize]
	copy(output[overlap:], current[overlap:frameSize])

	// Save new overlap: current[frameSize : frameSize+overlap]
	newOverlap = make([]float32, overlap)
	copy(newOverlap, current[frameSize:frameSize+overlap])

	return output, newOverlap
}

func synthesizeChannelWithOverlapScratchF32(coeffs []float32, prevOverlap []celtSig, overlap int, transient bool, shortBlocks int, out []float32, scratchF32 *imdctScratchF32, shortCoeffs []float32) (output []float32) {
	frameSize := len(coeffs)
	if frameSize == 0 {
		return nil
	}
	if overlap < 0 || len(prevOverlap) < overlap {
		return nil
	}
	if len(prevOverlap) > overlap {
		prevOverlap = prevOverlap[:overlap]
	}

	needed := frameSize + overlap
	if len(out) < needed {
		return nil
	}

	if transient && shortBlocks > 1 {
		clear(out[:needed])
		if overlap > 0 {
			for i := range overlap {
				out[i] = float32(prevOverlap[i])
			}
		}

		shortSize := frameSize / shortBlocks
		if shortSize <= 0 || len(shortCoeffs) < shortSize {
			return nil
		}

		if shortSize*shortBlocks == frameSize {
			for b := range shortBlocks {
				idx := b
				for i := range shortSize {
					shortCoeffs[i] = coeffs[idx]
					idx += shortBlocks
				}
				imdctInPlaceScratchF32Spectrum(shortCoeffs[:shortSize], out, b*shortSize, overlap, scratchF32)
			}
		} else {
			for b := range shortBlocks {
				for i := range shortSize {
					idx := b + i*shortBlocks
					if idx < frameSize {
						shortCoeffs[i] = coeffs[idx]
					} else {
						shortCoeffs[i] = 0
					}
				}
				imdctInPlaceScratchF32Spectrum(shortCoeffs[:shortSize], out, b*shortSize, overlap, scratchF32)
			}
		}

		return out[:needed]
	}

	output = imdctOverlapWithPrevScratchF32Output32(coeffs, prevOverlap, overlap, scratchF32)
	if len(output) < needed {
		return nil
	}
	copy(out[:needed], output[:needed])
	return out[:needed]
}

// Synthesize performs full IMDCT + windowing + overlap-add for decoded coefficients.
// This is the main synthesis function called by the decoder.
//
// Parameters:
//   - coeffs: MDCT coefficients from DecodeBands
//   - transient: true if frame uses short blocks (for transients)
//   - shortBlocks: number of short MDCTs if transient (1, 2, 4, or 8)
//
// Returns: PCM samples for this frame
func (d *Decoder) Synthesize(coeffs []float32, transient bool, shortBlocks int) []float32 {
	if len(coeffs) == 0 {
		return nil
	}
	overlap := d.synthOverlapLen()
	out := ensureFloat32Slice(&d.scratchSynthF32, len(coeffs)+overlap)
	shortCoeffs := ensureFloat32Slice(&d.scratchShortCoeffsF32, len(coeffs))
	output := synthesizeChannelWithOverlapScratchF32(coeffs, d.overlapBuffer, overlap, transient, shortBlocks, out, &d.scratchIMDCTF32, shortCoeffs)
	if len(output) == 0 {
		return nil
	}
	if overlap > 0 && len(output) >= len(coeffs)+overlap {
		copy(d.overlapBuffer[:overlap], output[len(coeffs):len(coeffs)+overlap])
	}
	return output[:len(coeffs)]
}

// SynthesizeFloat32 is Synthesize using decoder-owned scratch for the output,
// performing IMDCT, windowing and overlap-add for the decoded coefficients.
func (d *Decoder) SynthesizeFloat32(coeffs []float32, transient bool, shortBlocks int) []float32 {
	if len(coeffs) == 0 {
		return nil
	}
	out := ensureFloat32Slice(&d.scratchSynthF32, len(coeffs)+Overlap)
	shortCoeffs := ensureFloat32Slice(&d.scratchShortCoeffsF32, len(coeffs))
	output := synthesizeChannelWithOverlapScratchF32(coeffs, d.overlapBuffer, Overlap, transient, shortBlocks, out, &d.scratchIMDCTF32, shortCoeffs)
	if len(output) == 0 {
		return nil
	}
	if Overlap > 0 && len(output) >= len(coeffs)+Overlap {
		copy(d.overlapBuffer[:Overlap], output[len(coeffs):len(coeffs)+Overlap])
	}
	return output[:len(coeffs)]
}

func (d *Decoder) synthesizeMonoLongToFloat32(coeffs []float32) []float32 {
	if len(coeffs) == 0 {
		return nil
	}
	overlap := d.synthOverlapLen()
	if len(d.overlapBuffer) < overlap {
		buf := make([]celtSig, overlap)
		copy(buf, d.overlapBuffer)
		d.overlapBuffer = buf
	}

	outF32 := imdctOverlapWithPrevScratchF32Output32(coeffs, d.overlapBuffer[:overlap], overlap, &d.scratchIMDCTF32)
	if len(outF32) < len(coeffs)+overlap {
		return nil
	}
	if overlap > 0 {
		copy(d.overlapBuffer[:overlap], outF32[len(coeffs):len(coeffs)+overlap])
	}
	return outF32[:len(coeffs)]
}

func (d *Decoder) synthesizeStereoPlanarLongToFloat32(coeffsL, coeffsR []float32) (outL, outR []float32) {
	if len(coeffsL) == 0 || len(coeffsR) == 0 {
		return nil, nil
	}
	overlap := d.synthOverlapLen()
	if len(d.overlapBuffer) < overlap*2 {
		d.overlapBuffer = make([]celtSig, overlap*2)
	}
	overlapL := d.overlapBuffer[:overlap]
	overlapR := d.overlapBuffer[overlap : overlap*2]

	outLFull := imdctOverlapWithPrevScratchF32Output32(coeffsL, overlapL, overlap, &d.scratchIMDCTF32)
	outRFull := imdctOverlapWithPrevScratchF32Output32(coeffsR, overlapR, overlap, &d.scratchIMDCTF32R)
	if len(outLFull) < len(coeffsL)+overlap || len(outRFull) < len(coeffsR)+overlap {
		return nil, nil
	}
	if overlap > 0 {
		copy(overlapL, outLFull[len(coeffsL):len(coeffsL)+overlap])
		copy(overlapR, outRFull[len(coeffsR):len(coeffsR)+overlap])
	}
	return outLFull[:len(coeffsL)], outRFull[:len(coeffsR)]
}

func (d *Decoder) synthesizeStereoPlanar(coeffsL, coeffsR []float32, transient bool, shortBlocks int) (outL, outR []float32) {
	if len(coeffsL) == 0 && len(coeffsR) == 0 {
		return nil, nil
	}
	overlap := d.synthOverlapLen()
	if len(d.overlapBuffer) < overlap*2 {
		d.overlapBuffer = make([]celtSig, overlap*2)
	}
	overlapL := d.overlapBuffer[:overlap]
	overlapR := d.overlapBuffer[overlap : overlap*2]

	bufL := ensureFloat32Slice(&d.scratchSynthF32, len(coeffsL)+overlap)
	bufR := ensureFloat32Slice(&d.scratchSynthRF32, len(coeffsR)+overlap)
	shortCoeffs := ensureFloat32Slice(&d.scratchShortCoeffsF32, max(len(coeffsL), len(coeffsR)))
	outLFull := synthesizeChannelWithOverlapScratchF32(coeffsL, overlapL, overlap, transient, shortBlocks, bufL, &d.scratchIMDCTF32, shortCoeffs)
	outRFull := synthesizeChannelWithOverlapScratchF32(coeffsR, overlapR, overlap, transient, shortBlocks, bufR, &d.scratchIMDCTF32R, shortCoeffs)
	if len(outLFull) == 0 || len(outRFull) == 0 {
		return nil, nil
	}

	if overlap > 0 && len(outLFull) >= len(coeffsL)+overlap {
		copy(overlapL, outLFull[len(coeffsL):len(coeffsL)+overlap])
	}
	if overlap > 0 && len(outRFull) >= len(coeffsR)+overlap {
		copy(overlapR, outRFull[len(coeffsR):len(coeffsR)+overlap])
	}

	return outLFull[:len(coeffsL)], outRFull[:len(coeffsR)]
}

func (d *Decoder) synthesizeStereoPlanarFloat32(coeffsL, coeffsR []float32, transient bool, shortBlocks int) (outL, outR []float32) {
	if len(coeffsL) == 0 && len(coeffsR) == 0 {
		return nil, nil
	}
	if len(d.overlapBuffer) < Overlap*2 {
		d.overlapBuffer = make([]celtSig, Overlap*2)
	}
	overlapL := d.overlapBuffer[:Overlap]
	overlapR := d.overlapBuffer[Overlap : Overlap*2]

	bufL := ensureFloat32Slice(&d.scratchSynthF32, len(coeffsL)+Overlap)
	bufR := ensureFloat32Slice(&d.scratchSynthRF32, len(coeffsR)+Overlap)
	shortCoeffs := ensureFloat32Slice(&d.scratchShortCoeffsF32, max(len(coeffsL), len(coeffsR)))
	outLFull := synthesizeChannelWithOverlapScratchF32(coeffsL, overlapL, Overlap, transient, shortBlocks, bufL, &d.scratchIMDCTF32, shortCoeffs)
	outRFull := synthesizeChannelWithOverlapScratchF32(coeffsR, overlapR, Overlap, transient, shortBlocks, bufR, &d.scratchIMDCTF32R, shortCoeffs)
	if len(outLFull) == 0 || len(outRFull) == 0 {
		return nil, nil
	}

	if Overlap > 0 && len(outLFull) >= len(coeffsL)+Overlap {
		copy(overlapL, outLFull[len(coeffsL):len(coeffsL)+Overlap])
	}
	if Overlap > 0 && len(outRFull) >= len(coeffsR)+Overlap {
		copy(overlapR, outRFull[len(coeffsR):len(coeffsR)+Overlap])
	}

	return outLFull[:len(coeffsL)], outRFull[:len(coeffsR)]
}

func (d *Decoder) synthesizeStereoPlanarFromMonoLong(coeffs []float32) (outL, outR []float32) {
	if len(coeffs) == 0 {
		return nil, nil
	}
	if len(d.overlapBuffer) < Overlap*2 {
		d.overlapBuffer = make([]celtSig, Overlap*2)
	}
	overlapL := d.overlapBuffer[:Overlap]
	overlapR := d.overlapBuffer[Overlap : Overlap*2]

	outLFull := imdctOverlapWithPrevScratchF32Output32(coeffs, overlapL, Overlap, &d.scratchIMDCTF32)
	outRFull := imdctOverlapWithPrevScratchF32Output32(coeffs, overlapR, Overlap, &d.scratchIMDCTF32R)
	if len(outLFull) < len(coeffs)+Overlap || len(outRFull) < len(coeffs)+Overlap {
		return nil, nil
	}

	if Overlap > 0 {
		copy(overlapL, outLFull[len(coeffs):len(coeffs)+Overlap])
		copy(overlapR, outRFull[len(coeffs):len(coeffs)+Overlap])
	}
	return outLFull[:len(coeffs)], outRFull[:len(coeffs)]
}

// SynthesizeStereo performs synthesis for stereo frames.
// Handles both channels with proper interleaving.
//
// Parameters:
//   - coeffsL, coeffsR: MDCT coefficients for left and right channels
//   - transient: true if using short blocks
//   - shortBlocks: number of short MDCTs
//
// Returns: interleaved stereo samples [L0, R0, L1, R1, ...]
func (d *Decoder) SynthesizeStereo(coeffsL, coeffsR []float32, transient bool, shortBlocks int) []float32 {
	outputL, outputR := d.synthesizeStereoPlanar(coeffsL, coeffsR, transient, shortBlocks)
	if len(outputL) == 0 || len(outputR) == 0 {
		return nil
	}

	// Interleave stereo output
	n := min(len(outputR), len(outputL))

	stereo := ensureFloat32Slice(&d.scratchStereoF32, n*2)
	for i := 0; i < n; i++ {
		stereo[2*i] = outputL[i]
		stereo[2*i+1] = outputR[i]
	}

	return stereo[:n*2]
}

// SynthesizeStereoFloat32 is SynthesizeStereo using decoder-owned scratch,
// returning interleaved L/R output.
func (d *Decoder) SynthesizeStereoFloat32(coeffsL, coeffsR []float32, transient bool, shortBlocks int) []float32 {
	if len(coeffsL) == 0 || len(coeffsR) == 0 {
		return nil
	}
	outL, outR := d.synthesizeStereoPlanarFloat32(coeffsL, coeffsR, transient, shortBlocks)
	if len(outL) == 0 || len(outR) == 0 {
		return nil
	}
	n := min(len(outL), len(outR))
	stereo := ensureFloat32Slice(&d.scratchStereoF32, n*2)
	for i := range n {
		stereo[2*i] = outL[i]
		stereo[2*i+1] = outR[i]
	}
	return stereo[:n*2]
}

// WindowAndOverlap applies Vorbis window and performs overlap-add.
// This is a combined operation for efficiency.
//
// Parameters:
//   - imdctOut: raw IMDCT output (will be windowed in place)
//
// Returns: reconstructed samples after overlap-add
func (d *Decoder) WindowAndOverlap(imdctOut []float32) []float32 {
	if len(imdctOut) == 0 {
		return nil
	}

	frameSize := len(imdctOut) - Overlap
	if frameSize <= 0 {
		return nil
	}

	output := imdctOut[:frameSize]
	if frameSize+Overlap <= len(imdctOut) {
		copyFloat32ToSig(d.overlapBuffer, imdctOut[frameSize:frameSize+Overlap])
	}

	return output
}
