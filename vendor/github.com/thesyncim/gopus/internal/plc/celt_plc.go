package plc

// CELT-side concealment for the plc package.
//
// This mirrors the spectral-fold loss strategy of libopus celt/celt_decoder.c
// celt_decode_lost: when a CELT frame is lost, the previous frame's per-band
// energies are decayed and the bands are refilled with shaped noise, then run
// through the normal IMDCT + overlap-add synthesis so the concealed frame keeps
// the spectral envelope of the last good frame while fading out. Hybrid frames
// conceal only the CELT high bands here; the low band is handled by the SILK
// path (silk_plc.go), matching the libopus Hybrid layer split.
import "github.com/thesyncim/gopus/internal/opusmath"

// EnergyDecayPerFrame is the linear factor applied to each band's energy per
// lost CELT frame so the concealed spectrum fades over consecutive losses.
// This plays the role of the per-frame energy decay in celt_decode_lost
// (libopus celt/celt_decoder.c), here expressed directly on the linear band
// energies rather than libopus' log-domain backgroundLogE decay.
const EnergyDecayPerFrame = 0.85

// pow10F32 returns 10^x in float32, used to convert stored band energies from
// the dB (log10) domain to linear power. Delegates to opusmath.Pow10F32.
func pow10F32(x float32) float32 {
	return opusmath.Pow10F32(x)
}

// sqrtF32 returns the float32 square root, used to turn a band's target linear
// power into an amplitude scale for the noise fill. Delegates to opusmath.SqrtF32.
func sqrtF32(x float32) float32 {
	return opusmath.SqrtF32(x)
}

// CELTDecoderState is the minimal view of a CELT decoder that concealment
// reads and writes: channel count, per-band energies (oldBandE in libopus
// celt/celt_decoder.c), the noise-fill RNG seed (st->rng), the de-emphasis
// filter memory, and the IMDCT overlap buffer. Exposing it as an interface lets
// the plc package conceal without importing the celt package (avoiding an
// import cycle), while still mutating the live decoder state across losses.
type CELTDecoderState interface {
	// Channels returns the number of channels (1 or 2).
	Channels() int
	// PrevEnergy returns the previous frame's band energies.
	PrevEnergy() []float32
	// SetPrevEnergy updates the previous energy state.
	SetPrevEnergy(energies []float32)
	// RNG returns the current RNG state.
	RNG() uint32
	// SetRNG sets the RNG state.
	SetRNG(seed uint32)
	// PreemphState returns the de-emphasis filter state.
	PreemphState() []float32
	// OverlapBuffer returns the overlap buffer for synthesis.
	OverlapBuffer() []float32
	// SetOverlapBuffer sets the overlap buffer.
	SetOverlapBuffer(samples []float32)
}

// celtPreemphSetter is an optional capability of a CELTDecoderState that lets
// the concealer persist the de-emphasis filter memory it advanced, so the
// filter stays continuous into the next decoded frame.
type celtPreemphSetter interface {
	SetPreemphState(samples []float32)
}

// CELTBandInfo describes the CELT critical-band layout the concealer needs:
// how many bands exist, where the Hybrid high band starts, and the bin span of
// each band at a given frame size. It corresponds to the eBands / mode tables
// in libopus celt/modes.c and celt/static_modes_float.h, exposed here as plain
// callbacks so the plc package need not import the celt package.
type CELTBandInfo struct {
	// MaxBands is the maximum number of frequency bands.
	MaxBands int
	// HybridStartBand is the first band for hybrid mode (bands 17-21).
	HybridStartBand int
	// EffBands returns effective bands for a frame size.
	EffBands func(frameSize int) int
	// BandStart returns the starting bin for a band at given frame size.
	BandStart func(band, frameSize int) int
	// BandEnd returns the ending bin for a band at given frame size.
	BandEnd func(band, frameSize int) int
	// ValidFrameSize checks if frame size is valid.
	ValidFrameSize func(frameSize int) bool
	// Overlap is the overlap size for synthesis.
	Overlap int
}

