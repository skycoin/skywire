package encoder

import (
	"math"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/types"
)

// Tonality-analyzer dimensions mirroring the #defines in libopus src/analysis.h.
const (
	// NbFrames is the number of past short-time frames retained in the per-band
	// energy history rings (E/logE). Mirrors NB_FRAMES.
	NbFrames = 8
	// NbTBands is the number of tonality sub-bands analysed by the detector.
	// Mirrors NB_TBANDS.
	NbTBands = 18
	// NbTonalSkipBands is the number of low sub-bands skipped when summing
	// tonality (they are dominated by pitch). Mirrors NB_TONAL_SKIP_BANDS.
	NbTonalSkipBands = 9
	// AnalysisBufSize is the analyzer input buffer length, 30ms at 24kHz.
	// Mirrors ANALYSIS_BUF_SIZE.
	AnalysisBufSize = 720 // 30ms at 24kHz
	// DetectSize is the depth of the per-frame AnalysisInfo ring used to defer
	// the music/speech decision behind the analyzer lookahead. Mirrors
	// DETECT_SIZE.
	DetectSize        = 100
	transitionPenalty = float32(10.0)
	celtSigScale      = float32(32768.0)
	// analysisFFTEnergyScale folds the residual factor of libopus' SCALE_ENER
	// into the band-energy accumulation. The FFT output is normalised by 1/480
	// inside fft480 (matching libopus opus_fft), so SCALE_ENER reduces to
	// libopus' (1/32768/32768) and the 1/480^2 is not folded in here.
	analysisFFTEnergyScale = float32(1.0)
	analysisAtanScale      = float32(0.5 / math.Pi)
	analysisPi4            = float32(math.Pi * math.Pi * math.Pi * math.Pi)
	analysisAtanCA         = float32(0.43157974)
	analysisAtanCB         = float32(0.67848403)
	analysisAtanCC         = float32(0.08595542)
	analysisAtanCE         = float32(math.Pi / 2)
)

var stdFeatureBias = [9]float32{
	5.684947, 3.475288, 1.770634, 1.599784, 3.773215,
	2.163313, 1.260756, 1.116868, 1.918795,
}

var dctTable = [128]float32{
	0.250000, 0.250000, 0.250000, 0.250000, 0.250000, 0.250000, 0.250000, 0.250000,
	0.250000, 0.250000, 0.250000, 0.250000, 0.250000, 0.250000, 0.250000, 0.250000,
	0.351851, 0.338330, 0.311806, 0.273300, 0.224292, 0.166664, 0.102631, 0.034654,
	-0.034654, -0.102631, -0.166664, -0.224292, -0.273300, -0.311806, -0.338330, -0.351851,
	0.346760, 0.293969, 0.196424, 0.068975, -0.068975, -0.196424, -0.293969, -0.346760,
	-0.346760, -0.293969, -0.196424, -0.068975, 0.068975, 0.196424, 0.293969, 0.346760,
	0.338330, 0.224292, 0.034654, -0.166664, -0.311806, -0.351851, -0.273300, -0.102631,
	0.102631, 0.273300, 0.351851, 0.311806, 0.166664, -0.034654, -0.224292, -0.338330,
	0.326641, 0.135299, -0.135299, -0.326641, -0.326641, -0.135299, 0.135299, 0.326641,
	0.326641, 0.135299, -0.135299, -0.326641, -0.326641, -0.135299, 0.135299, 0.326641,
	0.311806, 0.034654, -0.273300, -0.338330, -0.102631, 0.224292, 0.351851, 0.166664,
	-0.166664, -0.351851, -0.224292, 0.102631, 0.338330, 0.273300, -0.034654, -0.311806,
	0.293969, -0.068975, -0.346760, -0.196424, 0.196424, 0.346760, 0.068975, -0.293969,
	-0.293969, 0.068975, 0.346760, 0.196424, -0.196424, -0.346760, -0.068975, 0.293969,
	0.273300, -0.166664, -0.338330, 0.034654, 0.351851, 0.102631, -0.311806, -0.224292,
	0.224292, 0.311806, -0.102631, -0.351851, -0.034654, 0.338330, 0.166664, -0.273300,
}

var analysisWindow = [240]float32{
	0.000043, 0.000171, 0.000385, 0.000685, 0.001071, 0.001541, 0.002098, 0.002739,
	0.003466, 0.004278, 0.005174, 0.006156, 0.007222, 0.008373, 0.009607, 0.010926,
	0.012329, 0.013815, 0.015385, 0.017037, 0.018772, 0.020590, 0.022490, 0.024472,
	0.026535, 0.028679, 0.030904, 0.033210, 0.035595, 0.038060, 0.040604, 0.043227,
	0.045928, 0.048707, 0.051564, 0.054497, 0.057506, 0.060591, 0.063752, 0.066987,
	0.070297, 0.073680, 0.077136, 0.080665, 0.084265, 0.087937, 0.091679, 0.095492,
	0.099373, 0.103323, 0.107342, 0.111427, 0.115579, 0.119797, 0.124080, 0.128428,
	0.132839, 0.137313, 0.141849, 0.146447, 0.151105, 0.155823, 0.160600, 0.165435,
	0.170327, 0.175276, 0.180280, 0.185340, 0.190453, 0.195619, 0.200838, 0.206107,
	0.211427, 0.216797, 0.222215, 0.227680, 0.233193, 0.238751, 0.244353, 0.250000,
	0.255689, 0.261421, 0.267193, 0.273005, 0.278856, 0.284744, 0.290670, 0.296632,
	0.302628, 0.308658, 0.314721, 0.320816, 0.326941, 0.333097, 0.339280, 0.345492,
	0.351729, 0.357992, 0.364280, 0.370590, 0.376923, 0.383277, 0.389651, 0.396044,
	0.402455, 0.408882, 0.415325, 0.421783, 0.428254, 0.434737, 0.441231, 0.447736,
	0.454249, 0.460770, 0.467298, 0.473832, 0.480370, 0.486912, 0.493455, 0.500000,
	0.506545, 0.513088, 0.519630, 0.526168, 0.532702, 0.539230, 0.545751, 0.552264,
	0.558769, 0.565263, 0.571746, 0.578217, 0.584675, 0.591118, 0.597545, 0.603956,
	0.610349, 0.616723, 0.623077, 0.629410, 0.635720, 0.642008, 0.648271, 0.654508,
	0.660720, 0.666903, 0.673059, 0.679184, 0.685279, 0.691342, 0.697372, 0.703368,
	0.709330, 0.715256, 0.721144, 0.726995, 0.732807, 0.738579, 0.744311, 0.750000,
	0.755647, 0.761249, 0.766807, 0.772320, 0.777785, 0.783203, 0.788573, 0.793893,
	0.799162, 0.804381, 0.809547, 0.814660, 0.819720, 0.824724, 0.829673, 0.834565,
	0.839400, 0.844177, 0.848895, 0.853553, 0.858151, 0.862687, 0.867161, 0.871572,
	0.875920, 0.880203, 0.884421, 0.888573, 0.892658, 0.896677, 0.900627, 0.904508,
	0.908321, 0.912063, 0.915735, 0.919335, 0.922864, 0.926320, 0.929703, 0.933013,
	0.936248, 0.939409, 0.942494, 0.945503, 0.948436, 0.951293, 0.954072, 0.956773,
	0.959396, 0.961940, 0.964405, 0.966790, 0.969096, 0.971321, 0.973465, 0.975528,
	0.977510, 0.979410, 0.981228, 0.982963, 0.984615, 0.986185, 0.987671, 0.989074,
	0.990393, 0.991627, 0.992778, 0.993844, 0.994826, 0.995722, 0.996534, 0.997261,
	0.997902, 0.998459, 0.998929, 0.999315, 0.999615, 0.999829, 0.999957, 1.000000,
}

