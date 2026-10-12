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
	if d.sampleRate == 96000 && d.synthOverlap == 240 {
		// The native 96 kHz mode uses shortMdctSize=240 for every duration.
		// PLC and transition frames can therefore be shorter than the static
		// 1920-sample frame while retaining the native mode's LM geometry.
		nbShort := frameSize / 240
		lm := 0
		for 1<<lm < nbShort {
			lm++
		}
		return ModeConfig{
			FrameSize:   frameSize,
			ShortBlocks: nbShort,
			LM:          lm,
			EffBands:    MaxBands,
			MDCTSize:    frameSize,
		}
	}
	return GetModeConfig(frameSize)
}

// effectiveEndBand returns the decode end band for the active mode, clamped to
// the custom effEBands when a custom mode is active.
func (d *Decoder) effectiveEndBand(frameSize int) int {
	mode := d.modeConfig(frameSize)
	if d.customEndBand > 0 {
		return min(max(int(d.customEndBand), 1), mode.EffBands)
	}
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
	if d.sampleRate == 96000 && d.synthOverlap == 240 {
		return frameSize == 240 || frameSize == 480 || frameSize == 960 || frameSize == 1920
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

// synthesizeChannelWithOverlapScratchF32 is one channel of libopus
// celt_synthesis(): the inverse MDCT (or the shortBlocks interleaved short
// inverse MDCTs of a transient frame) of coeffs, overlap-added with
// prevOverlap, written to out[:len(coeffs)+overlap]. prevOverlap may be
// out[:overlap] itself, as it is for the decoder's out_syn in decode_mem.
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

	if transient && shortBlocks > 1 && scratchF32 != nil && frameSize%shortBlocks == 0 && overlap%2 == 0 && len(shortCoeffs) >= frameSize/shortBlocks {
		// Like celt_synthesis, each short block's IMDCT reads its interleaved
		// coefficients in place and writes straight into out: out[:overlap]
		// starts as the previous frame's overlap, the blocks fill
		// out[overlap/2 : frameSize+overlap/2], and the unwritten tail of the
		// new overlap is zero.
		for i := range overlap {
			out[i] = float32(prevOverlap[i])
		}
		clear(out[frameSize+overlap/2 : needed])
		shortSize := frameSize / shortBlocks
		for b := range shortBlocks {
			imdctShortBlockInto(coeffs, b, shortBlocks, shortSize, out, b*shortSize, overlap, scratchF32, shortCoeffs)
		}
		return out[:needed]
	}
	if transient && shortBlocks > 1 {
		copy(out[:overlap], prevOverlap)
		clear(out[overlap:needed])

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

	if scratchF32 != nil {
		// Like celt_synthesis, the IMDCT writes straight into out.
		n := 2 * frameSize
		tables := scratchF32.mdctLookup(n)
		var trig []float32
		var fftState *kissFFTState
		if tables != nil {
			trig, fftState = tables.trig, tables.fft
		} else {
			trig = getMDCTTrigF32(n)
		}
		imdctOverlapWithPrevInto(out[:needed], coeffs, prevOverlap, overlap, scratchF32, tables, trig, fftState)
		return out[:needed]
	}
	output = imdctOverlapWithPrevScratchF32Output32(coeffs, prevOverlap, overlap, scratchF32)
	if len(output) < needed {
		return nil
	}
	copy(out[:needed], output[:needed])
	return out[:needed]
}

// SynthesizeFloat32 runs celt_synthesis for one mono frame of coeffs against
// decode_mem's MDCT overlap and returns the len(coeffs) output samples. It
// stores the frame's new MDCT overlap in decode_mem and leaves the decoded
// history unchanged. It implements plc.CELTSynthesizer.
func (d *Decoder) SynthesizeFloat32(coeffs []float32, transient bool, shortBlocks int) []float32 {
	if len(coeffs) == 0 {
		return nil
	}
	return d.synthesizeOverlapOnly(0, coeffs, transient, shortBlocks, &d.scratchSynthF32, &d.scratchIMDCTF32)
}

// SynthesizeStereoFloat32 is SynthesizeFloat32 for a stereo frame, returning
// interleaved L/R output in decoder scratch.
func (d *Decoder) SynthesizeStereoFloat32(coeffsL, coeffsR []float32, transient bool, shortBlocks int) []float32 {
	if len(coeffsL) == 0 || len(coeffsR) == 0 || d.channels != 2 {
		return nil
	}
	outL := d.synthesizeOverlapOnly(0, coeffsL, transient, shortBlocks, &d.scratchSynthF32, &d.scratchIMDCTF32)
	outR := d.synthesizeOverlapOnly(1, coeffsR, transient, shortBlocks, &d.scratchSynthRF32, &d.scratchIMDCTF32R)
	n := min(len(outL), len(outR))
	stereo := ensureFloat32Slice(&d.scratchStereoF32, n*2)
	for i := range n {
		stereo[2*i] = outL[i]
		stereo[2*i+1] = outR[i]
	}
	return stereo
}

func (d *Decoder) synthesizeOverlapOnly(c int, coeffs []float32, transient bool, shortBlocks int, buf *[]float32, scratch *imdctScratchF32) []float32 {
	d.ensureDecodeMem()
	n := len(coeffs)
	overlap := d.synthOverlapLen()
	out := ensureFloat32Slice(buf, n+overlap)
	shortCoeffs := ensureFloat32Slice(&d.scratchShortCoeffsF32, n)
	tail := d.decodeMemChannel(c)[d.decodeMemHistoryLen():]
	synthesizeChannelWithOverlapScratchF32(coeffs, tail, overlap, transient, shortBlocks, out, scratch, shortCoeffs)
	copy(tail, out[n:n+overlap])
	return out[:n]
}
