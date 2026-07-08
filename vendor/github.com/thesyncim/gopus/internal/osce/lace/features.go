package lace

// Port of libopus 1.6.1 dnn/osce_features.c `osce_calculate_features`. The
// feature extractor turns the decoded 16 kHz SILK lowband (signed int16 PCM)
// plus per-subframe SILK decoder control state into the 4 * 93 per-frame
// input vector consumed by LACE / NoLACE. Each subframe (80 samples / 5 ms)
// produces:
//
//   features[0:64]  -- log-spectrum from the SILK LPC coefficients projected
//                      onto the 64-band ERB-style "clean" filterbank
//                      (updated every other subframe; copied from prev otherwise).
//   features[64:82] -- DCT-II cepstrum of the noisy-signal log-magnitude
//                      spectrogram on the 18-band "noisy" filterbank
//                      (updated every other subframe).
//   features[82:87] -- normalised auto-correlation of the signal around the
//                      pitch lag at offsets {-2,-1,0,+1,+2}.
//   features[87:92] -- LTP coefficients from `psDecCtrl->LTPCoef_Q14`.
//   features[92]    -- log of the SILK subframe gain.
//
// Two additional outputs feed the feature net via the runtime's `numbits`
// and `periods` arrays:
//
//   numbits[0]      -- raw SILK payload bit count for the 20 ms frame.
//   numbits[1]      -- exponentially-smoothed `numbits` (alpha=0.9 mix).
//   periods[k]      -- pitch-lag for subframe k after libopus
//                      `pitch_postprocessing` (substitutes OSCE_NO_PITCH_VALUE
//                      on unvoiced frames).
//
// The extractor keeps a 350-sample input history so the per-subframe FFT
// (320-pt) can window the current subframe plus enough left context.
//
// libopus uses kissfft for the 320-pt DFT; gopus reuses the same scalar
// kernel via the celt package.

import (
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/opusmath"
)

// Public feature-layout constants (mirror libopus dnn/osce_config.h).
const (
	// FeatureDim is the per-subframe feature vector length (OSCE_FEATURE_DIM).
	FeatureDim = 93
	// SubframesPerFrame is the number of 5 ms subframes per 20 ms frame
	// (OSCE_MAX_FEATURE_FRAMES).
	SubframesPerFrame = 4
	// SubframeSize is the per-subframe sample count at 16 kHz (5 ms).
	SubframeSize = 80
	// FrameSize is the per-frame sample count at 16 kHz (20 ms).
	FrameSize = SubframesPerFrame * SubframeSize

	// FeaturesMaxHistory is the input-history depth (OSCE_FEATURES_MAX_HISTORY).
	FeaturesMaxHistory = 350

	// Layout offsets within one 93-float subframe feature vector.
	cleanSpecStart     = 0  // OSCE_CLEAN_SPEC_START
	cleanSpecLength    = 64 // OSCE_CLEAN_SPEC_LENGTH
	cleanSpecNumBands  = 64 // OSCE_CLEAN_SPEC_NUM_BANDS
	noisyCepstrumStart = 64 // OSCE_NOISY_CEPSTRUM_START
	noisyCepstrumLen   = 18 // OSCE_NOISY_CEPSTRUM_LENGTH
	noisySpecNumBands  = 18 // OSCE_NOISY_SPEC_NUM_BANDS == NB_BANDS
	acorrStart         = 82 // OSCE_ACORR_START
	acorrLen           = 5  // OSCE_ACORR_LENGTH
	ltpStart           = 87 // OSCE_LTP_START
	ltpLen             = 5  // OSCE_LTP_LENGTH (== SILK LTP_ORDER)
	logGainStart       = 92 // OSCE_LOG_GAIN_START

	// FFT geometry.
	specWindowSize = 320 // OSCE_SPEC_WINDOW_SIZE
	specNumFreqs   = 161 // OSCE_SPEC_NUM_FREQS

	// Pitch post-processing constants.
	noPitchValue  = 7 // OSCE_NO_PITCH_VALUE
	pitchHangover = 0 // OSCE_PITCH_HANGOVER (disabled in libopus 1.6.1)
	typeUnvoiced  = 0 // SILK signalType TYPE_NO_VOICE_ACTIVITY/TYPE_UNVOICED placeholders
	typeVoiced    = 2 // SILK TYPE_VOICED
)