var tbands = [NbTBands + 1]int{
	4, 8, 12, 16, 20, 24, 28, 32, 40, 48, 56, 64, 80, 96, 112, 136, 160, 192, 240,
}

func silkResamplerDown2HP(s []float32, out []float32, in []float32) float32 {
	len2 := min(len(out), len(in)/2)
	if len2 <= 0 {
		return 0
	}
	// BCE hints: ensure all accesses are in bounds
	_ = in[2*len2-1]
	_ = out[len2-1]
	_ = s[2]

	// Hoist filter state into locals to avoid repeated slice access
	s0, s1, s2 := s[0], s[1], s[2]

	// Keep this filter in the libopus float-build width.
	const (
		coef0 = float32(0.6074371)
		coef1 = float32(0.15063)
	)

	var hpEner float32
	k := 0
	for ; k+1 < len2; k += 2 {
		base := 2 * k

		in32 := in[base]
		y := in32 - s0
		xf := coef0 * y
		out32 := s0 + xf
		s0 = in32 + xf
		out32HP := out32

		in32 = in[base+1]
		y = in32 - s1
		xf = coef1 * y
		out32 = out32 + s1 + xf
		s1 = in32 + xf

		y = -in32 - s2
		xf = coef1 * y
		out32HP = out32HP + s2 + xf
		s2 = -in32 + xf

		hpEner += out32HP * out32HP
		out[k] = 0.5 * out32

		in32 = in[base+2]
		y = in32 - s0
		xf = coef0 * y
		out32 = s0 + xf
		s0 = in32 + xf
		out32HP = out32

		in32 = in[base+3]
		y = in32 - s1
		xf = coef1 * y
		out32 = out32 + s1 + xf
		s1 = in32 + xf

		y = -in32 - s2
		xf = coef1 * y
		out32HP = out32HP + s2 + xf
		s2 = -in32 + xf

		hpEner += out32HP * out32HP
		out[k+1] = 0.5 * out32
	}
	for ; k < len2; k++ {
		base := 2 * k

		in32 := in[base]
		y := in32 - s0
		xf := coef0 * y
		out32 := s0 + xf
		s0 = in32 + xf
		out32HP := out32

		in32 = in[base+1]
		y = in32 - s1
		xf = coef1 * y
		out32 = out32 + s1 + xf
		s1 = in32 + xf

		y = -in32 - s2
		xf = coef1 * y
		out32HP = out32HP + s2 + xf
		s2 = -in32 + xf

		hpEner += out32HP * out32HP
		out[k] = 0.5 * out32
	}

	// Write back filter state
	s[0], s[1], s[2] = s0, s1, s2

	// In libopus float builds, SHR64() is identity, so hp_ener accumulates the
	// raw squared high-pass output (no /256 shift). Keep that behavior here.
	return hpEner
}

func silkResamplerDown2HPScaled(s []float32, out []float32, in []float32, scale float32) float32 {
	len2 := min(len(out), len(in)/2)
	if len2 <= 0 {
		return 0
	}
	_ = in[2*len2-1]
	_ = out[len2-1]
	_ = s[2]

	s0, s1, s2 := s[0], s[1], s[2]

	const (
		coef0 = float32(0.6074371)
		coef1 = float32(0.15063)
	)

	var hpEner float32
	k := 0
	j := 0
	for ; k+1 < len2; k += 2 {
		in32 := in[j] * scale
		y := in32 - s0
		xf := coef0 * y
		out32 := s0 + xf
		s0 = in32 + xf
		out32HP := out32

		in32 = in[j+1] * scale
		y = in32 - s1
		xf = coef1 * y
		out32 = out32 + s1 + xf
		s1 = in32 + xf

		y = -in32 - s2
		xf = coef1 * y
		out32HP = out32HP + s2 + xf
		s2 = -in32 + xf

		hpEner += out32HP * out32HP
		out[k] = 0.5 * out32
		j += 2

		in32 = in[j] * scale
		y = in32 - s0
		xf = coef0 * y
		out32 = s0 + xf
		s0 = in32 + xf
		out32HP = out32

		in32 = in[j+1] * scale
		y = in32 - s1
		xf = coef1 * y
		out32 = out32 + s1 + xf
		s1 = in32 + xf

		y = -in32 - s2
		xf = coef1 * y
		out32HP = out32HP + s2 + xf
		s2 = -in32 + xf

		hpEner += out32HP * out32HP
		out[k+1] = 0.5 * out32
		j += 2
	}
	for ; k < len2; k++ {
		in32 := in[j] * scale
		y := in32 - s0
		xf := coef0 * y
		out32 := s0 + xf
		s0 = in32 + xf
		out32HP := out32

		in32 = in[j+1] * scale
		y = in32 - s1
		xf = coef1 * y
		out32 = out32 + s1 + xf
		s1 = in32 + xf

		y = -in32 - s2
		xf = coef1 * y
		out32HP = out32HP + s2 + xf
		s2 = -in32 + xf

		hpEner += out32HP * out32HP
		out[k] = 0.5 * out32
	}

	s[0], s[1], s[2] = s0, s1, s2
	return hpEner
}

func silkResamplerDown2HPStereo(s []float32, out []float32, in []float32, scale float32) float32 {
	len2 := min(len(out), len(in)/4)
	if len2 <= 0 {
		return 0
	}
	_ = in[4*len2-1]
	_ = out[len2-1]
	_ = s[2]

	s0, s1, s2 := s[0], s[1], s[2]

	const (
		coef0 = float32(0.6074371)
		coef1 = float32(0.15063)
	)

	var hpEner float32
	k := 0
	for ; k+1 < len2; k += 2 {
		base := 4 * k

		mixed0 := (in[base] + in[base+1]) * scale
		mixed1 := (in[base+2] + in[base+3]) * scale

		in32 := mixed0
		y := in32 - s0
		xf := coef0 * y
		out32 := s0 + xf
		s0 = in32 + xf
		out32HP := out32

		in32 = mixed1
		y = in32 - s1
		xf = coef1 * y
		out32 = out32 + s1 + xf
		s1 = in32 + xf

		y = -in32 - s2
		xf = coef1 * y
		out32HP = out32HP + s2 + xf
		s2 = -in32 + xf

		hpEner += out32HP * out32HP
		out[k] = 0.5 * out32

		base += 4

		mixed0 = (in[base] + in[base+1]) * scale
		mixed1 = (in[base+2] + in[base+3]) * scale

		in32 = mixed0
		y = in32 - s0
		xf = coef0 * y
		out32 = s0 + xf
		s0 = in32 + xf
		out32HP = out32

		in32 = mixed1
		y = in32 - s1
		xf = coef1 * y
		out32 = out32 + s1 + xf
		s1 = in32 + xf

		y = -in32 - s2
		xf = coef1 * y
		out32HP = out32HP + s2 + xf
		s2 = -in32 + xf

		hpEner += out32HP * out32HP
		out[k+1] = 0.5 * out32
	}
	for ; k < len2; k++ {
		base := 4 * k

		mixed0 := (in[base] + in[base+1]) * scale
		mixed1 := (in[base+2] + in[base+3]) * scale

		in32 := mixed0
		y := in32 - s0
		xf := coef0 * y
		out32 := s0 + xf
		s0 = in32 + xf
		out32HP := out32

		in32 = mixed1
		y = in32 - s1
		xf = coef1 * y
		out32 = out32 + s1 + xf
		s1 = in32 + xf

		y = -in32 - s2
		xf = coef1 * y
		out32HP = out32HP + s2 + xf
		s2 = -in32 + xf

		hpEner += out32HP * out32HP
		out[k] = 0.5 * out32
	}

	s[0], s[1], s[2] = s0, s1, s2
	return hpEner
}

func isDigitalSilence32(pcm []float32) bool {
	for i := range pcm {
		if pcm[i] != 0 {
			return false
		}
	}
	return true
}