// defaultCELTEffBands returns the number of effective coded bands for a given
// CELT frame size (2.5/5/10/20 ms at 48 kHz), matching the per-LM band counts
// libopus derives from mode->effEBands / mode->nbShortMdcts.
func defaultCELTEffBands(frameSize int) int {
	switch frameSize {
	case 120:
		return 13
	case 240:
		return 17
	case 480:
		return 19
	case 960:
		return 21
	default:
		return 21
	}
}

// defaultCELTBandStart returns the first MDCT bin of a CELT band at the given
// frame size. The constants are the eBands boundaries from libopus
// celt/modes.c scaled by frameSize/960 (LM-relative band edges).
func defaultCELTBandStart(band, frameSize int) int {
	switch band {
	case 0:
		return 0
	case 1:
		return frameSize / 960
	case 2:
		return (2 * frameSize) / 960
	case 3:
		return (3 * frameSize) / 960
	case 4:
		return (4 * frameSize) / 960
	case 5:
		return (5 * frameSize) / 960
	case 6:
		return (6 * frameSize) / 960
	case 7:
		return (7 * frameSize) / 960
	case 8:
		return (8 * frameSize) / 960
	case 9:
		return (10 * frameSize) / 960
	case 10:
		return (12 * frameSize) / 960
	case 11:
		return (14 * frameSize) / 960
	case 12:
		return (16 * frameSize) / 960
	case 13:
		return (20 * frameSize) / 960
	case 14:
		return (24 * frameSize) / 960
	case 15:
		return (28 * frameSize) / 960
	case 16:
		return (34 * frameSize) / 960
	case 17:
		return (40 * frameSize) / 960
	case 18:
		return (48 * frameSize) / 960
	case 19:
		return (60 * frameSize) / 960
	case 20:
		return (78 * frameSize) / 960
	case 21:
		return (100 * frameSize) / 960
	default:
		return 0
	}
}

// defaultCELTBandEnd returns the exclusive end bin of a CELT band, which is the
// start of the next band (clamped to the frame size for the last band).
func defaultCELTBandEnd(band, frameSize int) int {
	if band < 0 || band >= 21 {
		return frameSize
	}
	return defaultCELTBandStart(band+1, frameSize)
}

// defaultCELTValidFrameSize reports whether frameSize is one of the four legal
// CELT block sizes at 48 kHz (2.5/5/10/20 ms).
func defaultCELTValidFrameSize(frameSize int) bool {
	return frameSize == 120 || frameSize == 240 || frameSize == 480 || frameSize == 960
}

// defaultCELTBandInfo returns the standard 21-band CELT layout (Hybrid high
// band starting at band 17, 120-sample overlap) used by the concealer when the
// caller does not supply a custom CELTBandInfo.
func defaultCELTBandInfo() CELTBandInfo {
	return CELTBandInfo{
		MaxBands:        21,
		HybridStartBand: 17,
		EffBands:        defaultCELTEffBands,
		BandStart:       defaultCELTBandStart,
		BandEnd:         defaultCELTBandEnd,
		ValidFrameSize:  defaultCELTValidFrameSize,
		Overlap:         120,
	}
}

// CELTSynthesizer turns the noise-filled MDCT coefficients into time-domain
// samples via IMDCT + windowing + overlap-add, the same back end the normal
// CELT decode uses (libopus celt/celt_decoder.c celt_synthesis). It is supplied
// by the decoder so concealment reuses the live window/overlap state.
type CELTSynthesizer interface {
	// SynthesizeFloat32 performs IMDCT synthesis for a mono frame.
	SynthesizeFloat32(coeffs []float32, transient bool, shortBlocks int) []float32
	// SynthesizeStereoFloat32 performs IMDCT synthesis for a stereo frame,
	// returning interleaved L/R samples.
	SynthesizeStereoFloat32(coeffsL, coeffsR []float32, transient bool, shortBlocks int) []float32
}

// celtConcealmentConfig selects between fullband CELT concealment (all coded
// bands filled) and Hybrid concealment (only the high bands from
// CELTBandInfo.HybridStartBand, since SILK conceals the low band).
type celtConcealmentConfig struct {
	hybrid bool
}