// centerBinsClean is the 64-band clean filterbank centre-bin layout from
// `osce_features.c::center_bins_clean`.
var centerBinsClean = [cleanSpecNumBands]int{
	0, 2, 5, 8, 10, 12, 15, 18,
	20, 22, 25, 28, 30, 33, 35, 38,
	40, 42, 45, 48, 50, 52, 55, 58,
	60, 62, 65, 68, 70, 73, 75, 78,
	80, 82, 85, 88, 90, 92, 95, 98,
	100, 102, 105, 108, 110, 112, 115, 118,
	120, 122, 125, 128, 130, 132, 135, 138,
	140, 142, 145, 148, 150, 152, 155, 160,
}

// bandWeightsClean mirrors `osce_features.c::band_weights_clean`.
var bandWeightsClean = [cleanSpecNumBands]float32{
	0.666666666667, 0.400000000000, 0.333333333333, 0.400000000000,
	0.500000000000, 0.400000000000, 0.333333333333, 0.400000000000,
	0.500000000000, 0.400000000000, 0.333333333333, 0.400000000000,
	0.400000000000, 0.400000000000, 0.400000000000, 0.400000000000,
	0.500000000000, 0.400000000000, 0.333333333333, 0.400000000000,
	0.500000000000, 0.400000000000, 0.333333333333, 0.400000000000,
	0.500000000000, 0.400000000000, 0.333333333333, 0.400000000000,
	0.400000000000, 0.400000000000, 0.400000000000, 0.400000000000,
	0.500000000000, 0.400000000000, 0.333333333333, 0.400000000000,
	0.500000000000, 0.400000000000, 0.333333333333, 0.400000000000,
	0.500000000000, 0.400000000000, 0.333333333333, 0.400000000000,
	0.500000000000, 0.400000000000, 0.333333333333, 0.400000000000,
	0.500000000000, 0.400000000000, 0.333333333333, 0.400000000000,
	0.500000000000, 0.400000000000, 0.333333333333, 0.400000000000,
	0.500000000000, 0.400000000000, 0.333333333333, 0.400000000000,
	0.500000000000, 0.400000000000, 0.250000000000, 0.333333333333,
}

// centerBinsNoisy is the 18-band noisy filterbank centre-bin layout.
var centerBinsNoisy = [noisySpecNumBands]int{
	0, 4, 8, 12, 16, 20, 24, 28,
	32, 40, 48, 56, 64, 80, 96, 112,
	136, 160,
}

// bandWeightsNoisy mirrors `osce_features.c::band_weights_noisy`.
var bandWeightsNoisy = [noisySpecNumBands]float32{
	0.400000000000, 0.250000000000, 0.250000000000, 0.250000000000,
	0.250000000000, 0.250000000000, 0.250000000000, 0.250000000000,
	0.166666666667, 0.125000000000, 0.125000000000, 0.125000000000,
	0.083333333333, 0.062500000000, 0.062500000000, 0.050000000000,
	0.041666666667, 0.080000000000,
}