func analysisFloat2Int(x float32) int32 {
	return opusmath.RoundToEvenF32ToInt32(x)
}

func analysisFastAtan2f(y, x float32) float32 {
	x2 := x * x
	y2 := y * y
	if x2+y2 < 1e-18 {
		return 0
	}
	xy := x * y
	if x2 < y2 {
		num := -xy * (y2 + analysisAtanCA*x2)
		den := (y2 + analysisAtanCB*x2) * (y2 + analysisAtanCC*x2)
		if y < 0 {
			return num/den - analysisAtanCE
		}
		return num/den + analysisAtanCE
	}
	num := xy * (x2 + analysisAtanCA*y2)
	den := (x2 + analysisAtanCB*y2) * (x2 + analysisAtanCC*y2)
	if y < 0 {
		if xy < 0 {
			return num / den
		}
		return num/den - analysisAtanCE - analysisAtanCE
	}
	if xy < 0 {
		return num/den + analysisAtanCE + analysisAtanCE
	}
	return num / den
}

// AnalysisInfo is the per-frame output of the tonality analyzer ("the brain").
// It mirrors the AnalysisInfo struct in libopus celt/celt.h and carries the
// music/speech probability, tonality and bandwidth estimates that opus_encoder.c
// consumes for mode, bandwidth and rate decisions. The encoder keeps the most
// recent value in Encoder.lastAnalysisInfo.
type AnalysisInfo struct {
	// Valid reports whether this entry holds a computed result (libopus "valid").
	// A zero AnalysisInfo (Valid==false) means "analysis not run / disabled".
	Valid bool
	// Tonality is the frame tonality estimate in [0,1] (libopus "tonality").
	Tonality float32
	// TonalitySlope is the inter-band tonality slope (libopus "tonality_slope").
	TonalitySlope float32
	// NoisySpeech is the noisiness estimate; high values bias toward SILK/VoIP.
	// Mirrors libopus "noisiness".
	NoisySpeech float32
	// StationarySpeech is the analyzer's stationary-speech estimate used by the
	// SILK/CELT bridge heuristics.
	StationarySpeech float32
	// MusicProb is the smoothed probability that the frame is music, in [0,1]
	// (libopus "music_prob"); the primary CELT-vs-SILK mode driver.
	MusicProb float32
	// MusicProbMin is the lower confidence bound of the music probability
	// (libopus "music_prob_min").
	MusicProbMin float32
	// MusicProbMax is the upper confidence bound of the music probability
	// (libopus "music_prob_max").
	MusicProbMax float32
	// VADProb is the analyzer voice-activity probability, libopus
	// "activity_probability"; also feeds Opus-level DTX.
	VADProb float32
	// Loudness is the frame loudness estimate used by surround/level logic.
	Loudness float32
	// BandwidthIndex is the detected signal bandwidth as a sub-band index
	// (libopus "bandwidth"); see Bandwidth for the decoded value.
	BandwidthIndex int32
	// Bandwidth is BandwidthIndex decoded to an Opus bandwidth used to clamp the
	// encoder's chosen bandwidth.
	Bandwidth types.Bandwidth
	// Activity is the raw frame activity estimate (libopus "activity").
	Activity float32
	// MaxPitchRatio is the maximum cross-frame pitch ratio (libopus
	// "max_pitch_ratio"), used by the CELT pitch/leak boost.
	MaxPitchRatio float32
	// LeakBoost holds the per-band Q6 leak-boost coefficients (libopus
	// "leak_boost[LEAK_BANDS]") forwarded to CELT dynalloc.
	LeakBoost [19]uint8
}

// TonalityAnalysisState is the persistent state of the Opus tonality analyzer,
// mirroring the TonalityAnalysisState struct in libopus src/analysis.h. It holds
// the FFT phase history, per-band energy rings and the MLP/GRU recurrent state
// that produce a music/speech AnalysisInfo for each frame. Exported fields keep
// the libopus member names (CamelCased) so the port stays auditable against
// src/analysis.c; unexported scratch fields are Go-only reuse buffers and carry
// no analyzer state across Reset.
type TonalityAnalysisState struct {
	// Fs is the analyzer input sample rate in Hz (libopus "Fs").
	Fs int32
	// LSBDepth is the input bit depth (8..24) used to set the noise floor
	// (libopus tracks this via the run_analysis lsb_depth argument).
	LSBDepth int32
	// Angle is the per-bin FFT phase from the previous analysis step (libopus
	// "angle"); it begins the TONALITY_ANALYSIS_RESET_START region.
	Angle [240]float32
	// DAngle is the first difference of Angle, the per-bin phase rate (libopus
	// "d_angle").
	DAngle [240]float32
	// D2Angle is the second difference of Angle, the per-bin phase acceleration
	// used for the tonality estimate (libopus "d2_angle").
	D2Angle [240]float32
	// InMem is the analyzer input ring buffer (libopus "inmem").
	InMem [AnalysisBufSize]float32
	// MemFill is the number of usable samples currently in InMem (libopus
	// "mem_fill").
	MemFill int32
	// PrevBandTonality carries the previous frame's per-band tonality for
	// temporal smoothing (libopus "prev_band_tonality").
	PrevBandTonality [NbTBands]float32
	// PrevTonality carries the previous frame's summed tonality (libopus
	// "prev_tonality").
	PrevTonality float32
	// PrevBandwidth is the previously detected bandwidth index (libopus
	// "prev_bandwidth"), used for bandwidth hysteresis.
	PrevBandwidth int32
	// E is the NbFrames-deep ring of per-band linear energies (libopus "E").
	E [NbFrames][NbTBands]float32
	// SqrtE is a Go-side cache of the per-band magnitudes (sqrt of E) for the
	// current frame, avoiding repeated square roots.
	SqrtE [NbFrames][NbTBands]float32
	// LogE is the NbFrames-deep ring of per-band log energies (libopus "logE").
	LogE [NbFrames][NbTBands]float32
	// LowE is the long-term per-band energy floor used by the noise/tone
	// classifier (libopus "lowE").
	LowE [NbTBands]float32
	// HighE is the long-term per-band energy ceiling used by the noise/tone
	// classifier (libopus "highE").
	HighE [NbTBands]float32
	// MeanE is the per-band running mean energy (libopus "meanE").
	MeanE [NbTBands + 1]float32
	// Mem is the feature smoothing memory feeding the MLP (libopus "mem").
	Mem [32]float32
	// CMean is the running mean of the MLP feature vector (libopus "cmean").
	CMean [8]float32
	// Std is the running standard deviation of the MLP features (libopus "std").
	Std [9]float32
	// ETracker is the slow energy follower used for loudness/activity (libopus
	// "Etracker").
	ETracker float32
	// LowECount is the fractional count of recent low-energy frames (libopus
	// "lowECount").
	LowECount float32
	// ECount is the per-band energy ring write index (libopus "E_count").
	ECount int32
	// Count is the number of frames analysed since reset, saturating at
	// ANALYSIS_COUNT_MAX (libopus "count").
	Count int32
	// AnalysisOffset is the sample offset between the analyzer position and the
	// current encode frame, carried across RunAnalysis calls.
	AnalysisOffset int32
	// WritePos is the write index into the Info ring (libopus "write_pos").
	WritePos int32
	// ReadPos is the read index into the Info ring (libopus "read_pos").
	ReadPos int32
	// ReadSubframe is the subframe within the Info entry being consumed (libopus
	// "read_subframe").
	ReadSubframe int32
	// HPEnerAccum accumulates high-pass energy for the loudness estimate
	// (libopus "hp_ener_accum").
	HPEnerAccum float32
	// Initialized reports whether the slow init has run (libopus "initialized").
	Initialized bool
	// RNNState is the GRU hidden state of the music/speech classifier (libopus
	// "rnn_state").
	RNNState [MaxNeurons]float32
	// DownmixState is the stereo->mono downmix filter memory (libopus
	// "downmix_state").
	DownmixState [3]float32
	// Info is the DetectSize-deep ring of per-frame results, read back behind the
	// analyzer lookahead (libopus "info").
	Info [DetectSize]AnalysisInfo

	// Scratch buffers for zero-allocation analysis
	scratchMono        []float32
	scratchDownsampled []float32
	scratchResample3x  []float32
	scratchFFTKiss     []celt.KissCpx
	scratchBinE        []float32 // precomputed bin energies for band loop
	scratchFFTIn       [480]complex64
	scratchFFTOut      [480]complex64
	scratchTonality    [240]float32
	scratchTonality2   [240]float32
	scratchNoisiness   [240]float32
}