// ConcealCELT generates a full-band concealment frame for a lost CELT packet,
// mirroring the spectral-fold loss path of libopus celt/celt_decoder.c
// celt_decode_lost:
//
//  1. Decay the previous frame's per-band energies (EnergyDecayPerFrame).
//  2. Refill every coded band with noise shaped to the decayed energy.
//  3. Run the normal IMDCT + overlap-add synthesis.
//  4. Apply de-emphasis, threading the decoder filter state.
//
// The result keeps the spectral envelope of the last good frame while fading
// out, and the decoder's energy and RNG state are advanced so consecutive
// losses continue to decay. A nil decoder yields a silent mono frame; a
// fully faded gain yields silence sized for the decoder's channel count.
//
// Parameters:
//   - dec: CELT decoder state from the last good frame (read and updated)
//   - synth: CELT synthesizer for IMDCT (nil yields silence)
//   - frameSize: samples to generate at 48 kHz (120, 240, 480, or 960)
//   - fadeFactor: overall gain multiplier (0.0 to 1.0)
//
// Returns the concealed samples at 48 kHz, interleaved if stereo.
func ConcealCELT(dec CELTDecoderState, synth CELTSynthesizer, frameSize int, fadeFactor float32) []float32 {
	if dec == nil {
		return make([]float32, frameSize)
	}

	channels := dec.Channels()

	// If fade is effectively zero, return silence
	if fadeFactor < 0.001 {
		return make([]float32, frameSize*channels)
	}

	bandInfo := defaultCELTBandInfo()
	nbBands := bandInfo.EffBands(frameSize)

	// Get previous frame energy (will be decayed)
	prevEnergy := dec.PrevEnergy()

	// Create decayed energy for concealment
	concealEnergy := make([]float32, len(prevEnergy))
	for i := range prevEnergy {
		// Apply energy decay
		concealEnergy[i] = prevEnergy[i] * float32(EnergyDecayPerFrame)
	}

	// Generate noise-filled MDCT coefficients at the decayed energy levels
	var coeffs []float32
	var coeffsL, coeffsR []float32

	rng := dec.RNG()

	if channels == 2 {
		// Stereo: generate coefficients for both channels
		coeffsL = generateNoiseBands(celtChannelEnergies(concealEnergy, 0, bandInfo.MaxBands), nbBands, frameSize, &rng, fadeFactor, &bandInfo)
		coeffsR = generateNoiseBands(celtChannelEnergies(concealEnergy, 1, bandInfo.MaxBands), nbBands, frameSize, &rng, fadeFactor, &bandInfo)
	} else {
		// Mono: single set of coefficients
		coeffs = generateNoiseBands(concealEnergy, nbBands, frameSize, &rng, fadeFactor, &bandInfo)
	}

	// Synthesize using IMDCT + window + overlap-add
	var samples []float32
	if synth != nil {
		if channels == 2 {
			samples = synth.SynthesizeStereoFloat32(coeffsL, coeffsR, false, 1)
		} else {
			samples = synth.SynthesizeFloat32(coeffs, false, 1)
		}
	} else {
		// No synthesizer available - return zeros
		samples = make([]float32, frameSize*channels)
	}

	// Apply de-emphasis to maintain filter state continuity
	applyDeemphasisPLCToDecoderFloat32(samples, dec, channels)

	// Update decoder energy state for next concealment
	dec.SetPrevEnergy(concealEnergy)
	dec.SetRNG(rng)

	return samples
}

// celtChannelEnergies returns the maxBands band energies for channel ch
// (0 = first/left, 1 = second/right) from the interleaved-by-channel
// concealEnergy buffer, always returning exactly maxBands entries. For a
// correctly sized stereo buffer (len >= 2*maxBands) this is exactly
// concealEnergy[ch*maxBands : ch*maxBands+maxBands]; if the decoder handed back
// a short energy buffer, the missing tail is zero-padded so the noise fill
// treats those bands as silent rather than indexing out of bounds. Valid-input
// behavior is unchanged because the per-band fill reads at most maxBands bands.
func celtChannelEnergies(concealEnergy []float32, ch, maxBands int) []float32 {
	out := make([]float32, maxBands)
	start := ch * maxBands
	for i := range maxBands {
		if src := start + i; src < len(concealEnergy) {
			out[i] = concealEnergy[src]
		}
	}
	return out
}

