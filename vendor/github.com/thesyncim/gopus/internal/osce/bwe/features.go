package bwe

// Port of libopus 1.6.1 dnn/osce_features.c `osce_bwe_calculate_features`. The
// feature extractor turns the decoded 16 kHz SILK lowband (signed int16 PCM)
// into the 114-element per-10ms input vector consumed by BBWENet. Each 10 ms
// hop produces:
//
//   lmspec[0:32]  -- log-magnitude spectrogram on a 32-band ERB-style
//                    filterbank computed from a Hann-windowed 320-sample DFT.
//   instafreq[32:73]  -- normalised cross-power real parts for the first
//                        41 DFT bins (instantaneous-frequency cos).
//   instafreq[73:114] -- normalised cross-power imaginary parts (sin).
//
// libopus uses float32 kissfft (`opus_fft`) for the 320-point DFT; gopus reuses
// the same scalar mixed-radix kernel via the celt package's float32 Kiss FFT.

import (
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/opusmath"
)

// BWE feature-extractor geometry from `dnn/osce_config.h`. Kept here as
// package-level constants so callers can size feature buffers without having
// to import internal headers.
const (
	bweWindowSize      = 320                          // OSCE_BWE_WINDOW_SIZE
	bweHalfWindowSize  = 160                          // OSCE_BWE_HALF_WINDOW_SIZE
	bweMaxInstaFreqBin = 40                           // OSCE_BWE_MAX_INSTAFREQ_BIN
	bweNumBands        = 32                           // OSCE_BWE_NUM_BANDS
	bweFeatureDim      = 114                          // OSCE_BWE_FEATURE_DIM (== FeatureDim)
	bweSpecNumFreqs    = 161                          // OSCE_SPEC_NUM_FREQS (one-sided bins for 320-pt FFT)
	bweInstaFreqLen    = 2 * (bweMaxInstaFreqBin + 1) // 82
)

// centerBinsBWE is the 32-band filterbank centre-bin layout from
// `osce_features.c::center_bins_bwe`.
var centerBinsBWE = [bweNumBands]int{
	0, 5, 10, 15, 20, 25, 30, 35,
	40, 45, 50, 55, 60, 65, 70, 75,
	80, 85, 90, 95, 100, 105, 110, 115,
	120, 125, 130, 135, 140, 145, 150, 160,
}

// bandWeightsBWE mirrors `osce_features.c::band_weights_bwe`.
var bandWeightsBWE = [bweNumBands]float32{
	0.333333333, 0.200000000, 0.200000000, 0.200000000,
	0.200000000, 0.200000000, 0.200000000, 0.200000000,
	0.200000000, 0.200000000, 0.200000000, 0.200000000,
	0.200000000, 0.200000000, 0.200000000, 0.200000000,
	0.200000000, 0.200000000, 0.200000000, 0.200000000,
	0.200000000, 0.200000000, 0.200000000, 0.200000000,
	0.200000000, 0.200000000, 0.200000000, 0.200000000,
	0.200000000, 0.200000000, 0.133333333, 0.181818182,
}

