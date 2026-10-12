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
	// analysisFFTEnergyScale is unity because fft480 normalizes each output by
	// analysisFFTScale; analysisEnergyScale applies the codec amplitude scale to
	// the resulting bin energies.
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

// silkResamplerDown2HP is silk_resampler_down2_hp (src/analysis.c) in the float
// build: it halves the rate of in into out and returns the high-pass energy.
func silkResamplerDown2HP(s []float32, out []float32, in []float32) float32 {
	len2 := min(len(out), len(in)/2)
	if len2 <= 0 {
		return 0
	}
	_ = in[2*len2-1]
	_ = out[len2-1]
	s0, s1, s2 := s[0], s[1], s[2]
	var hpEner, hp, sample float32
	for k := range len2 {
		s0, s1, s2, sample, hp = down2HPStep(s0, s1, s2, in[2*k], in[2*k+1])
		out[k] = 0.5 * sample
		hpEner += hp * hp
	}
	s[0], s[1], s[2] = s0, s1, s2
	return hpEner
}

// silkResamplerDown2HPMono is silkResamplerDown2HP over downmix_float of the
// mono input pcm, which it reads directly.
func silkResamplerDown2HPMono(s []float32, out []float32, pcm []float32) float32 {
	len2 := min(len(out), len(pcm)/2)
	if len2 <= 0 {
		return 0
	}
	_ = pcm[2*len2-1]
	_ = out[len2-1]
	s0, s1, s2 := s[0], s[1], s[2]
	var hpEner, hp, sample float32
	for k := range len2 {
		in0 := downmixCap(pcm[2*k] * celtSigScale)
		in1 := downmixCap(pcm[2*k+1] * celtSigScale)
		s0, s1, s2, sample, hp = down2HPStep(s0, s1, s2, in0, in1)
		out[k] = 0.5 * sample
		hpEner += hp * hp
	}
	s[0], s[1], s[2] = s0, s1, s2
	return hpEner
}

// silkResamplerDown2HPStereo is silkResamplerDown2HP over downmix_float of the
// interleaved stereo input pcm, halved after the cap as for two summed
// channels, which it reads directly.
func silkResamplerDown2HPStereo(s []float32, out []float32, pcm []float32) float32 {
	len2 := min(len(out), len(pcm)/4)
	if len2 <= 0 {
		return 0
	}
	_ = pcm[4*len2-1]
	_ = out[len2-1]
	s0, s1, s2 := s[0], s[1], s[2]
	var hpEner, hp, sample float32
	for k := range len2 {
		p := pcm[4*k : 4*k+4 : 4*k+4]
		in0 := analysisDown2HalfInput(downmixCap(p[0]*celtSigScale + p[1]*celtSigScale))
		in1 := analysisDown2HalfInput(downmixCap(p[2]*celtSigScale + p[3]*celtSigScale))
		s0, s1, s2, sample, hp = down2HPStep(s0, s1, s2, in0, in1)
		out[k] = 0.5 * sample
		hpEner += hp * hp
	}
	s[0], s[1], s[2] = s0, s1, s2
	return hpEner
}

// analysisBinEnergy is the energy of the bin pair i and 480-i of the analysis
// FFT, out[i].r² + out[N-i].r² + out[i].i² + out[N-i].i² in analysis.c's order.
func analysisBinEnergy(out []complex64, i int) float32 {
	a, b := real(out[i]), real(out[480-i])
	c, d := imag(out[i]), imag(out[480-i])
	return fma32(a, a, round32(b*b)) + c*c + d*d
}

// analysisIsDigitalSilence32 is is_digital_silence32 (src/analysis.c) in the
// float build: the buffer is silent when its peak magnitude,
// celt_maxabs32(pcm), is at most 1/2^lsbDepth. The analysis buffer never holds
// NaN (downmix_float zeroes it), so the first sample outside the threshold
// decides.
func analysisIsDigitalSilence32(pcm []float32, lsbDepth int) bool {
	threshold := float32(1) / float32(int32(1)<<lsbDepth)
	for _, v := range pcm {
		if v > threshold || v < -threshold {
			return false
		}
	}
	return true
}