// generateNoiseBands builds a full frame of MDCT coefficients by filling each
// coded band [0, nbBands) with unit-norm noise scaled to that band's target
// amplitude. Per band the stored dB energy is converted to linear power, scaled
// by fadeFactor^2 (energy is amplitude squared), and the coefficient scale is
// its square root. This is the fullband counterpart of the spectral noise fill
// in libopus celt_decode_lost; generateNoiseHybridBands is the high-band variant.
func generateNoiseBands(energies []float32, nbBands, frameSize int, rng *uint32, fadeFactor float32, bandInfo *CELTBandInfo) []float32 {
	// Number of MDCT bins = frameSize (CELT convention)
	coeffs := make([]float32, frameSize)

	for band := 0; band < nbBands && band < len(energies); band++ {
		// Get band boundaries
		startBin := bandInfo.BandStart(band, frameSize)
		endBin := bandInfo.BandEnd(band, frameSize)

		if startBin >= frameSize {
			break
		}
		if endBin > frameSize {
			endBin = frameSize
		}

		bandWidth := endBin - startBin
		if bandWidth <= 0 {
			continue
		}

		// Get target energy for this band (linear scale from dB)
		// prevEnergy is stored in dB, convert to linear
		energyLin := pow10F32(energies[band] * 0.1)

		// Apply fade factor to energy
		energyLin *= fadeFactor * fadeFactor // Square for energy (amplitude squared)

		// Generate random unit-norm vector for the band
		noise := generateNoiseBand(rng, bandWidth)

		// Normalize noise vector
		normalizeVector(noise)

		// Scale by energy (sqrt because coefficients are amplitude)
		scale := sqrtF32(energyLin)

		// Fill band with scaled noise
		for i := 0; i < bandWidth && startBin+i < frameSize; i++ {
			coeffs[startBin+i] = noise[i] * scale
		}
	}

	return coeffs
}

// generateNoiseBand draws bandWidth pseudo-random samples in roughly [-1, 1]
// using the same linear congruential generator CELT uses for its noise fill
// (celt_rng, libopus celt/celt.h), so concealment noise is deterministic and
// advances the shared decoder RNG seed identically.
func generateNoiseBand(rng *uint32, bandWidth int) []float32 {
	noise := make([]float32, bandWidth)

	for i := range noise {
		// LCG: same as libopus for reproducibility
		*rng = *rng*1664525 + 1013904223

		// Convert to [-1, 1] range
		// Use top bits for better randomness
		noise[i] = float32(int32(*rng)) * (1.0 / float32(1<<31))
	}

	return noise
}

// normalizeVector rescales v in place to unit L2 norm so a band's noise fill
// carries unit energy before being scaled to the band's target amplitude. A
// near-zero vector is left untouched to avoid division by zero.
func normalizeVector(v []float32) {
	// Compute L2 norm
	var norm float32
	for _, x := range v {
		norm += x * x
	}
	norm = sqrtF32(norm)

	if norm < 1e-10 {
		// Avoid division by zero - leave as is
		return
	}

	// Normalize
	invNorm := float32(1.0) / norm
	for i := range v {
		v[i] *= invNorm
	}
}

// applyDeemphasisPLCToDecoderFloat32 runs the CELT de-emphasis (the inverse of
// the encoder pre-emphasis, a one-pole filter with coefficient 0.85) over the
// concealed samples in place, threading and updating the decoder's per-channel
// filter memory so the concealed frame joins seamlessly with surrounding
// decoded frames. Mirrors the deemphasis step of libopus celt/celt_decoder.c.
func applyDeemphasisPLCToDecoderFloat32(samples []float32, dec CELTDecoderState, channels int) {
	if dec == nil || len(samples) == 0 || channels < 1 {
		return
	}
	state := dec.PreemphState()
	if len(state) < channels {
		return
	}
	const preemphCoef = float32(0.85)
	if channels == 1 {
		s := state[0]
		for i := range samples {
			tmp := samples[i] + preemphCoef*s
			samples[i] = tmp
			s = tmp
		}
		state[0] = s
	} else {
		stateL := state[0]
		stateR := state[1]
		for i := 0; i < len(samples)-1; i += 2 {
			left := samples[i] + preemphCoef*stateL
			samples[i] = left
			stateL = left
			right := samples[i+1] + preemphCoef*stateR
			samples[i+1] = right
			stateR = right
		}
		state[0] = stateL
		state[1] = stateR
	}
	if setter, ok := dec.(celtPreemphSetter); ok {
		setter.SetPreemphState(state)
	}
}