// NewTonalityAnalysisState allocates and initializes a tonality analyzer for the
// given input sample rate (Hz). It corresponds to libopus tonality_analysis_init()
// followed by tonality_analysis_reset(): LSBDepth defaults to 24 and all
// reset-scoped state is cleared.
func NewTonalityAnalysisState(fs int) *TonalityAnalysisState {
	s := &TonalityAnalysisState{
		Fs:             int32(fs),
		LSBDepth:       24,
		scratchFFTKiss: make([]celt.KissCpx, 480),
	}
	s.Reset()
	return s
}

// Reset clears all reset-scoped analyzer state (the libopus
// TONALITY_ANALYSIS_RESET_START region onward) while preserving the configured
// sample rate, LSB depth and reusable scratch allocations. It mirrors libopus
// tonality_analysis_reset() and must be called on any input discontinuity.
func (s *TonalityAnalysisState) Reset() {
	// Match libopus tonality_analysis_reset(): clear all reset-scoped analysis
	// state while preserving reusable configuration/scratch allocations.
	fs := s.Fs
	lsbDepth := s.LSBDepth
	scratchMono := s.scratchMono[:0]
	scratchDownsampled := s.scratchDownsampled[:0]
	scratchResample3x := s.scratchResample3x[:0]
	scratchFFTKiss := s.scratchFFTKiss
	scratchBinE := s.scratchBinE[:0]

	*s = TonalityAnalysisState{
		Fs:                 fs,
		LSBDepth:           lsbDepth,
		scratchMono:        scratchMono,
		scratchDownsampled: scratchDownsampled,
		scratchResample3x:  scratchResample3x,
		scratchFFTKiss:     scratchFFTKiss,
		scratchBinE:        scratchBinE,
	}
}

// SetLSBDepth sets the analyzer's assumed input bit depth, clamped to the Opus
// range [8,24]. It controls the noise floor used by the tonality estimator and
// mirrors the lsb_depth value libopus passes into run_analysis.
func (s *TonalityAnalysisState) SetLSBDepth(depth int) {
	if depth < 8 {
		depth = 8
	}
	if depth > 24 {
		depth = 24
	}
	s.LSBDepth = int32(depth)
}

// analysisFFTScale is libopus opus_fft()'s float normalisation: st->scale =
// 1.f/nfft, applied per output element (S_MUL2(x, scale)) before the FFT
// recursion (celt/kiss_fft.c lines 478, 631-632). gopus' analysis must apply
// the same scale at the same point so the FFT outputs feeding fast_atan2f and
// the band-energy accumulators round identically to libopus; folding the
// 1/480^2 into the energy scale instead (the previous approach) squares the
// raw, unnormalised output and rounds at a different point, which the arm64
// fused-multiply-add path then amplifies through the scale-sensitive atan2.
const analysisFFTScale = float32(1.0 / 480.0)

// fft480 computes a 480-point complex forward FFT using the shared CELT KISS
// FFT, applying libopus' 1/nfft output normalisation exactly as opus_fft().
func fft480(out, in *[480]complex64, scratch []celt.KissCpx) {
	celt.KissFFT32ToScaledWithScratch(out[:], in[:], analysisFFTScale, scratch)
}

func analysisSpecVariability(logE *[NbFrames][NbTBands]float32) float32 {
	var mindist [NbFrames]float32
	for i := range NbFrames {
		mindist[i] = 1e15
	}
	for i := range NbFrames - 1 {
		rowI := &logE[i]
		for j := i + 1; j < NbFrames; j++ {
			rowJ := &logE[j]
			dist := float32(0)
			// NbTBands is fixed at 18; keep the accumulation order but
			// remove the fixed-trip loop overhead from this hot helper.
			d0 := rowI[0] - rowJ[0]
			dist += d0 * d0
			d1 := rowI[1] - rowJ[1]
			dist += d1 * d1
			d2 := rowI[2] - rowJ[2]
			dist += d2 * d2
			d3 := rowI[3] - rowJ[3]
			dist += d3 * d3
			d4 := rowI[4] - rowJ[4]
			dist += d4 * d4
			d5 := rowI[5] - rowJ[5]
			dist += d5 * d5
			d6 := rowI[6] - rowJ[6]
			dist += d6 * d6
			d7 := rowI[7] - rowJ[7]
			dist += d7 * d7
			d8 := rowI[8] - rowJ[8]
			dist += d8 * d8
			d9 := rowI[9] - rowJ[9]
			dist += d9 * d9
			d10 := rowI[10] - rowJ[10]
			dist += d10 * d10
			d11 := rowI[11] - rowJ[11]
			dist += d11 * d11
			d12 := rowI[12] - rowJ[12]
			dist += d12 * d12
			d13 := rowI[13] - rowJ[13]
			dist += d13 * d13
			d14 := rowI[14] - rowJ[14]
			dist += d14 * d14
			d15 := rowI[15] - rowJ[15]
			dist += d15 * d15
			d16 := rowI[16] - rowJ[16]
			dist += d16 * d16
			d17 := rowI[17] - rowJ[17]
			dist += d17 * d17
			if dist < mindist[i] {
				mindist[i] = dist
			}
			if dist < mindist[j] {
				mindist[j] = dist
			}
		}
	}
	specVariability := float32(0)
	for i := range NbFrames {
		specVariability += mindist[i]
	}
	return opusmath.SqrtF32(specVariability / float32(NbFrames*NbTBands))
}