// osceWindow is the 320-sample sine analysis window used by both the OSCE
// feature extractor and the BWE cross-fade. Values verbatim from
// `osce_features.c::osce_window`.
var osceWindow = [bweWindowSize]float32{
	0.004908718808, 0.014725683311, 0.024541228523, 0.034354408400, 0.044164277127,
	0.053969889210, 0.063770299562, 0.073564563600, 0.083351737332, 0.093130877450,
	0.102901041421, 0.112661287575, 0.122410675199, 0.132148264628, 0.141873117332,
	0.151584296010, 0.161280864678, 0.170961888760, 0.180626435180, 0.190273572448,
	0.199902370753, 0.209511902052, 0.219101240157, 0.228669460829, 0.238215641862,
	0.247738863176, 0.257238206902, 0.266712757475, 0.276161601717, 0.285583828929,
	0.294978530977, 0.304344802381, 0.313681740399, 0.322988445118, 0.332264019538,
	0.341507569661, 0.350718204573, 0.359895036535, 0.369037181064, 0.378143757022,
	0.387213886697, 0.396246695891, 0.405241314005, 0.414196874117, 0.423112513073,
	0.431987371563, 0.440820594212, 0.449611329655, 0.458358730621, 0.467061954019,
	0.475720161014, 0.484332517110, 0.492898192230, 0.501416360796, 0.509886201809,
	0.518306898929, 0.526677640552, 0.534997619887, 0.543266035038, 0.551482089078,
	0.559644990127, 0.567753951426, 0.575808191418, 0.583806933818, 0.591749407690,
	0.599634847523, 0.607462493302, 0.615231590581, 0.622941390558, 0.630591150148,
	0.638180132051, 0.645707604824, 0.653172842954, 0.660575126926, 0.667913743292,
	0.675187984742, 0.682397150168, 0.689540544737, 0.696617479953, 0.703627273726,
	0.710569250438, 0.717442741007, 0.724247082951, 0.730981620454, 0.737645704427,
	0.744238692572, 0.750759949443, 0.757208846506, 0.763584762206, 0.769887082016,
	0.776115198508, 0.782268511401, 0.788346427627, 0.794348361383, 0.800273734191,
	0.806121974951, 0.811892519997, 0.817584813152, 0.823198305781, 0.828732456844,
	0.834186732948, 0.839560608398, 0.844853565250, 0.850065093356, 0.855194690420,
	0.860241862039, 0.865206121757, 0.870086991109, 0.874883999665, 0.879596685080,
	0.884224593137, 0.888767277786, 0.893224301196, 0.897595233788, 0.901879654283,
	0.906077149740, 0.910187315596, 0.914209755704, 0.918144082372, 0.921989916403,
	0.925746887127, 0.929414632439, 0.932992798835, 0.936481041442, 0.939879024058,
	0.943186419177, 0.946402908026, 0.949528180593, 0.952561935658, 0.955503880820,
	0.958353732530, 0.961111216112, 0.963776065795, 0.966348024735, 0.968826845041,
	0.971212287799, 0.973504123096, 0.975702130039, 0.977806096779, 0.979815820533,
	0.981731107599, 0.983551773378, 0.985277642389, 0.986908548290, 0.988444333892,
	0.989884851171, 0.991229961288, 0.992479534599, 0.993633450666, 0.994691598273,
	0.995653875433, 0.996520189401, 0.997290456679, 0.997964603026, 0.998542563469,
	0.999024282300, 0.999409713092, 0.999698818696, 0.999891571247, 0.999987952167,
	0.999987952167, 0.999891571247, 0.999698818696, 0.999409713092, 0.999024282300,
	0.998542563469, 0.997964603026, 0.997290456679, 0.996520189401, 0.995653875433,
	0.994691598273, 0.993633450666, 0.992479534599, 0.991229961288, 0.989884851171,
	0.988444333892, 0.986908548290, 0.985277642389, 0.983551773378, 0.981731107599,
	0.979815820533, 0.977806096779, 0.975702130039, 0.973504123096, 0.971212287799,
	0.968826845041, 0.966348024735, 0.963776065795, 0.961111216112, 0.958353732530,
	0.955503880820, 0.952561935658, 0.949528180593, 0.946402908026, 0.943186419177,
	0.939879024058, 0.936481041442, 0.932992798835, 0.929414632439, 0.925746887127,
	0.921989916403, 0.918144082372, 0.914209755704, 0.910187315596, 0.906077149740,
	0.901879654283, 0.897595233788, 0.893224301196, 0.888767277786, 0.884224593137,
	0.879596685080, 0.874883999665, 0.870086991109, 0.865206121757, 0.860241862039,
	0.855194690420, 0.850065093356, 0.844853565250, 0.839560608398, 0.834186732948,
	0.828732456844, 0.823198305781, 0.817584813152, 0.811892519997, 0.806121974951,
	0.800273734191, 0.794348361383, 0.788346427627, 0.782268511401, 0.776115198508,
	0.769887082016, 0.763584762206, 0.757208846506, 0.750759949443, 0.744238692572,
	0.737645704427, 0.730981620454, 0.724247082951, 0.717442741007, 0.710569250438,
	0.703627273726, 0.696617479953, 0.689540544737, 0.682397150168, 0.675187984742,
	0.667913743292, 0.660575126926, 0.653172842954, 0.645707604824, 0.638180132051,
	0.630591150148, 0.622941390558, 0.615231590581, 0.607462493302, 0.599634847523,
	0.591749407690, 0.583806933818, 0.575808191418, 0.567753951426, 0.559644990127,
	0.551482089078, 0.543266035038, 0.534997619887, 0.526677640552, 0.518306898929,
	0.509886201809, 0.501416360796, 0.492898192230, 0.484332517110, 0.475720161014,
	0.467061954019, 0.458358730621, 0.449611329655, 0.440820594212, 0.431987371563,
	0.423112513073, 0.414196874117, 0.405241314005, 0.396246695891, 0.387213886697,
	0.378143757022, 0.369037181064, 0.359895036535, 0.350718204573, 0.341507569661,
	0.332264019538, 0.322988445118, 0.313681740399, 0.304344802381, 0.294978530977,
	0.285583828929, 0.276161601717, 0.266712757475, 0.257238206902, 0.247738863176,
	0.238215641862, 0.228669460829, 0.219101240157, 0.209511902052, 0.199902370753,
	0.190273572448, 0.180626435180, 0.170961888760, 0.161280864678, 0.151584296010,
	0.141873117332, 0.132148264628, 0.122410675199, 0.112661287575, 0.102901041421,
	0.093130877450, 0.083351737332, 0.073564563600, 0.063770299562, 0.053969889210,
	0.044164277127, 0.034354408400, 0.024541228523, 0.014725683311, 0.004908718808,
}