// ConcealCELTHybrid generates the CELT-layer concealment for a lost Hybrid
// frame, filling only the high bands (from CELTBandInfo.HybridStartBand, bands
// 17-21); the SILK path conceals the low band, matching the libopus Hybrid
// layer split (src/opus_decoder.c routing into celt/silk). It is otherwise the
// same decay + noise-fill + synthesis + de-emphasis pipeline as ConcealCELT.
//
// Parameters:
//   - dec: CELT decoder state from the last good frame (read and updated)
//   - synth: CELT synthesizer for IMDCT (nil yields silence)
//   - frameSize: samples to generate at 48 kHz (480 or 960 for Hybrid)
//   - fadeFactor: overall gain multiplier (0.0 to 1.0)
//
// Returns the concealed high-frequency samples at 48 kHz, interleaved if stereo.
func ConcealCELTHybrid(dec CELTDecoderState, synth CELTSynthesizer, frameSize int, fadeFactor float32) []float32 {
	if dec == nil {
		return make([]float32, frameSize)
	}
	channels := dec.Channels()
	outLen := frameSize * channels
	out := make([]float32, outLen)
	ConcealCELTHybridRawInto(out, dec, synth, frameSize, fadeFactor)
	if outLen > len(out) {
		outLen = len(out)
	}
	if outLen > 0 {
		applyDeemphasisPLCToDecoderFloat32(out[:outLen], dec, channels)
	}
	return out
}

// ConcealCELTHybridRawInto writes Hybrid CELT concealment into the caller's dst
// buffer and, unlike ConcealCELTHybrid, does not apply de-emphasis. This lets a
// decoder-owned path interleave postfilter/comb-filter and de-emphasis in the
// exact libopus order (celt/celt_decoder.c) over the combined SILK+CELT signal.
// dst is written up to frameSize*channels samples and zero-padded beyond.
func ConcealCELTHybridRawInto(dst []float32, dec CELTDecoderState, synth CELTSynthesizer, frameSize int, fadeFactor float32) {
	writeCELTConcealment(dst, dec, synth, frameSize, fadeFactor, celtConcealmentConfig{hybrid: true})
}

// minInt returns the smaller of two ints.
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// writeCELTConcealment is the shared core behind the fullband and Hybrid CELT
// entry points: it decays the band energies, synthesizes a noise-filled frame
// into dst (without de-emphasis), and commits the decayed energies and advanced
// RNG seed back to the decoder. A nil decoder or fully faded gain yields
// silence. cfg.hybrid selects high-band-only fill.
func writeCELTConcealment(
	dst []float32,
	dec CELTDecoderState,
	synth CELTSynthesizer,
	frameSize int,
	fadeFactor float32,
	cfg celtConcealmentConfig,
) {
	if dec == nil {
		zeroCELTConcealment(dst, frameSize)
		return
	}

	channels := dec.Channels()
	outLen := minInt(frameSize*channels, len(dst))
	if fadeFactor < 0.001 {
		zeroCELTConcealment(dst, outLen)
		return
	}

	concealEnergy := buildCELTConcealmentEnergies(dec.PrevEnergy())
	samples, rng := synthesizeCELTConcealment(dec, synth, frameSize, fadeFactor, concealEnergy, cfg.hybrid)
	copyCELTConcealment(dst, samples, outLen)

	dec.SetPrevEnergy(concealEnergy)
	dec.SetRNG(rng)
}

// buildCELTConcealmentEnergies returns a copy of the previous band energies
// scaled by EnergyDecayPerFrame, the decayed envelope used for this lost frame.
func buildCELTConcealmentEnergies(prevEnergy []float32) []float32 {
	concealEnergy := make([]float32, len(prevEnergy))
	for i := range prevEnergy {
		concealEnergy[i] = prevEnergy[i] * float32(EnergyDecayPerFrame)
	}
	return concealEnergy
}