// osceFeatureWindow is the 320-sample sine analysis window used by the OSCE
// feature extractor for the noisy-cepstrum branch. Values verbatim from
// `osce_features.c::osce_window` -- same constant the BWE features port
// duplicates (libopus shares the single static table).
var osceFeatureWindow = [specWindowSize]float32{
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

// dctTable is the orthonormal DCT-II matrix used for the noisy cepstrum. The
// numbers mirror libopus `dnn/lpcnet_tables.c::dct_table` (NB_BANDS = 18).
// libopus stores it column-major: dct_table[j*NB_BANDS + i] is dct(j,i).
var dctTable = [noisySpecNumBands * noisySpecNumBands]float32{
	0.707106769, 0.996194720, 0.984807730, 0.965925813, 0.939692616,
	0.906307817, 0.866025388, 0.819152057, 0.766044438, 0.707106769,
	0.642787635, 0.573576450, 0.500000000, 0.422618270, 0.342020154,
	0.258819044, 0.173648179, 0.0871557444, 0.707106769, 0.965925813,
	0.866025388, 0.707106769, 0.500000000, 0.258819044, 6.12323426e-17,
	-0.258819044, -0.500000000, -0.707106769, -0.866025388, -0.965925813,
	-1.00000000, -0.965925813, -0.866025388, -0.707106769, -0.500000000,
	-0.258819044, 0.707106769, 0.906307817, 0.642787635, 0.258819044,
	-0.173648179, -0.573576450, -0.866025388, -0.996194720, -0.939692616,
	-0.707106769, -0.342020154, 0.0871557444, 0.500000000, 0.819152057,
	0.984807730, 0.965925813, 0.766044438, 0.422618270, 0.707106769,
	0.819152057, 0.342020154, -0.258819044, -0.766044438, -0.996194720,
	-0.866025388, -0.422618270, 0.173648179, 0.707106769, 0.984807730,
	0.906307817, 0.500000000, -0.0871557444, -0.642787635, -0.965925813,
	-0.939692616, -0.573576450, 0.707106769, 0.707106769, 6.12323426e-17,
	-0.707106769, -1.00000000, -0.707106769, -1.83697015e-16, 0.707106769,
	1.00000000, 0.707106769, 3.06161700e-16, -0.707106769, -1.00000000,
	-0.707106769, -4.28626385e-16, 0.707106769, 1.00000000, 0.707106769,
	0.707106769, 0.573576450, -0.342020154, -0.965925813, -0.766044438,
	0.0871557444, 0.866025388, 0.906307817, 0.173648179, -0.707106769,
	-0.984807730, -0.422618270, 0.500000000, 0.996194720, 0.642787635,
	-0.258819044, -0.939692616, -0.819152057, 0.707106769, 0.422618270,
	-0.642787635, -0.965925813, -0.173648179, 0.819152057, 0.866025388,
	-0.0871557444, -0.939692616, -0.707106769, 0.342020154, 0.996194720,
	0.500000000, -0.573576450, -0.984807730, -0.258819044, 0.766044438,
	0.906307817, 0.707106769, 0.258819044, -0.866025388, -0.707106769,
	0.500000000, 0.965925813, 3.06161700e-16, -0.965925813, -0.500000000,
	0.707106769, 0.866025388, -0.258819044, -1.00000000, -0.258819044,
	0.866025388, 0.707106769, -0.500000000, -0.965925813, 0.707106769,
	0.0871557444, -0.984807730, -0.258819044, 0.939692616, 0.422618270,
	-0.866025388, -0.573576450, 0.766044438, 0.707106769, -0.642787635,
	-0.819152057, 0.500000000, 0.906307817, -0.342020154, -0.965925813,
	0.173648179, 0.996194720, 0.707106769, -0.0871557444, -0.984807730,
	0.258819044, 0.939692616, -0.422618270, -0.866025388, 0.573576450,
	0.766044438, -0.707106769, -0.642787635, 0.819152057, 0.500000000,
	-0.906307817, -0.342020154, 0.965925813, 0.173648179, -0.996194720,
	0.707106769, -0.258819044, -0.866025388, 0.707106769, 0.500000000,
	-0.965925813, -4.28626385e-16, 0.965925813, -0.500000000, -0.707106769,
	0.866025388, 0.258819044, -1.00000000, 0.258819044, 0.866025388,
	-0.707106769, -0.500000000, 0.965925813, 0.707106769, -0.422618270,
	-0.642787635, 0.965925813, -0.173648179, -0.819152057, 0.866025388,
	0.0871557444, -0.939692616, 0.707106769, 0.342020154, -0.996194720,
	0.500000000, 0.573576450, -0.984807730, 0.258819044, 0.766044438,
	-0.906307817, 0.707106769, -0.573576450, -0.342020154, 0.965925813,
	-0.766044438, -0.0871557444, 0.866025388, -0.906307817, 0.173648179,
	0.707106769, -0.984807730, 0.422618270, 0.500000000, -0.996194720,
	0.642787635, 0.258819044, -0.939692616, 0.819152057, 0.707106769,
	-0.707106769, -1.83697015e-16, 0.707106769, -1.00000000, 0.707106769,
	5.51091070e-16, -0.707106769, 1.00000000, -0.707106769, -2.69484189e-15,
	0.707106769, -1.00000000, 0.707106769, -4.90477710e-16, -0.707106769,
	1.00000000, -0.707106769, 0.707106769, -0.819152057, 0.342020154,
	0.258819044, -0.766044438, 0.996194720, -0.866025388, 0.422618270,
	0.173648179, -0.707106769, 0.984807730, -0.906307817, 0.500000000,
	0.0871557444, -0.642787635, 0.965925813, -0.939692616, 0.573576450,
	0.707106769, -0.906307817, 0.642787635, -0.258819044, -0.173648179,
	0.573576450, -0.866025388, 0.996194720, -0.939692616, 0.707106769,
	-0.342020154, -0.0871557444, 0.500000000, -0.819152057, 0.984807730,
	-0.965925813, 0.766044438, -0.422618270, 0.707106769, -0.965925813,
	0.866025388, -0.707106769, 0.500000000, -0.258819044, 1.10280111e-15,
	0.258819044, -0.500000000, 0.707106769, -0.866025388, 0.965925813,
	-1.00000000, 0.965925813, -0.866025388, 0.707106769, -0.500000000,
	0.258819044, 0.707106769, -0.996194720, 0.984807730, -0.965925813,
	0.939692616, -0.906307817, 0.866025388, -0.819152057, 0.766044438,
	-0.707106769, 0.642787635, -0.573576450, 0.500000000, -0.422618270,
	0.342020154, -0.258819044, 0.173648179, -0.0871557444,
}

// FeatureControl bundles the per-frame SILK decoder-side inputs the LACE
// feature extractor reads. Field names mirror libopus `silk_decoder_control`.
// All fields have the libopus Q-format scale; the extractor converts them
// to float on read.
type FeatureControl struct {
	// PredCoefQ12 holds the two LPC prediction coefficient sets (Q12) for
	// the 20 ms frame; libopus drives `PredCoef_Q12[k>>1]` per subframe so
	// the first set serves subframes 0/1 and the second set serves 2/3.
	// Each set has `LPCOrder` entries (zero-padded if shorter).
	PredCoefQ12 [2][16]int16
	// LPCOrder is the active LPC order (10 for NB, 16 for MB/WB).
	LPCOrder int32
	// LTPCoefQ14 holds the per-subframe LTP filter coefficients (Q14).
	// Stored as a flat 4*5 = 20 array indexed by subframe*5 + tap.
	LTPCoefQ14 [SubframesPerFrame * ltpLen]int16
	// GainsQ16 are the per-subframe gains in Q16.
	GainsQ16 [SubframesPerFrame]int32
	// PitchL is the per-subframe pitch lag (in samples). Used as the raw
	// pitch input to the pitch post-processing step.
	PitchL [SubframesPerFrame]int32
	// SignalType is the SILK frame signalType (TYPE_VOICED / TYPE_UNVOICED).
	// Drives whether `pitch_postprocessing` substitutes OSCE_NO_PITCH_VALUE.
	SignalType int32
}

// FeatureState mirrors libopus `OSCEFeatureState`. Reset / Calculate update
// the persistent fields between successive 20 ms frames.
type FeatureState struct {
	numbitsSmooth      float32
	pitchHangoverCount int32
	lastLag            int32
	lastType           int32
	signalHistory      [FeaturesMaxHistory]float32
	// reset mirrors libopus `OSCEFeatureState.reset`: set on bypass and
	// consumed by the cross-fade logic outside the feature extractor.
	reset bool
}

// Reset zero-initialises the feature state, matching libopus `osce_reset`.
func (s *FeatureState) Reset() {
	if s == nil {
		return
	}
	s.numbitsSmooth = 0
	s.pitchHangoverCount = 0
	s.lastLag = 0
	s.lastType = 0
	s.signalHistory = [FeaturesMaxHistory]float32{}
	s.reset = true
}

// MarkConsumed clears the `reset` flag once the caller has taken the
// transition into account (mirrors libopus `psDec->osce.features.reset = 0`
// at the bottom of `osce_enhance_frame`).
func (s *FeatureState) MarkConsumed() {
	if s == nil {
		return
	}
	s.reset = false
}

// CalculateFeatures populates `features` (4 * 93), `numbits` (2) and
// `periods` (4) with libopus-compatible LACE / NoLACE input vectors for
// one 20 ms 16 kHz int16 input frame `xq16k` (length 320). `ctrl` carries
// the SILK decoder-side per-frame state used by the feature net.
//
// `numBits` is the raw bit count of the SILK payload for the 20 ms frame
// (matches the `num_bits` arg libopus passes to `osce_calculate_features`).
//
// The output buffers must be sized:
//
//	features: SubframesPerFrame * FeatureDim (= 372) float32 entries.
//	numbits:  2 float32 entries.
//	periods:  SubframesPerFrame int entries.
//
// CalculateFeatures returns false when the input buffers are misshapen.
func (s *FeatureState) CalculateFeatures(
	features []float32,
	numbits []float32,
	periods []int,
	xq16k []int16,
	ctrl *FeatureControl,
	numBits int32,
) bool {
	if s == nil || ctrl == nil {
		return false
	}
	if len(xq16k) != FrameSize {
		return false
	}
	if len(features) < SubframesPerFrame*FeatureDim {
		return false
	}
	if len(numbits) < 2 {
		return false
	}
	if len(periods) < SubframesPerFrame {
		return false
	}

	// Smoothed bit count, exactly as libopus
	// `osce_features.c::osce_calculate_features`.
	s.numbitsSmooth = 0.9*s.numbitsSmooth + 0.1*float32(numBits)
	numbits[0] = float32(numBits)
	numbits[1] = s.numbitsSmooth

	// Assemble the signal buffer: 350 samples of history + 320 new samples
	// (4 subframes * 80). The buffer drives the FFT-based cepstrum and the
	// auto-correlation around the pitch lag.
	var buffer [FeaturesMaxHistory + SubframesPerFrame*SubframeSize]float32
	copy(buffer[:FeaturesMaxHistory], s.signalHistory[:])
	for n := range FrameSize {
		buffer[FeaturesMaxHistory+n] = float32(xq16k[n]) / 32768.0
	}

	var (
		fftIn  [specWindowSize]complex64
		fftOut [specWindowSize]complex64
		fftTmp [specWindowSize]celt.KissCpx
		// Reused buffers across subframes.
		magBuf  [specWindowSize]float32
		invBuf  [specNumFreqs]float32
		cepBuf  [noisySpecNumBands]float32
		specBuf [noisySpecNumBands]float32
	)

	for k := range SubframesPerFrame {
		base := k * FeatureDim
		pfeatures := features[base : base+FeatureDim]
		for i := range pfeatures {
			pfeatures[i] = 0
		}
		frameOffset := FeaturesMaxHistory + k*SubframeSize

		// Clean spectrum from LPC (update every other subframe; copy previous
		// otherwise).  libopus picks PredCoef_Q12[k>>1] -- i.e. the same LPC
		// set serves subframes 0/1 and 2/3, but the LPC->spectrum projection
		// is still done only on k==0 and k==2.
		if k%2 == 0 {
			calculateLogSpectrumFromLPC(
				pfeatures[cleanSpecStart:cleanSpecStart+cleanSpecLength],
				ctrl.PredCoefQ12[k>>1][:],
				int(ctrl.LPCOrder),
				fftIn[:], fftOut[:], fftTmp[:], magBuf[:], invBuf[:],
			)
		} else {
			// Copy from previous subframe slot (which is exactly FeatureDim
			// floats earlier in the same `features` buffer).
			copy(
				features[base+cleanSpecStart:base+cleanSpecStart+cleanSpecLength],
				features[base+cleanSpecStart-FeatureDim:base+cleanSpecStart-FeatureDim+cleanSpecLength],
			)
		}

		// Noisy cepstrum from the signal (update every other subframe).
		// libopus windows `frame - 160`, i.e. one 10 ms hop into history so
		// the 320-pt window straddles the previous half-frame and the
		// current half-frame.
		if k%2 == 0 {
			calculateCepstrum(
				pfeatures[noisyCepstrumStart:noisyCepstrumStart+noisyCepstrumLen],
				buffer[frameOffset-160:frameOffset-160+specWindowSize],
				fftIn[:], fftOut[:], fftTmp[:], magBuf[:], cepBuf[:], specBuf[:],
			)
		} else {
			copy(
				features[base+noisyCepstrumStart:base+noisyCepstrumStart+noisyCepstrumLen],
				features[base+noisyCepstrumStart-FeatureDim:base+noisyCepstrumStart-FeatureDim+noisyCepstrumLen],
			)
		}

		// Pitch post-processing with hangover (currently a no-op in libopus
		// because OSCE_PITCH_HANGOVER==0 and OSCE_HANGOVER_BUGFIX is
		// undefined; we mirror the exact branch logic so a future libopus
		// bugfix flip just needs the constant change).
		periods[k] = pitchPostprocessing(s, ctrl.PitchL[k], ctrl.SignalType)

		// Auto-correlation around the pitch lag.
		calculateAcorr(
			pfeatures[acorrStart:acorrStart+acorrLen],
			buffer[frameOffset:frameOffset+SubframeSize],
			frameOffset, // absolute base for the "signal[n - lag + k]" reads
			buffer[:],
			periods[k],
		)

		// LTP coefficients.
		for i := range ltpLen {
			pfeatures[ltpStart+i] = float32(ctrl.LTPCoefQ14[k*ltpLen+i]) / float32(1<<14)
		}

		// Log of the SILK subframe gain.
		gain := float32(ctrl.GainsQ16[k]) / float32(1<<16)
		pfeatures[logGainStart] = opusmath.LogF32(gain + 1e-9)
	}

	// Signal history update: keep the trailing 350 samples of `buffer`.
	copy(s.signalHistory[:], buffer[FrameSize:FrameSize+FeaturesMaxHistory])
	return true
}

// calculateLogSpectrumFromLPC mirrors
// `osce_features.c::calculate_log_spectrum_from_lpc`. It builds the
// zero-expanded LPC polynomial, takes its magnitude spectrum, inverts it,
// applies the 64-band clean filterbank and converts to a scaled log domain.
func calculateLogSpectrumFromLPC(
	spec []float32,
	aQ12 []int16,
	lpcOrder int,
	fftIn, fftOut []complex64,
	fftTmp []celt.KissCpx,
	magBuf []float32,
	invBuf []float32,
) {
	// Zero-expanded LPC polynomial: [1, -a1, -a2, ..., 0, 0, ...].
	for i := range magBuf {
		magBuf[i] = 0
	}
	magBuf[0] = 1
	for i := range lpcOrder {
		magBuf[i+1] = -float32(aQ12[i]) / float32(1<<12)
	}

	// One-sided magnitude spectrum scaled by N (matches libopus
	// `mag_spec_320_onesided`: each bin is N*|X[k]|).
	magSpec320OneSided(magBuf[:specNumFreqs], magBuf, fftIn, fftOut, fftTmp)

	// Invert magnitude spectrum with the libopus 1e-9 bias.
	for i := range specNumFreqs {
		invBuf[i] = 1.0 / (magBuf[i] + 1e-9)
	}

	// 64-band clean filterbank.
	applyFilterbankClean(spec, invBuf)

	// Log + 0.3 scaling, libopus log(spec + 1e-9)*0.3.
	for i := range cleanSpecNumBands {
		spec[i] = 0.3 * opusmath.LogF32(spec[i]+1e-9)
	}
}

// calculateCepstrum mirrors `osce_features.c::calculate_cepstrum`. It
// windows the 320-sample input, takes the magnitude spectrum, projects
// onto the 18-band noisy filterbank, log-scales the band values and then
// runs an orthonormal DCT-II.
func calculateCepstrum(
	cepstrum []float32,
	signal []float32, // length specWindowSize
	fftIn, fftOut []complex64,
	fftTmp []celt.KissCpx,
	magBuf []float32,
	cepBuf []float32, // length noisySpecNumBands
	specBuf []float32, // length noisySpecNumBands
) {
	// Apply the sine window.
	for n := range specWindowSize {
		magBuf[n] = osceFeatureWindow[n] * signal[n]
	}

	// One-sided magnitude spectrum (writes to magBuf[:161]).
	magSpec320OneSided(magBuf[:specNumFreqs], magBuf, fftIn, fftOut, fftTmp)

	// 18-band noisy filterbank.
	applyFilterbankNoisy(specBuf, magBuf[:specNumFreqs])

	// log(spec + 1e-9).
	for n := range noisySpecNumBands {
		specBuf[n] = opusmath.LogF32(specBuf[n] + 1e-9)
	}

	// Orthonormal DCT-II: out[i] = sqrt(2/N) * sum_j in[j] * dct_table[j*N + i].
	scale := opusmath.SqrtF32(2.0 / float32(noisySpecNumBands))
	for i := range noisySpecNumBands {
		var sum float32
		for j := range noisySpecNumBands {
			sum += specBuf[j] * dctTable[j*noisySpecNumBands+i]
		}
		cepBuf[i] = sum * scale
	}
	copy(cepstrum[:noisySpecNumBands], cepBuf[:])
}

// magSpec320OneSided mirrors `osce_features.c::mag_spec_320_onesided`. The
// libopus kissfft kernel scales the output by 1/nfft, so the bin magnitude
// is multiplied by N to match the unscaled DFT magnitude libopus emits.
func magSpec320OneSided(
	out []float32,
	in []float32,
	fftIn, fftOut []complex64,
	fftTmp []celt.KissCpx,
) {
	const fftScale = float32(1.0 / specWindowSize)
	for n := range specWindowSize {
		fftIn[n] = complex(in[n]*fftScale, 0)
	}
	celt.KissFFT32ToWithScratch(fftOut, fftIn, fftTmp)
	for k := range specNumFreqs {
		re := real(fftOut[k])
		im := imag(fftOut[k])
		out[k] = float32(specWindowSize) * opusmath.SqrtF32(re*re+im*im)
	}
}

// applyFilterbankClean is the 64-band specialisation of the libopus
// `apply_filterbank` helper using the centerBinsClean / bandWeightsClean
// tables.
func applyFilterbankClean(out, in []float32) {
	applyFilterbank(out, in, centerBinsClean[:], bandWeightsClean[:], cleanSpecNumBands)
}

// applyFilterbankNoisy is the 18-band specialisation.
func applyFilterbankNoisy(out, in []float32) {
	applyFilterbank(out, in, centerBinsNoisy[:], bandWeightsNoisy[:], noisySpecNumBands)
}

// applyFilterbank mirrors `osce_features.c::apply_filterbank`: triangular
// overlap-add of the magnitude spectrum onto the supplied band layout.
func applyFilterbank(out, in []float32, centerBins []int, bandWeights []float32, numBands int) {
	out[0] = 0
	for b := 0; b < numBands-1; b++ {
		out[b+1] = 0
		w0 := bandWeights[b]
		w1 := bandWeights[b+1]
		c0 := centerBins[b]
		c1 := centerBins[b+1]
		span := float32(c1 - c0)
		for i := c0; i < c1; i++ {
			frac := float32(c1-i) / span
			out[b] += w0 * frac * in[i]
			out[b+1] += w1 * (1.0 - frac) * in[i]
		}
	}
	out[numBands-1] += bandWeights[numBands-1] * in[centerBins[numBands-1]]
}

// calculateAcorr mirrors `osce_features.c::calculate_acorr`. The cross-
// correlation is normalised by the geometric mean of the two energies; the
// lag offset ranges over {-2, -1, 0, +1, +2}.
//
// `signal` is the 80-sample current subframe (used for the xx energy and the
// x*y product). `signalAbsBase` is the absolute index of `signal[0]` inside
// the full buffer (so we can index `buffer[signalAbsBase + n - lag + k]`).
func calculateAcorr(
	acorr []float32,
	signal []float32, // 80 samples
	signalAbsBase int,
	buffer []float32,
	lag int,
) {
	for k := -2; k <= 2; k++ {
		var xx, xy, yy float32
		for n := range SubframeSize {
			x := signal[n]
			y := buffer[signalAbsBase+n-lag+k]
			xx += x * x
			yy += y * y
			xy += x * y
		}
		acorr[k+2] = xy / opusmath.SqrtF32(xx*yy+1e-9)
	}
}

// pitchPostprocessing mirrors `osce_features.c::pitch_postprocessing`. In
// libopus 1.6.1, OSCE_HANGOVER_BUGFIX is undefined so the hangover branches
// are dead code; we still mirror the exact branch structure for future
// constant flips. With the hangover gate compiled out, the behaviour
// collapses to:
//
//	type == TYPE_VOICED -> return lag (and update last_lag)
//	otherwise           -> return OSCE_NO_PITCH_VALUE
func pitchPostprocessing(s *FeatureState, lag, signalType int32) int {
	const testBit = 0 // OSCE_HANGOVER_BUGFIX is undefined in libopus 1.6.1
	modulus := int32(pitchHangover)
	if modulus == 0 {
		modulus++
	}
	var newLag int32
	switch {
	case signalType != typeVoiced && s.lastType == typeVoiced && testBit == 1:
		newLag = noPitchValue
		if s.pitchHangoverCount < pitchHangover {
			newLag = s.lastLag
			s.pitchHangoverCount = (s.pitchHangoverCount + 1) % modulus
		}
	case signalType != typeVoiced && s.pitchHangoverCount != 0 && testBit == 1:
		newLag = s.lastLag
		s.pitchHangoverCount = (s.pitchHangoverCount + 1) % modulus
	case signalType != typeVoiced:
		newLag = noPitchValue
		s.pitchHangoverCount = 0
	default:
		newLag = lag
		s.lastLag = lag
		s.pitchHangoverCount = 0
	}
	s.lastType = signalType
	return int(newLag)
}