// FeatureState mirrors libopus `OSCEBWEFeatureState` (dnn/osce_structs.h):
// the 50%-overlap analysis maintains a 160-sample input history plus the
// previous frame's first 82 spectral values for the instantaneous-frequency
// cross-power computation.
type FeatureState struct {
	signalHistory [bweHalfWindowSize]float32
	lastSpec      [bweInstaFreqLen]float32
	primed        bool
}

// Reset clears the persistent buffers and primes the previous spectrum to
// match libopus `osce_bwe_reset`.
func (s *FeatureState) Reset() {
	if s == nil {
		return
	}
	s.signalHistory = [bweHalfWindowSize]float32{}
	s.lastSpec = [bweInstaFreqLen]float32{}
	s.primeLastSpec()
}

func (s *FeatureState) primeLastSpec() {
	for k := 0; k <= bweMaxInstaFreqBin; k++ {
		s.lastSpec[2*k] = 1e-9
	}
	s.primed = true
}

// CalculateFeatures populates `features` with libopus-compatible BBWENet input
// vectors derived from `xq16k`. The input is interpreted as signed int16 PCM
// at 16 kHz (the SILK lowband output). `xq16k` length must be a multiple of
// 160 (one 10 ms hop). `features` must hold `(len(xq16k)/160) * FeatureDim`
// floats; existing contents are overwritten.
func (s *FeatureState) CalculateFeatures(features []float32, xq16k []int16) {
	if s == nil {
		return
	}
	numSamples := len(xq16k)
	if numSamples == 0 || numSamples%bweHalfWindowSize != 0 {
		return
	}
	numFrames := numSamples / bweHalfWindowSize
	if len(features) < numFrames*bweFeatureDim {
		return
	}
	if !s.primed {
		s.primeLastSpec()
	}

	const fftScale = float32(1.0 / bweWindowSize)
	var (
		fftIn   [bweWindowSize]complex64
		fftOut  [bweWindowSize]complex64
		fftTmp  [bweWindowSize]celt.KissCpx
		buffer  [bweWindowSize]float32
		spec    [bweInstaFreqLen]float32
		magSpec [bweSpecNumFreqs]float32
	)

	for frame := range numFrames {
		base := frame * bweFeatureDim
		// Zero the output slot for this hop (libopus OPUS_CLEAR before fill).
		for i := range bweFeatureDim {
			features[base+i] = 0
		}
		lmspec := features[base : base+bweNumBands]
		instafreq := features[base+bweNumBands : base+bweFeatureDim]

		// Assemble 320-sample analysis window from the cached history + the
		// new 160-sample hop, normalised to [-1, 1].
		copy(buffer[:bweHalfWindowSize], s.signalHistory[:])
		x := xq16k[frame*bweHalfWindowSize : frame*bweHalfWindowSize+bweHalfWindowSize]
		for n := range bweHalfWindowSize {
			buffer[bweHalfWindowSize+n] = float32(x[n]) / 32768.0
		}
		// Update history with the new hop (the back half of `buffer`).
		copy(s.signalHistory[:], buffer[bweHalfWindowSize:bweWindowSize])

		// Apply the sine window in place.
		for n := range bweWindowSize {
			buffer[n] *= osceWindow[n]
		}

		// Forward 320-point DFT via the float32 kissfft kernel. libopus'
		// opus_fft applies st->scale during the bit-reverse copy, before the
		// butterflies, so scale the input here rather than the output.
		for n := range bweWindowSize {
			fftIn[n] = complex(buffer[n]*fftScale, 0)
		}
		celt.KissFFT32ToWithScratch(fftOut[:], fftIn[:], fftTmp[:])

		// Instantaneous frequency from the cross-power of the current frame
		// with the previous frame's first (bweMaxInstaFreqBin+1) bins.
		for k := 0; k <= bweMaxInstaFreqBin; k++ {
			// libopus stores the un-scaled DFT samples (*nfft) and tacks on
			// a 1e-9 bias to the real channel; the bias is harmless because
			// it is dwarfed by even small signals after the *nfft scaling.
			spec[2*k] = float32(bweWindowSize)*real(fftOut[k]) + 1e-9
			spec[2*k+1] = float32(bweWindowSize) * imag(fftOut[k])
			re1 := spec[2*k]
			im1 := spec[2*k+1]
			re2 := s.lastSpec[2*k]
			im2 := s.lastSpec[2*k+1]
			auxR := re1*re2 + im1*im2
			auxI := im1*re2 - re1*im2
			auxAbs := opusmath.SqrtF32(auxR*auxR + auxI*auxI)
			invAbs := 1.0 / (auxAbs + 1e-9)
			instafreq[k] = auxR * invAbs
			instafreq[k+bweMaxInstaFreqBin+1] = auxI * invAbs
		}

		// ERB-scale magnitude spectrogram on the first 161 bins of the DFT.
		for k := range bweSpecNumFreqs {
			re := real(fftOut[k])
			im := imag(fftOut[k])
			magSpec[k] = float32(bweWindowSize) * opusmath.SqrtF32(re*re+im*im)
		}
		applyFilterbankBWE(lmspec, magSpec[:])
		for k := range bweNumBands {
			lmspec[k] = opusmath.LogF32(lmspec[k] + 1e-9)
		}

		// Update the previous-frame spectrum buffer.
		copy(s.lastSpec[:], spec[:])
	}
}

// applyFilterbankBWE mirrors `osce_features.c::apply_filterbank` specialised
// to the 32-band BWE filterbank tables.
func applyFilterbankBWE(out, in []float32) {
	out[0] = 0
	for b := range bweNumBands - 1 {
		out[b+1] = 0
		w0 := bandWeightsBWE[b]
		w1 := bandWeightsBWE[b+1]
		c0 := centerBinsBWE[b]
		c1 := centerBinsBWE[b+1]
		span := float32(c1 - c0)
		for i := c0; i < c1; i++ {
			frac := float32(c1-i) / span
			out[b] += w0 * frac * in[i]
			out[b+1] += w1 * (1 - frac) * in[i]
		}
	}
	out[bweNumBands-1] += bandWeightsBWE[bweNumBands-1] * in[centerBinsBWE[bweNumBands-1]]
}