// synthesizeCELTConcealment builds noise-filled MDCT coefficients at the
// decayed band energies (mono or stereo, fullband or Hybrid high-band) and runs
// them through the supplied synthesizer. It returns the time-domain samples and
// the advanced RNG seed. With a nil synthesizer it returns nil samples and the
// advanced seed so callers can still commit RNG state.
func synthesizeCELTConcealment(
	dec CELTDecoderState,
	synth CELTSynthesizer,
	frameSize int,
	fadeFactor float32,
	concealEnergy []float32,
	hybrid bool,
) ([]float32, uint32) {
	bandInfo := defaultCELTBandInfo()
	nbBands := bandInfo.EffBands(frameSize)
	channels := dec.Channels()
	rng := dec.RNG()

	var coeffs []float32
	var coeffsL, coeffsR []float32
	if channels == 2 {
		energyL := celtChannelEnergies(concealEnergy, 0, bandInfo.MaxBands)
		energyR := celtChannelEnergies(concealEnergy, 1, bandInfo.MaxBands)
		if hybrid {
			coeffsL = generateNoiseHybridBands(energyL, nbBands, frameSize, &rng, fadeFactor, &bandInfo)
			coeffsR = generateNoiseHybridBands(energyR, nbBands, frameSize, &rng, fadeFactor, &bandInfo)
		} else {
			coeffsL = generateNoiseBands(energyL, nbBands, frameSize, &rng, fadeFactor, &bandInfo)
			coeffsR = generateNoiseBands(energyR, nbBands, frameSize, &rng, fadeFactor, &bandInfo)
		}
	} else if hybrid {
		coeffs = generateNoiseHybridBands(concealEnergy, nbBands, frameSize, &rng, fadeFactor, &bandInfo)
	} else {
		coeffs = generateNoiseBands(concealEnergy, nbBands, frameSize, &rng, fadeFactor, &bandInfo)
	}

	if synth == nil {
		return nil, rng
	}
	if channels == 2 {
		return synth.SynthesizeStereoFloat32(coeffsL, coeffsR, false, 1), rng
	}
	return synth.SynthesizeFloat32(coeffs, false, 1), rng
}

// copyCELTConcealment copies up to outLen synthesized samples into dst,
// zero-padding any remainder (and emitting all-zero when there are no samples).
func copyCELTConcealment(dst, samples []float32, outLen int) {
	if len(samples) == 0 {
		zeroCELTConcealment(dst, outLen)
		return
	}

	n := minInt(outLen, len(samples))
	copy(dst[:outLen], samples[:n])
	for i := n; i < outLen; i++ {
		dst[i] = 0
	}
}

// zeroCELTConcealment zeroes the first limit samples of dst (clamped to its
// length), used to emit silence when concealment has no signal to produce.
func zeroCELTConcealment(dst []float32, limit int) {
	if limit > len(dst) {
		limit = len(dst)
	}
	for i := 0; i < limit; i++ {
		dst[i] = 0
	}
}

// generateNoiseHybridBands is the Hybrid-mode noise fill: identical to
// generateNoiseBands but starting at bandInfo.HybridStartBand (band 17), so only
// the CELT high band is concealed and bins below it stay zero. The SILK path
// reconstructs the low band of a Hybrid frame.
func generateNoiseHybridBands(energies []float32, nbBands, frameSize int, rng *uint32, fadeFactor float32, bandInfo *CELTBandInfo) []float32 {
	coeffs := make([]float32, frameSize)

	// Start at hybrid start band (17)
	startBand := bandInfo.HybridStartBand

	for band := startBand; band < nbBands && band < len(energies); band++ {
		startBin := bandInfo.BandStart(band, frameSize)
		endBin := bandInfo.BandEnd(band, frameSize)

		if startBin >= frameSize {
			break
		}
		if endBin > frameSize {
			endBin = frameSize
		}

		bandWidth := endBin - startBin
		if bandWidth <= 0 {
			continue
		}

		// Get target energy
		energyLin := pow10F32(energies[band] * 0.1)
		energyLin *= fadeFactor * fadeFactor

		// Generate and scale noise
		noise := generateNoiseBand(rng, bandWidth)
		normalizeVector(noise)
		scale := sqrtF32(energyLin)

		for i := 0; i < bandWidth && startBin+i < frameSize; i++ {
			coeffs[startBin+i] = noise[i] * scale
		}
	}

	return coeffs
}