// downmixCap is downmix_float's +6 dBFS cap (src/opus_encoder.c): it clamps the
// SIG-scale sample to ±65536 and zeroes NaN.
func downmixCap(v float32) float32 {
	if v < -65536 {
		v = -65536
	}
	if v > 65536 {
		v = 65536
	}
	if v != v {
		v = 0
	}
	return v
}

// downmixAndResample is downmix_and_resample (src/analysis.c) driven by
// downmix_float (src/opus_encoder.c) for interleaved float input and the
// all-channel c2 == -2 layout. subframe and offset are 24 kHz lengths. It writes
// subframe analysis samples to y and returns the high-band energy, which only
// 48 kHz input produces.
func (s *TonalityAnalysisState) downmixAndResample(pcm []float32, channels int, y []float32, subframe, offset int) float32 {
	if subframe <= 0 {
		return 0
	}
	switch s.Fs {
	case 48000:
		subframe *= 2
		offset *= 2
	case 16000:
		subframe = subframe * 2 / 3
		offset = offset * 2 / 3
	}
	if s.Fs == 48000 && channels <= 2 {
		var ret float32
		if channels == 1 {
			ret = silkResamplerDown2HPMono(s.DownmixState[:], y[:subframe/2], pcm[offset:offset+subframe])
		} else {
			ret = silkResamplerDown2HPStereo(s.DownmixState[:], y[:subframe/2], pcm[2*offset:2*(offset+subframe)])
		}
		return ret * (1.0 / 32768 / 32768)
	}
	if cap(s.scratchMono) < subframe {
		s.scratchMono = make([]float32, subframe)
	}
	tmp := s.scratchMono[:subframe]
	switch channels {
	case 1:
		for j := range tmp {
			tmp[j] = downmixCap(pcm[j+offset] * celtSigScale)
		}
	case 2:
		// Two summed channels are halved after the cap.
		for j := range tmp {
			k := 2 * (j + offset)
			tmp[j] = 0.5 * downmixCap(pcm[k]*celtSigScale+pcm[k+1]*celtSigScale)
		}
	default:
		for j := range tmp {
			tmp[j] = pcm[(j+offset)*channels] * celtSigScale
		}
		for c := 1; c < channels; c++ {
			for j := range tmp {
				tmp[j] += pcm[(j+offset)*channels+c] * celtSigScale
			}
		}
		for j, v := range tmp {
			tmp[j] = downmixCap(v)
		}
	}
	var ret float32
	switch s.Fs {
	case 48000:
		ret = silkResamplerDown2HP(s.DownmixState[:], y, tmp)
	case 24000:
		copy(y[:subframe], tmp)
	case 16000:
		// The 3x repeat then 2x decimation is libopus's rough 16->24 kHz
		// resampler; its high-band energy is discarded.
		if cap(s.scratchResample3x) < 3*subframe {
			s.scratchResample3x = make([]float32, 3*subframe)
		}
		tmp3x := s.scratchResample3x[:3*subframe]
		for j, v := range tmp {
			tmp3x[3*j] = v
			tmp3x[3*j+1] = v
			tmp3x[3*j+2] = v
		}
		silkResamplerDown2HP(s.DownmixState[:], y, tmp3x)
	}
	return ret * (1.0 / 32768 / 32768)
}

func analysisFloat2Int(x float32) int32 {
	return opusmath.RoundToEvenF32ToInt32(x)
}

// analysisBins runs tonality_analysis's per-bin loop over bins 1..239 of the
// FFT output: the two phase-acceleration estimates, the noisiness and the
// per-bin tonality, updating the phase history.
func (s *TonalityAnalysisState) analysisBins(out *[480]complex64, tonality, tonality2, noisiness []float32) {
	s.analysisBinsScalar(out, s.analysisBinsSIMD(out, tonality, tonality2, noisiness), tonality, tonality2, noisiness)
}