func (s *TonalityAnalysisState) tonalityAnalysis(pcm []float32, channels int) {
	if !s.Initialized {
		s.MemFill = 240
		s.Initialized = true
	}
	count := int(s.Count)
	alpha := float32(1.0 / float32(min(10, 1+count)))
	alphaE := float32(1.0 / float32(min(25, 1+count)))
	alphaE2 := float32(1.0 / float32(min(100, 1+count)))
	if s.Count <= 1 {
		alphaE2 = 1.0
	}

	frameSize := len(pcm) / channels
	stereoScale := float32(0.5 * celtSigScale)
	var mono []float32
	if s.Fs != 48000 {
		if cap(s.scratchMono) < frameSize {
			s.scratchMono = make([]float32, frameSize)
		}
		mono = s.scratchMono[:frameSize]
		if channels == 2 {
			for i := range frameSize {
				mono[i] = (pcm[2*i] + pcm[2*i+1]) * stereoScale
			}
		} else {
			for i := range frameSize {
				mono[i] = pcm[i] * celtSigScale
			}
		}
	}

	// 2. Buffer mono samples into InMem
	// tonalityAnalysis is called with a new frame.
	// We need 480 samples at 24kHz for one analysis iteration.
	// But frames can be 2.5, 5, 10, 20ms.

	var (
		analysisLen int
		firstCopy   int
		hpEner      float32
	)
	oldMemFill := int(s.MemFill)
	space := max(AnalysisBufSize-oldMemFill, 0)

	// Match libopus downmix_and_resample split:
	// fill remaining analysis buffer first, and only then process residual.
	switch s.Fs {
	case 48000:
		analysisLen = frameSize / 2
		firstCopy = min(analysisLen, space)
		if firstCopy > 0 {
			first := s.InMem[oldMemFill : oldMemFill+firstCopy]
			var hp float32
			switch channels {
			case 2:
				hp = silkResamplerDown2HPStereo(s.DownmixState[:], first, pcm[:firstCopy*4], stereoScale)
			default:
				hp = silkResamplerDown2HPScaled(s.DownmixState[:], first, pcm[:firstCopy*2], celtSigScale)
			}
			hp *= 1.0 / (celtSigScale * celtSigScale)
			s.HPEnerAccum += hp
		}
	case 24000:
		analysisLen = frameSize
		firstCopy = min(analysisLen, space)
		if firstCopy > 0 {
			copy(s.InMem[oldMemFill:oldMemFill+firstCopy], mono[:firstCopy])
		}
	case 16000:
		analysisLen = (frameSize * 3) / 2
		firstCopy = min(analysisLen, space)
		if firstCopy > 0 {
			firstInput := (firstCopy * 2) / 3
			if firstInput > 0 {
				firstOutput := (firstInput * 3) / 2
				if cap(s.scratchResample3x) < firstInput*3 {
					s.scratchResample3x = make([]float32, firstInput*3)
				}
				first := s.InMem[oldMemFill : oldMemFill+firstOutput]
				tmp3x := s.scratchResample3x[:firstInput*3]
				for i := range firstInput {
					v := mono[i]
					j := 3 * i
					tmp3x[j] = v
					tmp3x[j+1] = v
					tmp3x[j+2] = v
				}
				hp := silkResamplerDown2HP(s.DownmixState[:], first, tmp3x)
				hp *= 1.0 / (celtSigScale * celtSigScale)
				s.HPEnerAccum += hp
				firstCopy = firstOutput
			} else {
				firstCopy = 0
			}
		}
	default:
		// Handle supported float-analysis rates only.
		return
	}

	if oldMemFill+analysisLen < AnalysisBufSize {
		s.MemFill = int32(oldMemFill + analysisLen)
		return
	}

	hpEner = s.HPEnerAccum
	infoPos := int(s.WritePos)
	nextWritePos := infoPos + 1
	if nextWritePos >= DetectSize {
		nextWritePos = 0
	}
	isSilence := isDigitalSilence32(s.InMem[:AnalysisBufSize])

	inBuf := s.scratchFFTIn[:]
	// Use 480 samples from InMem for FFT
	for i := range 240 {
		w := analysisWindow[i]
		inBuf[i] = complex(w*s.InMem[i], w*s.InMem[240+i])
		inBuf[480-i-1] = complex(w*s.InMem[480-i-1], w*s.InMem[480+240-i-1])
	}

	// Shift buffer and keep the residual input for the next analysis step.
	copy(s.InMem[:240], s.InMem[AnalysisBufSize-240:AnalysisBufSize])
	remaining := analysisLen - firstCopy
	switch s.Fs {
	case 48000:
		if remaining > 0 {
			rest := s.InMem[240 : 240+remaining]
			var hp float32
			switch channels {
			case 2:
				restSrcStart := firstCopy * 4
				restSrcEnd := restSrcStart + remaining*4
				hp = silkResamplerDown2HPStereo(s.DownmixState[:], rest, pcm[restSrcStart:restSrcEnd], stereoScale)
			default:
				restSrcStart := firstCopy * 2
				restSrcEnd := restSrcStart + remaining*2
				hp = silkResamplerDown2HPScaled(s.DownmixState[:], rest, pcm[restSrcStart:restSrcEnd], celtSigScale)
			}
			hp *= 1.0 / (celtSigScale * celtSigScale)
			s.HPEnerAccum = hp
		} else {
			s.HPEnerAccum = 0
		}
	case 24000:
		if remaining > 0 {
			copy(s.InMem[240:240+remaining], mono[firstCopy:firstCopy+remaining])
		}
		s.HPEnerAccum = 0
	case 16000:
		if remaining > 0 {
			restInput := (remaining * 2) / 3
			restSrcStart := (firstCopy * 2) / 3
			restSrcEnd := min(restSrcStart+restInput, frameSize)
			restInput = restSrcEnd - restSrcStart
			if restInput > 0 {
				restOutput := (restInput * 3) / 2
				if cap(s.scratchResample3x) < restInput*3 {
					s.scratchResample3x = make([]float32, restInput*3)
				}
				rest := s.InMem[240 : 240+restOutput]
				tmp3x := s.scratchResample3x[:restInput*3]
				for i := 0; i < restInput; i++ {
					v := mono[restSrcStart+i]
					j := 3 * i
					tmp3x[j] = v
					tmp3x[j+1] = v
					tmp3x[j+2] = v
				}
				hp := silkResamplerDown2HP(s.DownmixState[:], rest, tmp3x)
				hp *= 1.0 / (celtSigScale * celtSigScale)
				s.HPEnerAccum = hp
				remaining = restOutput
			} else {
				remaining = 0
				s.HPEnerAccum = 0
			}
		} else {
			s.HPEnerAccum = 0
		}
	}
	s.MemFill = int32(240 + remaining)
	if isSilence {
		prevPos := infoPos - 1
		if prevPos < 0 {
			prevPos = DetectSize - 1
		}
		s.Info[infoPos] = s.Info[prevPos]
		s.WritePos = int32(nextWritePos)
		return
	}

	if cap(s.scratchFFTKiss) < 480 {
		s.scratchFFTKiss = make([]celt.KissCpx, 480)
	}
	fft480(&s.scratchFFTOut, &s.scratchFFTIn, s.scratchFFTKiss[:480])
	outBuf := s.scratchFFTOut[:]
	if math.Float32bits(real(outBuf[0]))&0x7fffffff > 0x7f800000 {
		s.Info[infoPos].Valid = false
		s.WritePos = int32(nextWritePos)
		return
	}

	var logE [NbTBands]float32
	var bandLog2 [NbTBands + 1]float32
	var leakageFrom [NbTBands + 1]float32
	var leakageTo [NbTBands + 1]float32
	var BFCC [8]float32
	var midE [8]float32
	var features [25]float32
	var masked [NbTBands + 1]bool
	const (
		log2Scale      = float32(0.7213475) // 0.5*log2(e)
		leakageOffset  = float32(2.5)
		leakageSlope   = float32(2.0)
		leakageDivisor = float32(4.0)
	)
	specVariability := float32(0)

	tonality := s.scratchTonality[:]
	tonality2 := s.scratchTonality2[:]
	noisiness := s.scratchNoisiness[:]
	for i := 1; i < 240; i++ {
		x1r := real(outBuf[i]) + real(outBuf[480-i])
		x1i := imag(outBuf[i]) - imag(outBuf[480-i])
		x2r := imag(outBuf[i]) + imag(outBuf[480-i])
		x2i := real(outBuf[480-i]) - real(outBuf[i])

		xr2 := x1r * x1r
		xi2 := x1i * x1i
		atan := float32(0)
		if xr2+xi2 >= 1e-18 {
			xy := x1r * x1i
			if xr2 < xi2 {
				num := -xy * (xi2 + analysisAtanCA*xr2)
				den := (xi2 + analysisAtanCB*xr2) * (xi2 + analysisAtanCC*xr2)
				if x1i < 0 {
					atan = num/den - analysisAtanCE
				} else {
					atan = num/den + analysisAtanCE
				}
			} else {
				num := xy * (xr2 + analysisAtanCA*xi2)
				den := (xr2 + analysisAtanCB*xi2) * (xr2 + analysisAtanCC*xi2)
				if x1i < 0 {
					if xy < 0 {
						atan = num / den
					} else {
						atan = num/den - analysisAtanCE - analysisAtanCE
					}
				} else if xy < 0 {
					atan = num/den + analysisAtanCE + analysisAtanCE
				} else {
					atan = num / den
				}
			}
		}
		angle := analysisAtanScale * atan
		dAngle := angle - s.Angle[i]
		d2Angle := dAngle - s.DAngle[i]

		xr2 = x2r * x2r
		xi2 = x2i * x2i
		atan = 0
		if xr2+xi2 >= 1e-18 {
			xy := x2r * x2i
			if xr2 < xi2 {
				num := -xy * (xi2 + analysisAtanCA*xr2)
				den := (xi2 + analysisAtanCB*xr2) * (xi2 + analysisAtanCC*xr2)
				if x2i < 0 {
					atan = num/den - analysisAtanCE
				} else {
					atan = num/den + analysisAtanCE
				}
			} else {
				num := xy * (xr2 + analysisAtanCA*xi2)
				den := (xr2 + analysisAtanCB*xi2) * (xr2 + analysisAtanCC*xi2)
				if x2i < 0 {
					if xy < 0 {
						atan = num / den
					} else {
						atan = num/den - analysisAtanCE - analysisAtanCE
					}
				} else if xy < 0 {
					atan = num/den + analysisAtanCE + analysisAtanCE
				} else {
					atan = num / den
				}
			}
		}
		angle2 := analysisAtanScale * atan
		dAngle2 := angle2 - angle
		d2Angle2 := dAngle2 - dAngle

		mod1 := d2Angle - float32(analysisFloat2Int(d2Angle))
		if mod1 < 0 {
			noisiness[i] = -mod1
		} else {
			noisiness[i] = mod1
		}
		mod1 *= mod1
		mod1 *= mod1

		mod2 := d2Angle2 - float32(analysisFloat2Int(d2Angle2))
		if mod2 < 0 {
			noisiness[i] += -mod2
		} else {
			noisiness[i] += mod2
		}
		mod2 *= mod2
		mod2 *= mod2

		avgMod := 0.25 * (s.D2Angle[i] + mod1 + 2*mod2)
		tonality[i] = 1.0/(1.0+40.0*16.0*analysisPi4*avgMod) - 0.015
		tonality2[i] = 1.0/(1.0+40.0*16.0*analysisPi4*mod2) - 0.015

		s.Angle[i] = angle2
		s.DAngle[i] = dAngle2
		s.D2Angle[i] = mod2
	}
	for i := 2; i < 239; i++ {
		tt := minf(tonality2[i], maxf(tonality2[i-1], tonality2[i+1]))
		tonality[i] = 0.9 * maxf(tonality[i], tt-0.1)
	}

	frameNoisiness := float32(0)
	frameStationarity := float32(0)
	frameTonality := float32(0)
	maxFrameTonality := float32(0)
	relativeE := float32(0)
	frameLoudness := float32(0)
	slope := float32(0)
	bandwidthMask := float32(0)
	bandwidth := 0
	maxE := float32(0)
	lsbDepth := max(int(s.LSBDepth), 8)
	if lsbDepth > 24 {
		lsbDepth = 24
	}
	noiseFloor := float32(5.7e-4) / float32(uint(1)<<uint(max(0, lsbDepth-8)))
	belowMaxPitch := float32(0)
	aboveMaxPitch := float32(0)
	var bandTonality [NbTBands]float32
	var bandERaw [NbTBands]float32
	maxPitchRatio := float32(1.0)

	if s.Count == 0 {
		for b := range NbTBands {
			s.LowE[b] = 1e10
			s.HighE[b] = -1e10
		}
	}
	noiseFloor *= noiseFloor

	// Match libopus special handling for the first band (DC/Nyquist bins).
	{
		x1r := 2 * real(outBuf[0])
		x2r := 2 * imag(outBuf[0])
		E := x1r*x1r + x2r*x2r
		for i := 1; i < 4; i++ {
			binE := real(outBuf[i])*real(outBuf[i]) + real(outBuf[480-i])*real(outBuf[480-i]) +
				imag(outBuf[i])*imag(outBuf[i]) + imag(outBuf[480-i])*imag(outBuf[480-i])
			E += binE
		}
		E *= (1.0 / (celtSigScale * celtSigScale)) * analysisFFTEnergyScale
		bandLog2[0] = log2Scale * opusmath.LogF32(E+1e-10)
	}

	// Precompute bin energies for all analysis bins to improve memory access patterns.
	// Range: tbands[0]=4 to tbands[NbTBands]=240, so bins 4..239 = 236 entries.
	const binStart = 4 // tbands[0]
	const binEnd = 240 // tbands[NbTBands]
	const numBins = binEnd - binStart
	if cap(s.scratchBinE) < numBins {
		s.scratchBinE = make([]float32, numBins)
	}
	binEArr := s.scratchBinE[:numBins]
	{
		for i := binStart; i < binEnd; i++ {
			binE := real(outBuf[i])*real(outBuf[i]) + real(outBuf[480-i])*real(outBuf[480-i]) +
				imag(outBuf[i])*imag(outBuf[i]) + imag(outBuf[480-i])*imag(outBuf[480-i])
			binEArr[i-binStart] = binE
		}
	}
	const analysisBinScale = (1.0 / (celtSigScale * celtSigScale)) * analysisFFTEnergyScale

	// Band energies and tonal metrics using precomputed bin energies.
	for b := range NbTBands {
		var bandE, tE, nE, rawE float32
		for i := tbands[b]; i < tbands[b+1]; i++ {
			binERaw := binEArr[i-binStart]
			binE := binERaw * analysisBinScale
			rawE += binERaw
			bandE += binE
			tE += binE * maxf(0, tonality[i])
			nE += binE * 2.0 * (0.5 - noisiness[i])
		}
		bandERaw[b] = rawE

		eCount := int(s.ECount)
		s.E[eCount][b] = bandE
		logBandE := opusmath.LogF32(bandE + 1e-10)
		logE[b] = logBandE
		bandLog2[b+1] = log2Scale * logBandE
		s.LogE[eCount][b] = logE[b]
		s.SqrtE[eCount][b] = opusmath.SqrtF32(bandE)

		frameNoisiness += nE / (1e-15 + bandE)
		frameLoudness += opusmath.SqrtF32(bandE + 1e-10)

		if s.Count == 0 {
			s.HighE[b] = logE[b]
			s.LowE[b] = logE[b]
		}
		if s.HighE[b] > s.LowE[b]+7.5 {
			if s.HighE[b]-logE[b] > logE[b]-s.LowE[b] {
				s.HighE[b] -= 0.01
			} else {
				s.LowE[b] += 0.01
			}
		}
		if logE[b] > s.HighE[b] {
			s.HighE[b] = logE[b]
			s.LowE[b] = maxf(s.LowE[b], s.HighE[b]-15)
		} else if logE[b] < s.LowE[b] {
			s.LowE[b] = logE[b]
			s.HighE[b] = minf(s.HighE[b], s.LowE[b]+15)
		}
		relativeE += (logE[b] - s.LowE[b]) / (1e-5 + (s.HighE[b] - s.LowE[b]))

		var L1, L2 float32
		for i := range NbFrames {
			L1 += s.SqrtE[i][b]
			L2 += s.E[i][b]
		}
		stationarity := minf(0.99, L1/opusmath.SqrtF32(1e-15+float32(NbFrames)*L2))
		stationarity *= stationarity
		stationarity *= stationarity
		frameStationarity += stationarity

		bandTonality[b] = maxf(tE/(1e-15+bandE), stationarity*s.PrevBandTonality[b])
		frameTonality += bandTonality[b]
		if b >= NbTBands-NbTonalSkipBands {
			frameTonality -= bandTonality[b-NbTBands+NbTonalSkipBands]
		}
		maxFrameTonality = maxf(maxFrameTonality, (1.0+0.03*float32(b-NbTBands))*frameTonality)
		slope += bandTonality[b] * float32(b-8)
		s.PrevBandTonality[b] = bandTonality[b]
	}

	// Compute analysis leak_boost[] exactly as libopus analysis.c does.
	leakageFrom[0] = bandLog2[0]
	leakageTo[0] = bandLog2[0] - leakageOffset
	for b := 1; b < NbTBands+1; b++ {
		leakSlope := leakageSlope * float32(tbands[b]-tbands[b-1]) / leakageDivisor
		leakageFrom[b] = minf(leakageFrom[b-1]+leakSlope, bandLog2[b])
		leakageTo[b] = maxf(leakageTo[b-1]-leakSlope, bandLog2[b]-leakageOffset)
	}
	for b := NbTBands - 2; b >= 0; b-- {
		leakSlope := leakageSlope * float32(tbands[b+1]-tbands[b]) / leakageDivisor
		leakageFrom[b] = minf(leakageFrom[b], leakageFrom[b+1]+leakSlope)
		leakageTo[b] = maxf(leakageTo[b], leakageTo[b+1]-leakSlope)
	}
	specVariability = analysisSpecVariability(&s.LogE)

	for b := range NbTBands {
		bandStart := tbands[b]
		bandEnd := tbands[b+1]
		E := bandERaw[b] * analysisBinScale
		maxE = maxf(maxE, E)
		if bandStart < 64 {
			belowMaxPitch += E
		} else {
			aboveMaxPitch += E
		}
		s.MeanE[b] = maxf((1.0-alphaE2)*s.MeanE[b], E)
		Em := maxf(E, s.MeanE[b])
		width := float32(bandEnd - bandStart)
		if E*1e9 > maxE && (Em > 3*noiseFloor*width || E > noiseFloor*width) {
			bandwidth = b + 1
		}
		maskThresh := float32(0.05)
		if int(s.PrevBandwidth) >= b+1 {
			maskThresh = 0.01
		}
		masked[b] = E < maskThresh*bandwidthMask
		bandwidthMask = maxf(0.05*bandwidthMask, E)
	}
	if s.Fs == 48000 {
		E := hpEner * (1.0 / (60.0 * 60.0))
		noiseRatio := float32(30.0)
		if s.PrevBandwidth == 20 {
			noiseRatio = 10.0
		}
		aboveMaxPitch += E
		s.MeanE[NbTBands] = maxf((1.0-alphaE2)*s.MeanE[NbTBands], E)
		Em := maxf(E, s.MeanE[NbTBands])
		if Em > 3*noiseRatio*noiseFloor*160 || E > noiseRatio*noiseFloor*160 {
			bandwidth = 20
		}
		maskThresh := float32(0.05)
		if s.PrevBandwidth == 20 {
			maskThresh = 0.01
		}
		masked[NbTBands] = E < maskThresh*bandwidthMask
	}
	if aboveMaxPitch > belowMaxPitch {
		maxPitchRatio = belowMaxPitch / aboveMaxPitch
	}
	if bandwidth == 20 && masked[NbTBands] {
		bandwidth -= 2
	} else if bandwidth > 0 && bandwidth <= NbTBands && masked[bandwidth-1] {
		bandwidth--
	}
	if s.Count <= 2 {
		bandwidth = 20
	}

	frameLoudness = 20.0 * opusmath.Log10F32(frameLoudness)
	s.ETracker = maxf(s.ETracker-0.003, frameLoudness)
	s.LowECount *= 1.0 - alphaE
	if frameLoudness < s.ETracker-30.0 {
		s.LowECount += alphaE
	}

	// BFCC and mid-energy extraction
	for i := range 8 {
		row := dctTable[i*16 : i*16+16]
		var bfccSum, midESum float32
		for b := range 16 {
			coeff := row[b]
			bfccSum += coeff * logE[b]
			midESum += coeff * 0.5 * (s.HighE[b] + s.LowE[b])
		}
		BFCC[i] = bfccSum
		midE[i] = midESum
	}

	frameStationarity /= NbTBands
	relativeE /= NbTBands
	if s.Count < 10 {
		relativeE = 0.5
	}
	frameNoisiness /= NbTBands

	activity := frameNoisiness + (1.0-frameNoisiness)*relativeE
	frameTonality = maxFrameTonality / float32(NbTBands-NbTonalSkipBands)
	frameTonality = maxf(frameTonality, s.PrevTonality*0.8)
	s.PrevTonality = frameTonality
	slope /= 64.0

	s.ECount = (s.ECount + 1) % int32(NbFrames)
	s.Count = int32(min(int(s.Count+1), 10000))

	info := &s.Info[infoPos]
	info.Valid = true
	info.Tonality = frameTonality
	info.TonalitySlope = slope
	info.NoisySpeech = frameNoisiness
	info.StationarySpeech = frameStationarity
	info.Activity = activity
	info.MaxPitchRatio = maxPitchRatio
	info.BandwidthIndex = int32(bandwidth)
	info.Bandwidth = bandwidthTypeFromIndex(bandwidth)
	info.Loudness = frameLoudness

	for i := range 4 {
		features[i] = -0.12299*(BFCC[i]+s.Mem[i+24]) +
			0.49195*(s.Mem[i]+s.Mem[i+16]) +
			0.69693*s.Mem[i+8] -
			1.4349*s.CMean[i]
	}
	for i := range 4 {
		s.CMean[i] = (1.0-alpha)*s.CMean[i] + alpha*BFCC[i]
	}
	for i := range 4 {
		features[4+i] = 0.63246*(BFCC[i]-s.Mem[i+24]) + 0.31623*(s.Mem[i]-s.Mem[i+16])
	}
	for i := range 3 {
		features[8+i] = 0.53452*(BFCC[i]+s.Mem[i+24]) -
			0.26726*(s.Mem[i]+s.Mem[i+16]) -
			0.53452*s.Mem[i+8]
	}
	if s.Count > 5 {
		for i := range 9 {
			s.Std[i] = (1.0-alpha)*s.Std[i] + alpha*features[i]*features[i]
		}
	}
	for i := range 4 {
		features[i] = BFCC[i] - midE[i]
	}
	for i := range 8 {
		s.Mem[i+24] = s.Mem[i+16]
		s.Mem[i+16] = s.Mem[i+8]
		s.Mem[i+8] = s.Mem[i]
		s.Mem[i] = BFCC[i]
	}
	for i := range 9 {
		features[11+i] = opusmath.SqrtF32(s.Std[i]) - stdFeatureBias[i]
	}
	features[18] = specVariability - 0.78
	features[20] = info.Tonality - 0.154723
	features[21] = info.Activity - 0.724643
	features[22] = info.StationarySpeech - 0.743717
	features[23] = info.TonalitySlope + 0.069216
	features[24] = s.LowECount - 0.067930

	// Run MLP
	var layerOut [32]float32
	var frameProbs [2]float32
	layer0.ComputeDense(layerOut[:], features[:])
	layer1.ComputeGRU(s.RNNState[:], layerOut[:])
	layer2.ComputeDense(frameProbs[:], s.RNNState[:])
	info.MusicProb = frameProbs[0]
	info.VADProb = frameProbs[1]
	for b := range NbTBands + 1 {
		boost := maxf(0, leakageTo[b]-bandLog2[b]) + maxf(0, bandLog2[b]-(leakageFrom[b]+leakageOffset))
		q6 := min(int(opusmath.FloorHalfPlusF32ToInt32(float32(64)*boost)), 255)
		if q6 < 0 {
			q6 = 0
		}
		info.LeakBoost[b] = uint8(q6)
	}
	s.PrevBandwidth = int32(bandwidth)
	s.WritePos = int32(nextWritePos)
}