// analysisBinsScalar is analysisBins for bins from..239.
func (s *TonalityAnalysisState) analysisBinsScalar(out *[480]complex64, from int, tonality, tonality2, noisiness []float32) {
	for i := from; i < 240; i++ {
		x1r := real(out[i]) + real(out[480-i])
		x1i := imag(out[i]) - imag(out[480-i])
		x2r := imag(out[i]) + imag(out[480-i])
		x2i := real(out[480-i]) - real(out[i])

		angle := round32(analysisAtanScale * analysisAtan2(x1i, x1r))
		dAngle := angle - s.Angle[i]
		d2Angle := dAngle - s.DAngle[i]

		angle2 := round32(analysisAtanScale * analysisAtan2(x2i, x2r))
		dAngle2 := angle2 - angle
		d2Angle2 := dAngle2 - dAngle

		mod1 := d2Angle - float32(analysisFloat2Int(d2Angle))
		noisiness[i] = opusmath.AbsF32(mod1)
		mod1 = round32(mod1 * mod1)

		mod2 := d2Angle2 - float32(analysisFloat2Int(d2Angle2))
		noisiness[i] += opusmath.AbsF32(mod2)
		mod2 *= mod2
		mod2 = round32(mod2 * mod2)

		avgMod := analysisAvgMod32(mod1, s.D2Angle[i], mod2)
		tonality[i] = 1.0/(1.0+40.0*16.0*analysisPi4*avgMod) - 0.015
		tonality2[i] = 1.0/(1.0+40.0*16.0*analysisPi4*mod2) - 0.015

		s.Angle[i] = angle2
		s.DAngle[i] = dAngle2
		s.D2Angle[i] = mod2
		if analysisPerBinTraceEnabled && analysisPerBinTraceHook != nil {
			analysisPerBinTraceHook(analysisPerBinTraceSnapshot{
				Bin:       int32(i),
				AvgMod:    avgMod,
				Tonality:  tonality[i],
				Tonality2: tonality2[i],
				Noisiness: noisiness[i],
			})
		}
	}
}

// analysisAtan2 is analysis.c fast_atan2f(y, x). The two rational forms and
// the quadrant terms are chosen by conditional moves rather than branches on
// the spectrum. When x2 < y2 the second quadrant term is zero, and q + s1 - 0
// is q + s1 exactly.
func analysisAtan2(y, x float32) float32 {
	x2 := round32(x * x)
	y2 := round32(y * y)
	xy := x * y
	swap := x2 < y2
	p := opusmath.SelectF32(swap, y2, x2)
	q := opusmath.SelectF32(swap, x2, y2)
	num := opusmath.SelectF32(swap, -xy, xy) * (p + analysisAtanCA*q)
	den := (p + analysisAtanCB*q) * (p + analysisAtanCC*q)
	s1 := opusmath.SelectF32(y < 0, -analysisAtanCE, analysisAtanCE)
	s2 := opusmath.SelectF32(swap, 0, opusmath.SelectF32(xy < 0, -analysisAtanCE, analysisAtanCE))
	return opusmath.SelectF32(x2+y2 < 1e-18, 0, num/den+s1-s2)
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
	// CMean is the running mean of the first four BFCC coefficients (libopus "cmean").
	CMean [8]float32
	// Std stores running second moments of the temporal BFCC features (libopus "std").
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
	// fixed holds the integer input/resampler/FFT state when the fixed-point
	// encoder build is selected. The analyzer's feature and classifier state
	// remains float32 in both libopus builds.
	fixed fixedAnalysisState
	// Info is the DetectSize-deep ring of per-frame results, read back behind the
	// analyzer lookahead (libopus "info").
	Info [DetectSize]AnalysisInfo

	// silentWindow reports whether the latest completed analysis window was
	// digital silence, for which the Info ring repeats the previous entry.
	silentWindow bool

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
		Fs:       int32(fs),
		LSBDepth: 24,
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

// analysisFFTScale matches libopus opus_fft() normalization: st->scale =
// 1.f/nfft, applied per output element (S_MUL2(x, scale)) before the FFT
// recursion (celt/kiss_fft.c lines 478, 631-632). Applying the scale at this
// point keeps the FFT outputs used by fast_atan2f and band-energy accumulation
// in the same normalized range as libopus.
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
			dist = analysisSpecVariabilityAddSquare(dist, d0)
			d1 := rowI[1] - rowJ[1]
			dist = analysisSpecVariabilityAddSquare(dist, d1)
			d2 := rowI[2] - rowJ[2]
			dist = analysisSpecVariabilityAddSquare(dist, d2)
			d3 := rowI[3] - rowJ[3]
			dist = analysisSpecVariabilityAddSquare(dist, d3)
			d4 := rowI[4] - rowJ[4]
			dist = analysisSpecVariabilityAddSquare(dist, d4)
			d5 := rowI[5] - rowJ[5]
			dist = analysisSpecVariabilityAddSquare(dist, d5)
			d6 := rowI[6] - rowJ[6]
			dist = analysisSpecVariabilityAddSquare(dist, d6)
			d7 := rowI[7] - rowJ[7]
			dist = analysisSpecVariabilityAddSquare(dist, d7)
			d8 := rowI[8] - rowJ[8]
			dist = analysisSpecVariabilityAddSquare(dist, d8)
			d9 := rowI[9] - rowJ[9]
			dist = analysisSpecVariabilityAddSquare(dist, d9)
			d10 := rowI[10] - rowJ[10]
			dist = analysisSpecVariabilityAddSquare(dist, d10)
			d11 := rowI[11] - rowJ[11]
			dist = analysisSpecVariabilityAddSquare(dist, d11)
			d12 := rowI[12] - rowJ[12]
			dist = analysisSpecVariabilityAddSquare(dist, d12)
			d13 := rowI[13] - rowJ[13]
			dist = analysisSpecVariabilityAddSquare(dist, d13)
			d14 := rowI[14] - rowJ[14]
			dist = analysisSpecVariabilityAddSquare(dist, d14)
			d15 := rowI[15] - rowJ[15]
			dist = analysisSpecVariabilityAddSquare(dist, d15)
			d16 := rowI[16] - rowJ[16]
			dist = analysisSpecVariabilityAddFinalSquare(dist, d16)
			d17 := rowI[17] - rowJ[17]
			dist = analysisSpecVariabilityAddFinalSquare(dist, d17)
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
	normalized := specVariability / float32(NbFrames*NbTBands)
	result := opusmath.SqrtF32(normalized)
	if analysisSpecVariabilityTraceEnabled && analysisSpecVariabilityTraceHook != nil {
		analysisSpecVariabilityTraceHook(analysisSpecVariabilityTraceSnapshot{
			LogE:       *logE,
			MinDist:    mindist,
			Sum:        specVariability,
			Normalized: normalized,
			Result:     result,
		})
	}
	return result
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

	// tonality_analysis works on 24 kHz lengths: len and offset are scaled here
	// and scaled back by downmixAndResample.
	frameSize := len(pcm) / channels
	var len24 int
	switch s.Fs {
	case 48000:
		len24 = frameSize / 2
	case 24000:
		len24 = frameSize
	case 16000:
		len24 = 3 * frameSize / 2
	default:
		return
	}
	memFill := int(s.MemFill)
	s.HPEnerAccum += s.analysisDownmixAndResample(pcm, channels, memFill, min(len24, AnalysisBufSize-memFill), 0)
	if memFill+len24 < AnalysisBufSize {
		s.MemFill = int32(memFill + len24)
		return
	}
	hpEner := s.HPEnerAccum
	infoPos := int(s.WritePos)
	nextWritePos := infoPos + 1
	if nextWritePos >= DetectSize {
		nextWritePos = 0
	}
	isSilence := s.analysisIsDigitalSilence()
	s.silentWindow = isSilence
	s.analysisPrepareFFTInput()
	s.analysisShiftInput()
	remaining := len24 - (AnalysisBufSize - memFill)
	s.HPEnerAccum = s.analysisDownmixAndResample(pcm, channels, 240, remaining, AnalysisBufSize-memFill)
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

	s.analysisRunFFT()
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
	s.analysisBins(&s.scratchFFTOut, tonality, tonality2, noisiness)
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
	lsbDepth := min(max(int(s.LSBDepth), 8), 24)
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
		E := fma32(x1r, x1r, round32(x2r*x2r))
		for i := 1; i < 4; i++ {
			E += analysisBinEnergy(outBuf, i)
		}
		E *= s.analysisEnergyScale()
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
			binEArr[i-binStart] = analysisBinEnergy(outBuf, i)
		}
	}
	analysisBinScale := s.analysisEnergyScale()

	// Band energies and tonal metrics using precomputed bin energies.
	for b := range NbTBands {
		var bandE, tE, nE, rawE float32
		// The selected ARM C loop rounds products in four-bin groups. Its
		// remaining one to three bins use the scalar fused accumulation.
		vecEnd := tbands[b] + (tbands[b+1]-tbands[b])&^3
		for i := tbands[b]; i < tbands[b+1]; i++ {
			binERaw := binEArr[i-binStart]
			binE := round32(binERaw * analysisBinScale)
			rawE += binERaw
			bandE += binE
			if analysisNEONReductions && i < vecEnd {
				tE += round32(binE * maxf(0, tonality[i]))
				nE += round32(binE * 2.0 * (0.5 - noisiness[i]))
			} else {
				tE += binE * maxf(0, tonality[i])
				nE += binE * 2.0 * (0.5 - noisiness[i])
			}
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
		// analysis.c compares against lowE[b] + 7.5 in C double.
		if opusmath.CReal(s.HighE[b]) > opusmath.CReal(s.LowE[b])+7.5 {
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
		// analysis.c: L1/(float)sqrt(1e-15+NB_FRAMES*L2), with the sum and the
		// square root in C double.
		stationarity := minf(0.99, L1/float32(opusmath.SqrtCReal(1e-15+opusmath.CReal(float32(NbFrames)*L2))))
		stationarity *= stationarity
		stationarity = round32(stationarity * stationarity)
		frameStationarity += stationarity

		bandTonality[b] = maxf(tE/(1e-15+bandE), stationarity*s.PrevBandTonality[b])
		frameTonality += bandTonality[b]
		if b >= NbTBands-NbTonalSkipBands {
			frameTonality -= bandTonality[b-NbTBands+NbTonalSkipBands]
		}
		maxFrameTonality = maxf(maxFrameTonality, (1.0+0.03*float32(b-NbTBands))*frameTonality)
		slope = analysisSlopeAccumulate(slope, bandTonality[b], float32(b-8))
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
		E := s.analysisHighBandEnergy(hpEner)
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
	s.LowECount = round32(s.LowECount * (1.0 - alphaE))
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
		features[i] = analysisCMeanFeatureTail(
			fma32(-0.12299, BFCC[i]+s.Mem[i+24], round32(0.49195*(s.Mem[i]+s.Mem[i+16])))+
				0.69693*s.Mem[i+8],
			s.CMean[i],
		)
	}
	traceMeanStd := analysisMeanStdTraceEnabled && analysisMeanStdTraceHook != nil && count >= 0 && count <= int(analysisMeanStdTraceMaxCount)
	if traceMeanStd {
		analysisMeanStdTraceBegin(int32(count), alpha, s.CMean[:4], BFCC[:4])
	}
	for i := range 4 {
		s.CMean[i] = analysisCMeanUpdate(alpha, s.CMean[i], BFCC[i])
	}
	if traceMeanStd {
		analysisMeanStdTraceSetCMeanNew(s.CMean[:4])
	}
	for i := range 4 {
		features[4+i] = fma32(0.63246, BFCC[i]-s.Mem[i+24], round32(0.31623*(s.Mem[i]-s.Mem[i+16])))
	}
	for i := range 3 {
		features[8+i] = analysisFeatureMemoryTail(
			fma32(0.53452, BFCC[i]+s.Mem[i+24], -round32(0.26726*(s.Mem[i]+s.Mem[i+16]))),
			s.Mem[i+8],
		)
	}
	if s.Count > 5 {
		if traceMeanStd {
			analysisMeanStdTraceSetStdInput(s.Mem[:4], s.Mem[8:12], s.Mem[16:20], s.Mem[24:28], features[:11], s.Std[:])
		}
		analysisStdUpdate(alpha, &s.Std, &features)
		if traceMeanStd {
			analysisMeanStdTraceSetStdNew(s.Std[:])
		}
	}
	if traceMeanStd {
		analysisMeanStdTraceFinish()
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
	if analysisMLPTraceEnabled && s.Count == 1 && analysisMLPTraceHook != nil {
		var mlpTrace analysisMLPTraceSnapshot
		mlpTrace.Frame = s.Count - 1
		copy(mlpTrace.Dense0Input[:], features[:])
		copy(mlpTrace.GRUStateBefore[:], s.RNNState[:len(mlpTrace.GRUStateBefore)])
		layer0.ComputeDense(layerOut[:], features[:])
		mlpTrace.Dense0Calls = 1
		copy(mlpTrace.Dense0Output[:], layerOut[:])
		copy(mlpTrace.GRUInput[:], layerOut[:])
		layer1.ComputeGRU(s.RNNState[:], layerOut[:])
		mlpTrace.GRUCalls = 1
		copy(mlpTrace.GRUStateAfter[:], s.RNNState[:len(mlpTrace.GRUStateAfter)])
		copy(mlpTrace.Dense2Input[:], s.RNNState[:len(mlpTrace.Dense2Input)])
		layer2.ComputeDense(frameProbs[:], s.RNNState[:])
		mlpTrace.Dense2Calls = 1
		copy(mlpTrace.Dense2Output[:], frameProbs[:])
		analysisMLPTraceHook(mlpTrace)
	} else {
		layer0.ComputeDense(layerOut[:], features[:])
		layer1.ComputeGRU(s.RNNState[:], layerOut[:])
		layer2.ComputeDense(frameProbs[:], s.RNNState[:])
	}
	info.MusicProb = frameProbs[0]
	info.VADProb = frameProbs[1]
	for b := range NbTBands + 1 {
		boost := maxf(0, leakageTo[b]-bandLog2[b]) + maxf(0, bandLog2[b]-(leakageFrom[b]+leakageOffset))
		q6 := max(min(int(opusmath.FloorHalfPlusF32ToInt32(float32(64)*boost)), 255), 0)
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

func maxf(a, b float32) float32 { return opusmath.MaxF32(a, b) }

func minf(a, b float32) float32 { return opusmath.MinF32(a, b) }

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
		probAvg += analysisWeightedProduct32(posWeight, s.Info[mpos].MusicProb)
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
		history := max(min(int(s.Count-1), 15), 0)
		for i := 0; i < history; i++ {
			pos--
			if pos < 0 {
				pos = DetectSize - 1
			}
			pmin = minf(pmin, s.Info[pos].MusicProb)
			pmax = maxf(pmax, s.Info[pos].MusicProb)
		}

		pmin = maxf(0.0, pmin-0.1*vadProb)
		pmax = minf(1.0, pmax+analysisWeightedProduct32(0.1, vadProb))
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
		traceChunk := int32(0)
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
				if analysisMeanStdTraceEnabled && analysisMeanStdTraceHook != nil {
					analysisMeanStdTraceSetChunk(traceChunk)
				}
				s.tonalityAnalysis(pcm[start:end], channels)
				traceChunk++
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