func bandwidthTypeFromIndex(bandwidth int) types.Bandwidth {
	switch {
	case bandwidth <= 12:
		return types.BandwidthNarrowband
	case bandwidth <= 14:
		return types.BandwidthMediumband
	case bandwidth <= 16:
		return types.BandwidthWideband
	case bandwidth <= 18:
		return types.BandwidthSuperwideband
	default:
		return types.BandwidthFullband
	}
}

func maxf(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

func minf(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

// tonalityGetInfo mirrors libopus tonality_get_info() and derives the
// smoothed music-probability thresholds used for mode switching.
func (s *TonalityAnalysisState) tonalityGetInfo(frameSize int) AnalysisInfo {
	out := AnalysisInfo{}

	pos := int(s.ReadPos)
	writePos := int(s.WritePos)
	currLookahead := writePos - pos
	if currLookahead < 0 {
		currLookahead += DetectSize
	}

	subframe := int(s.Fs) / 400
	if subframe <= 0 {
		subframe = 1
	}
	s.ReadSubframe += int32(frameSize / subframe)
	for s.ReadSubframe >= 8 {
		s.ReadSubframe -= 8
		s.ReadPos++
	}
	if s.ReadPos >= DetectSize {
		s.ReadPos -= DetectSize
	}

	// On long frames, inspect the second analysis window.
	writePos = int(s.WritePos)
	if frameSize > int(s.Fs)/50 && pos != writePos {
		pos++
		if pos == DetectSize {
			pos = 0
		}
	}
	if pos == writePos {
		pos--
	}
	if pos < 0 {
		pos = DetectSize - 1
	}
	pos0 := pos

	out = s.Info[pos]
	if !out.Valid {
		return out
	}

	tonalityMax := out.Tonality
	tonalityAvg := out.Tonality
	tonalityCount := 1
	bandwidthSpan := 6

	// Look ahead for tonality and safe bandwidth.
	for range 3 {
		pos++
		if pos == DetectSize {
			pos = 0
		}
		if pos == int(s.WritePos) {
			break
		}
		if s.Info[pos].Tonality > tonalityMax {
			tonalityMax = s.Info[pos].Tonality
		}
		tonalityAvg += s.Info[pos].Tonality
		tonalityCount++
		if s.Info[pos].BandwidthIndex > out.BandwidthIndex {
			out.BandwidthIndex = s.Info[pos].BandwidthIndex
		}
		bandwidthSpan--
	}

	// Look back for wider bandwidth evidence.
	pos = pos0
	for i := 0; i < bandwidthSpan; i++ {
		pos--
		if pos < 0 {
			pos = DetectSize - 1
		}
		if pos == int(s.WritePos) {
			break
		}
		if s.Info[pos].BandwidthIndex > out.BandwidthIndex {
			out.BandwidthIndex = s.Info[pos].BandwidthIndex
		}
	}
	out.Bandwidth = bandwidthTypeFromIndex(int(out.BandwidthIndex))

	tonalityMean := tonalityAvg / float32(tonalityCount)
	out.Tonality = maxf(tonalityMean, tonalityMax-0.2)

	mpos := pos0
	vpos := pos0
	// Compensate music-prob (~5 frames) and VAD (~1 frame) delay when lookahead exists.
	if currLookahead > 15 {
		mpos += 5
		if mpos >= DetectSize {
			mpos -= DetectSize
		}
		vpos++
		if vpos >= DetectSize {
			vpos -= DetectSize
		}
	}

	probMin := float32(1.0)
	probMax := float32(0.0)
	vadProb := s.Info[vpos].VADProb
	activityWeight := maxf(0.1, vadProb)
	probCount := activityWeight
	probAvg := activityWeight * s.Info[mpos].MusicProb

	for {
		mpos++
		if mpos == DetectSize {
			mpos = 0
		}
		if mpos == int(s.WritePos) {
			break
		}
		vpos++
		if vpos == DetectSize {
			vpos = 0
		}
		if vpos == int(s.WritePos) {
			break
		}

		posVAD := s.Info[vpos].VADProb
		posWeight := maxf(0.1, posVAD)
		denom := probCount
		if denom < 1e-9 {
			denom = 1e-9
		}
		probMin = minf((probAvg-transitionPenalty*(vadProb-posVAD))/denom, probMin)
		probMax = maxf((probAvg+transitionPenalty*(vadProb-posVAD))/denom, probMax)

		probCount += posWeight
		probAvg += posWeight * s.Info[mpos].MusicProb
	}

	if probCount < 1e-9 {
		probCount = 1e-9
	}
	out.MusicProb = probAvg / probCount
	probMin = minf(out.MusicProb, probMin)
	probMax = maxf(out.MusicProb, probMax)
	probMin = maxf(probMin, 0.0)
	probMax = minf(probMax, 1.0)

	// With little/no lookahead, use recent history as fallback.
	if currLookahead < 10 {
		pmin := probMin
		pmax := probMax
		pos = pos0
		history := min(int(s.Count-1), 15)
		if history < 0 {
			history = 0
		}
		for i := 0; i < history; i++ {
			pos--
			if pos < 0 {
				pos = DetectSize - 1
			}
			pmin = minf(pmin, s.Info[pos].MusicProb)
			pmax = maxf(pmax, s.Info[pos].MusicProb)
		}

		pmin = maxf(0.0, pmin-0.1*vadProb)
		pmax = minf(1.0, pmax+0.1*vadProb)
		blend := float32(1.0) - 0.1*float32(currLookahead)
		probMin += blend * (pmin - probMin)
		probMax += blend * (pmax - probMax)
	}

	out.MusicProbMin = probMin
	out.MusicProbMax = probMax

	return out
}

// RunAnalysis feeds one frame of interleaved float PCM through the tonality
// analyzer and returns the AnalysisInfo to use for the current frame. It mirrors
// libopus run_analysis() (src/analysis.c): the input is split into 20ms
// (Fs/50-sample) chunks fed to tonalityAnalysis, AnalysisOffset is advanced so
// the analyzer stays frameSize samples behind the encode position, and the
// matured result is read back via tonalityGetInfo. frameSize is the encode frame
// length in samples at Fs; channels is 1 or 2. Passing an empty pcm slice only
// reads back the buffered result without advancing the analyzer.
func (s *TonalityAnalysisState) RunAnalysis(pcm []float32, frameSize int, channels int) AnalysisInfo {
	if channels <= 0 {
		channels = 1
	}

	analysisFrameSize := 0
	if len(pcm) > 0 {
		analysisFrameSize = len(pcm) / channels
		analysisFrameSize -= analysisFrameSize & 1
		maxAnalysisFrameSize := (DetectSize - 5) * int(s.Fs) / 50
		if maxAnalysisFrameSize > 0 && analysisFrameSize > maxAnalysisFrameSize {
			analysisFrameSize = maxAnalysisFrameSize
		}
	}

	if analysisFrameSize > 0 {
		pcmLen := analysisFrameSize - int(s.AnalysisOffset)
		offset := int(s.AnalysisOffset)
		chunkSize := int(s.Fs) / 50
		if chunkSize <= 0 {
			chunkSize = analysisFrameSize
		}

		for pcmLen > 0 {
			// libopus can pass negative offsets when analysis uses external
			// lookahead. Skip unavailable prefix when only current PCM is present.
			if offset < 0 {
				advance := min(-offset, pcmLen)
				offset += advance
				pcmLen -= advance
				continue
			}

			chunk := min(chunkSize, pcmLen)
			if chunk <= 0 {
				break
			}

			start := offset * channels
			if start >= len(pcm) {
				break
			}
			end := min(start+chunk*channels, len(pcm))
			if end > start {
				s.tonalityAnalysis(pcm[start:end], channels)
			}

			offset += chunkSize
			pcmLen -= chunkSize
		}

		s.AnalysisOffset = int32(analysisFrameSize - frameSize)
	}

	return s.tonalityGetInfo(frameSize)
}

// GetInfo returns the most recently written per-frame AnalysisInfo, i.e. the
// entry just behind WritePos in the Info ring. Unlike tonality_get_info() in
// libopus it does not advance or average over subframes; it is a read-only peek
// at the latest result for callers that drive RunAnalysis directly.
func (s *TonalityAnalysisState) GetInfo() AnalysisInfo {
	readPos := int((s.WritePos + int32(DetectSize) - 1) % int32(DetectSize))
	return s.Info[readPos]
}
