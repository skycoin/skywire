// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file provides transient detection for short block decisions.
//
// Transient detection identifies frames with sudden energy changes (attacks)
// that benefit from using multiple short MDCTs instead of one long MDCT.
// Short blocks provide better time resolution at the cost of frequency resolution,
// which is crucial for preserving the quality of percussive sounds like drums,
// hand claps, and other impulsive audio.
//
// The encoder path uses TransientAnalysis, which mirrors libopus' transient
// masking metric, and PatchTransientDecisionWithScratch, which mirrors the
// frequency-domain second pass used by libopus.
//
// Reference: libopus celt/celt_encoder.c transient_analysis(), patch_transient_decision()

package celt

import "github.com/thesyncim/gopus/internal/opusmath"

// TransientAnalysisResult holds the results of transient analysis.
// This provides both the transient decision and the tf_estimate metric.
type TransientAnalysisResult struct {
	IsTransient   bool    // Whether a transient was detected
	TfEstimate    float32 // Time-frequency estimate (0.0 = time, 1.0 = freq) for TF analysis bias
	TfChannel     int     // Which channel had the strongest transient (0 or 1)
	MaskMetric    float32 // Raw mask metric value.
	WeakTransient bool    // Whether this is a "weak" transient (for hybrid mode)
	ToneFreq      float32 // Detected tone frequency in radians/sample (-1 if no tone)
	Toneishness   float32 // How "pure" the tone is (0.0-1.0, higher = purer)
}

// toneLPC computes 2nd-order LPC coefficients using forward+backward least-squares fit.
// This is used to detect pure tones by analyzing the resonant characteristics.
// Returns (lpc0, lpc1, success) where success=false if the computation fails.
// Reference: libopus celt/celt_encoder.c tone_lpc()
func toneLPC(x []float32, delay int, lane4Corr bool) (float32, float32, bool) {
	n := len(x)
	if n <= 2*delay {
		return 0, 0, false
	}
	if delay == 1 {
		return toneLPCDelay1(x, lane4Corr)
	}

	// BCE hint: the maximum index accessed in the correlation loop is (cnt-1)+2*delay = n-1.
	_ = x[n-1]

	// Compute correlations using forward prediction covariance method.
	cnt := n - 2*delay
	delay2 := 2 * delay
	var r00, r01, r02 float32
	if lane4Corr {
		r00, r01, r02 = toneLPCCorrLane4(x, cnt, delay, delay2)
	} else {
		r00, r01, r02 = toneLPCCorr(x, cnt, delay, delay2)
	}
	return toneLPCSolveFromCorr(x, delay, r00, r01, r02)
}

func toneLPCSolveFromCorr(x []float32, delay int, r00, r01, r02 float32) (float32, float32, bool) {
	n := len(x)
	delay2 := 2 * delay
	base1 := n - delay2 // n-2*delay
	base2 := n - delay

	var edges float32
	for i := range delay {
		a := x[base1+i]
		b := x[i]
		edges += a*a - b*b
	}
	r11 := r00 + edges

	edges = 0
	for i := range delay {
		a := x[base2+i]
		b := x[i+delay]
		edges += a*a - b*b
	}
	r22 := r11 + edges

	edges = 0
	for i := range delay {
		edges += x[base1+i]*x[base2+i] - x[i]*x[i+delay]
	}
	r12 := r01 + edges

	R00 := r00 + r22
	R01 := r01 + r12
	R11 := 2 * r11
	R02 := 2 * r02
	R12 := r12 + r01

	den := R00*R11 - R01*R01
	if den < float32(0.001)*R00*R11 {
		return 0, 0, false
	}

	num1 := R02*R11 - R01*R12
	var lpc1 float32
	if num1 >= den {
		lpc1 = 1.0
	} else if num1 <= -den {
		lpc1 = -1.0
	} else {
		lpc1 = num1 / den
	}

	num0 := R00*R12 - R02*R01
	var lpc0 float32
	if float32(0.5)*num0 >= den {
		lpc0 = 1.999999
	} else if float32(0.5)*num0 <= -den {
		lpc0 = -1.999999
	} else {
		lpc0 = num0 / den
	}

	return lpc0, lpc1, true
}

func toneLPCDelay1(x []float32, lane4Corr bool) (float32, float32, bool) {
	n := len(x)

	// BCE hint: the maximum index accessed in the correlation loop is n-1.
	_ = x[n-1]

	cnt := n - 2
	var r00, r01, r02 float32
	if lane4Corr {
		r00, r01, r02 = toneLPCCorrLane4(x, cnt, 1, 2)
	} else {
		r00, r01, r02 = toneLPCCorrDelay1(x, cnt)
	}

	r11 := r00 + x[n-2]*x[n-2] - x[0]*x[0]
	r22 := r11 + x[n-1]*x[n-1] - x[1]*x[1]
	r12 := r01 + x[n-2]*x[n-1] - x[0]*x[1]

	R00 := r00 + r22
	R01 := r01 + r12
	R11 := 2 * r11
	R02 := 2 * r02
	R12 := r12 + r01

	den := R00*R11 - R01*R01
	if den < float32(0.001)*R00*R11 {
		return 0, 0, false
	}

	num1 := R02*R11 - R01*R12
	var lpc1 float32
	if num1 >= den {
		lpc1 = 1.0
	} else if num1 <= -den {
		lpc1 = -1.0
	} else {
		lpc1 = num1 / den
	}

	num0 := R00*R12 - R02*R01
	var lpc0 float32
	if float32(0.5)*num0 >= den {
		lpc0 = 1.999999
	} else if float32(0.5)*num0 <= -den {
		lpc0 = -1.999999
	} else {
		lpc0 = num0 / den
	}

	return lpc0, lpc1, true
}

func toneLPCRetryNeeded(lpc0, lpc1 float32, success bool) bool {
	return !success || (lpc0 > float32(1.0) && lpc1 < 0)
}

// toneLPCRetry48kMono fuses the 48 kHz mono retry-delay correlations while
// preserving the ascending accumulation order used by separate toneLPC calls.
func toneLPCRetry48kMono(x []float32, maxDelay int) (float32, float32, bool, int) {
	if !toneLPCRetry48kMonoUseFused {
		return toneLPCRetry48kMonoSequential(x, maxDelay)
	}

	n := len(x)
	_ = x[n-1]
	cnt2 := n - 4
	cnt4 := n - 8
	cnt8 := n - 16
	cnt16 := n - 32
	cnt32 := n - 64

	var r002, r012, r022 float32
	var r004, r014, r024 float32
	var r008, r018, r028 float32
	var r0016, r0116, r0216 float32
	var r0032, r0132, r0232 float32

	i := 0
	for ; i < cnt32; i++ {
		xi := x[i]
		xi2 := xi * xi
		r002 += xi2
		r012 += xi * x[i+2]
		r022 += xi * x[i+4]
		r004 += xi2
		r014 += xi * x[i+4]
		r024 += xi * x[i+8]
		r008 += xi2
		r018 += xi * x[i+8]
		r028 += xi * x[i+16]
		r0016 += xi2
		r0116 += xi * x[i+16]
		r0216 += xi * x[i+32]
		r0032 += xi2
		r0132 += xi * x[i+32]
		r0232 += xi * x[i+64]
	}
	for ; i < cnt16; i++ {
		xi := x[i]
		xi2 := xi * xi
		r002 += xi2
		r012 += xi * x[i+2]
		r022 += xi * x[i+4]
		r004 += xi2
		r014 += xi * x[i+4]
		r024 += xi * x[i+8]
		r008 += xi2
		r018 += xi * x[i+8]
		r028 += xi * x[i+16]
		r0016 += xi2
		r0116 += xi * x[i+16]
		r0216 += xi * x[i+32]
	}
	for ; i < cnt8; i++ {
		xi := x[i]
		xi2 := xi * xi
		r002 += xi2
		r012 += xi * x[i+2]
		r022 += xi * x[i+4]
		r004 += xi2
		r014 += xi * x[i+4]
		r024 += xi * x[i+8]
		r008 += xi2
		r018 += xi * x[i+8]
		r028 += xi * x[i+16]
	}
	for ; i < cnt4; i++ {
		xi := x[i]
		xi2 := xi * xi
		r002 += xi2
		r012 += xi * x[i+2]
		r022 += xi * x[i+4]
		r004 += xi2
		r014 += xi * x[i+4]
		r024 += xi * x[i+8]
	}
	for ; i < cnt2; i++ {
		xi := x[i]
		r002 += xi * xi
		r012 += xi * x[i+2]
		r022 += xi * x[i+4]
	}

	delay := 2
	lpc0, lpc1, success := toneLPCSolveFromCorr(x, delay, r002, r012, r022)
	if delay > maxDelay || !toneLPCRetryNeeded(lpc0, lpc1, success) {
		return lpc0, lpc1, success, delay
	}
	delay = 4
	lpc0, lpc1, success = toneLPCSolveFromCorr(x, delay, r004, r014, r024)
	if delay > maxDelay || !toneLPCRetryNeeded(lpc0, lpc1, success) {
		return lpc0, lpc1, success, delay
	}
	delay = 8
	lpc0, lpc1, success = toneLPCSolveFromCorr(x, delay, r008, r018, r028)
	if delay > maxDelay || !toneLPCRetryNeeded(lpc0, lpc1, success) {
		return lpc0, lpc1, success, delay
	}
	delay = 16
	lpc0, lpc1, success = toneLPCSolveFromCorr(x, delay, r0016, r0116, r0216)
	if delay > maxDelay || !toneLPCRetryNeeded(lpc0, lpc1, success) {
		return lpc0, lpc1, success, delay
	}
	delay = 32
	lpc0, lpc1, success = toneLPCSolveFromCorr(x, delay, r0032, r0132, r0232)
	return lpc0, lpc1, success, delay
}

func toneLPCRetry48kMonoSequential(x []float32, maxDelay int) (float32, float32, bool, int) {
	n := len(x)
	delay := 1
	var lpc0, lpc1 float32
	var success bool
	for delay <= maxDelay {
		delay *= 2
		if 2*delay >= n {
			break
		}
		lpc0, lpc1, success = toneLPC(x, delay, false)
		if !toneLPCRetryNeeded(lpc0, lpc1, success) {
			break
		}
	}
	return lpc0, lpc1, success, delay
}

// toneLPCCorrLane4 mirrors the four-lane reduction shape used by the
// libopus float build for non-amd64 stereo tone detection.
func toneLPCCorrLane4(x []float32, cnt, delay, delay2 int) (r00, r01, r02 float32) {
	i := 0
	var r00v0, r00v1, r00v2, r00v3 float32
	var r01v0, r01v1, r01v2, r01v3 float32
	var r02v0, r02v1, r02v2, r02v3 float32
	for ; i+3 < cnt; i += 4 {
		xi := x[i]
		r00v0 += xi * xi
		r01v0 += xi * x[i+delay]
		r02v0 += xi * x[i+delay2]

		xi = x[i+1]
		r00v1 += xi * xi
		r01v1 += xi * x[i+1+delay]
		r02v1 += xi * x[i+1+delay2]

		xi = x[i+2]
		r00v2 += xi * xi
		r01v2 += xi * x[i+2+delay]
		r02v2 += xi * x[i+2+delay2]

		xi = x[i+3]
		r00v3 += xi * xi
		r01v3 += xi * x[i+3+delay]
		r02v3 += xi * x[i+3+delay2]
	}
	r00 = (r00v0 + r00v1) + (r00v2 + r00v3)
	r01 = (r01v0 + r01v1) + (r01v2 + r01v3)
	r02 = (r02v0 + r02v1) + (r02v2 + r02v3)
	for ; i < cnt; i++ {
		xi := x[i]
		r00 += xi * xi
		r01 += xi * x[i+delay]
		r02 += xi * x[i+delay2]
	}
	return
}

// toneDetectScratch is the scratch-aware version of toneDetect.
func toneDetectScratch(in []float32, channels int, sampleRate int, xBuf []float32) (float32, float32) {
	n := len(in) / channels
	if n < 4 {
		return -1, 0
	}

	// Use provided buffer or allocate
	var x []float32
	if xBuf != nil && len(xBuf) >= n {
		x = xBuf[:n]
	} else {
		x = make([]float32, n)
	}

	// Mix down to mono if stereo (matching libopus behavior)
	if channels == 2 {
		for i := range n {
			// libopus sums the two celt_sig float channels inside tone_detect.
			x[i] = in[i*2] + in[i*2+1]
		}
	} else {
		copy(x, in[:n])
	}

	lane4Corr := channels == 2 && toneLPCStereoLane4
	return toneDetectFloat32Mono(x, sampleRate, lane4Corr)
}

func toneDetectScratchF32(in []float32, channels int, sampleRate int, xBuf []float32) (float32, float32) {
	n := len(in) / channels
	if n < 4 {
		return -1, 0
	}

	var x []float32
	if xBuf != nil && len(xBuf) >= n {
		x = xBuf[:n]
	} else {
		x = make([]float32, n)
	}

	if channels == 2 {
		for i := range n {
			x[i] = in[i*2] + in[i*2+1]
		}
	} else {
		copy(x, in[:n])
	}

	lane4Corr := channels == 2 && toneLPCStereoLane4
	return toneDetectFloat32Mono(x, sampleRate, lane4Corr)
}

func toneDetectFloat32Mono(x []float32, sampleRate int, lane4Corr bool) (float32, float32) {
	n := len(x)
	if n < 4 {
		return -1, 0
	}

	delay := 1
	lpc0, lpc1, success := toneLPCDelay1(x, lane4Corr)

	// If LPC resonates too close to DC, retry with downsampling
	// (delay <= sampleRate/3000 corresponds to frequencies > ~1500 Hz)
	maxDelay := max(sampleRate/3000, 1)
	if !lane4Corr && sampleRate == 48000 && n > 64 && delay <= maxDelay && toneLPCRetryNeeded(lpc0, lpc1, success) {
		lpc0, lpc1, success, delay = toneLPCRetry48kMono(x, maxDelay)
	} else {
		for delay <= maxDelay && toneLPCRetryNeeded(lpc0, lpc1, success) {
			delay *= 2
			if 2*delay >= n {
				break
			}
			lpc0, lpc1, success = toneLPC(x, delay, lane4Corr)
		}
	}

	// Check that our filter has complex roots: lpc0^2 + 4*lpc1 < 0
	// This indicates a resonant (tonal) system
	if success && (lpc0*lpc0+float32(3.999999)*lpc1) < 0 {
		// Toneishness is the squared radius of the poles.
		toneishness := -lpc1
		// Frequency from the angle of the complex pole.
		freq := opusmath.AcosF32(float32(0.5)*lpc0) / float32(delay)
		return freq, toneishness
	}

	return -1, 0
}

// TransientAnalysis performs full transient analysis matching libopus.
// This computes:
//   - Whether the frame is transient (should use short blocks)
//   - tf_estimate: bias for TF resolution analysis (0 = time, 1 = freq)
//   - tf_chan: which channel has the strongest transient
//
// The algorithm uses a high-pass filter followed by forward/backward masking
// to detect temporal energy variations. The mask_metric measures how much
// the signal energy varies over time relative to a masked threshold.
//
// Parameters:
//   - pcm: input PCM samples (mono or interleaved stereo)
//   - frameSize: frame size in samples (120, 240, 480, or 960)
//   - allowWeakTransients: for hybrid mode at low bitrate
//
// Returns: TransientAnalysisResult with all metrics
//
// Reference: libopus celt/celt_encoder.c transient_analysis()
func (e *Encoder) TransientAnalysis(pcm []float32, frameSize int, allowWeakTransients bool) TransientAnalysisResult {
	return e.transientAnalysisScratchF32(pcm, frameSize, allowWeakTransients,
		e.scratch.transientX,
		e.scratch.transientEnergy)
}

// TransientAnalysisF32 is an alias for TransientAnalysis kept for the float32
// naming convention used elsewhere in the encoder API.
func (e *Encoder) TransientAnalysisF32(pcm []float32, frameSize int, allowWeakTransients bool) TransientAnalysisResult {
	return e.TransientAnalysis(pcm, frameSize, allowWeakTransients)
}

// toneDetectOnlyF32 runs only tone_detect and leaves all transient outputs
// zeroed. libopus calls tone_detect() unconditionally (celt_encoder.c:2020) but
// gates transient_analysis() behind st->complexity >= 1 && !st->lfe
// (celt_encoder.c:2023); at complexity 0 isTransient/tf_estimate/tf_chan/
// weak_transient therefore stay 0 while toneishness/tone_freq still come from
// tone_detect.
func (e *Encoder) toneDetectOnlyF32(pcm []float32, frameSize int) TransientAnalysisResult {
	result := TransientAnalysisResult{
		TfEstimate:  0.0,
		TfChannel:   0,
		ToneFreq:    -1,
		Toneishness: 0,
	}
	channels := int(e.channels)
	if len(pcm) == 0 || frameSize <= 0 || channels <= 0 {
		return result
	}
	samplesPerChannel := len(pcm) / channels
	if samplesPerChannel < 16 {
		return result
	}
	if channels == 1 {
		result.ToneFreq, result.Toneishness = toneDetectFloat32Mono(pcm[:samplesPerChannel], e.toneDetectFs(), false)
	} else {
		result.ToneFreq, result.Toneishness = toneDetectScratchF32(pcm, channels, e.toneDetectFs(), nil)
	}
	return result
}

func (e *Encoder) transientAnalysisMonoFloat32(pcm []float32, frameSize int, allowWeakTransients bool) TransientAnalysisResult {
	result := TransientAnalysisResult{
		TfEstimate:  0.0,
		TfChannel:   0,
		ToneFreq:    -1,
		Toneishness: 0,
	}

	if len(pcm) == 0 || frameSize <= 0 {
		return result
	}
	samplesPerChannel := len(pcm)
	if samplesPerChannel < 16 {
		return result
	}

	toneFreq, toneishness := toneDetectFloat32Mono(pcm[:samplesPerChannel], e.toneDetectFs(), false)
	result.ToneFreq = toneFreq
	result.Toneishness = toneishness

	forwardDecay := float32(0.0625)
	forwardRetain := float32(1.0) - forwardDecay
	if allowWeakTransients {
		forwardDecay = 0.03125
		forwardRetain = float32(1.0) - forwardDecay
	}

	len2 := samplesPerChannel / 2
	var energy []float32
	if len(e.scratch.transientEnergy) >= len2 {
		energy = e.scratch.transientEnergy[:len2]
	} else {
		energy = make([]float32, len2)
	}

	const (
		hpFeedback     = float32(0.5)
		backwardRetain = float32(0.875)
		backwardScale  = float32(0.125)
		warmupPairs    = 6
	)
	var mask float32
	mean := float32(0)
	src := pcm[:samplesPerChannel]
	_ = src[2*len2-1]

	if celtFusedFloat {
		// Numerically-equivalent reform of the libopus high-pass filter. The
		// reference form per sample is
		//     y = a + x ; a' = a - x + hpFeedback*b ; b' = x - a
		// which serializes as FSUB->FMADD on the (a,b) state. Substituting
		// b_n = x_{n-1} - a_{n-1} collapses it to a single second-order
		// recurrence whose input term is state-independent:
		//     a_{n+1} = a_n + ((hpFeedback*x_{n-1} - x_n) - hpFeedback*a_{n-1})
		// leaving one add on the critical path instead of two ops, ~2x shorter.
		// The (hp0,hp1) state is frame-local (reset each call), so only the y
		// sequence must match; it does within ~1 ULP, which the quality-gated
		// fused build allows (same posture as the decode de-emphasis reform).
		var a, aPrev, xPrev float32
		for i := range len2 {
			j := i << 1

			x0 := src[j]
			term0 := (hpFeedback*xPrev - x0) - hpFeedback*aPrev
			y0 := a + x0
			aPrev = a
			a = a + term0
			xPrev = x0

			x1 := src[j+1]
			term1 := (hpFeedback*xPrev - x1) - hpFeedback*aPrev
			y1 := a + x1
			aPrev = a
			a = a + term1
			xPrev = x1

			if i < warmupPairs {
				y0 = 0
				y1 = 0
			}

			pair := y0*y0 + y1*y1
			mean += pair
			mask = pair + forwardRetain*mask
			energy[i] = forwardDecay * mask
		}
	} else {
		var hp0, hp1 float32
		for i := range len2 {
			j := i << 1

			x0 := src[j]
			y0 := hp0 + x0
			hp00 := hp0
			hp0 = hp0 - x0 + hpFeedback*hp1
			hp1 = x0 - hp00

			x1 := src[j+1]
			y1 := hp0 + x1
			hp00 = hp0
			hp0 = hp0 - x1 + hpFeedback*hp1
			hp1 = x1 - hp00

			if i < warmupPairs {
				y0 = 0
				y1 = 0
			}

			pair := y0*y0 + y1*y1
			mean += pair
			mask = pair + forwardRetain*mask
			energy[i] = forwardDecay * mask
		}
	}

	var maxE float32
	mask = 0
	for i := len2; i > 0; {
		i--
		mask = energy[i] + backwardRetain*mask
		ei := backwardScale * mask
		energy[i] = ei
		maxE = max(maxE, ei)
	}

	meanGeom := opusmath.SqrtF32(mean * maxE * float32(0.5*float32(len2)))
	const epsilon = 1e-15
	normE := float32(64*len2) / (meanGeom + epsilon)

	const epsF32 = float32(1e-15)
	var unmask int
	for i := 12; i < len2-5; i += 4 {
		id := min(int(normE*(energy[i]+epsF32)), 127)
		unmask += transientInvTable[id]
	}

	maxMaskMetric := 0
	if len2 > 17 {
		maxMaskMetric = 64 * unmask * 4 / (6 * (len2 - 17))
	}

	result.TfChannel = 0
	result.IsTransient = maxMaskMetric > 200
	if result.Toneishness > 0.98 && result.ToneFreq >= 0 && result.ToneFreq < 0.026 {
		result.IsTransient = false
		maxMaskMetric = 0
	}
	if allowWeakTransients && result.IsTransient && maxMaskMetric < 600 {
		result.IsTransient = false
		result.WeakTransient = true
	}
	result.MaskMetric = float32(maxMaskMetric)

	tfMax := opusmath.SqrtF32(float32(27*maxMaskMetric)) - 42
	if tfMax < 0 {
		tfMax = 0
	}
	if tfMax > 163 {
		tfMax = 163
	}
	tfEstimateSquared := float32(0.0069)*tfMax - float32(0.139)
	if tfEstimateSquared < 0 {
		tfEstimateSquared = 0
	}
	result.TfEstimate = opusmath.SqrtF32(tfEstimateSquared)
	if result.TfEstimate > 1.0 {
		result.TfEstimate = 1.0
	}

	return result
}

// transientInvTable is the inverse table for computing harmonic mean (6*64/x, trained on real data).
// Hoisted to package level to avoid re-initializing on every call. This matches libopus exactly.
var transientInvTable = [128]int{
	255, 255, 156, 110, 86, 70, 59, 51, 45, 40, 37, 33, 31, 28, 26, 25,
	23, 22, 21, 20, 19, 18, 17, 16, 16, 15, 15, 14, 13, 13, 12, 12,
	12, 12, 11, 11, 11, 10, 10, 10, 9, 9, 9, 9, 9, 9, 8, 8,
	8, 8, 8, 7, 7, 7, 7, 7, 7, 6, 6, 6, 6, 6, 6, 6,
	6, 6, 6, 6, 6, 6, 6, 6, 6, 5, 5, 5, 5, 5, 5, 5,
	5, 5, 5, 5, 5, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4,
	4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 3, 3,
	3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 2,
}

// transientAnalysisScratch is the scratch-aware version of TransientAnalysis.
func (e *Encoder) transientAnalysisScratchF32(pcm []float32, frameSize int, allowWeakTransients bool,
	toneBuf []float32, energyBuf []float32) TransientAnalysisResult {
	result := TransientAnalysisResult{
		TfEstimate:  0.0,
		TfChannel:   0,
		ToneFreq:    -1,
		Toneishness: 0,
	}

	if len(pcm) == 0 || frameSize <= 0 {
		return result
	}

	channels := int(e.channels)
	samplesPerChannel := len(pcm) / channels
	if samplesPerChannel < 16 {
		return result
	}

	// Detect pure tones before transient analysis. Mono can fill the tone
	// buffer while computing pair energies below; stereo keeps the standalone
	// path because toneBuf is reused for right-channel energy.
	deferMonoToneDetect := channels == 1 && len(toneBuf) >= samplesPerChannel
	deferStereoToneDetect := channels == 2 && len(toneBuf) >= samplesPerChannel && len(e.scratch.transientEnergyR) >= samplesPerChannel/2
	var monoToneX []float32
	if deferMonoToneDetect {
		monoToneX = toneBuf[:samplesPerChannel]
	} else if !deferStereoToneDetect {
		toneFreq, toneishness := toneDetectScratchF32(pcm, channels, e.toneDetectFs(), toneBuf)
		result.ToneFreq = toneFreq
		result.Toneishness = toneishness
	}

	// Forward masking decay: 6.7 dB/ms (default) or 3.3 dB/ms (weak transients)
	// At 48kHz, we process pairs of samples, so decay per pair:
	// Default: forward_decay = 0.0625 (1/16)
	// Weak: forward_decay = 0.03125 (1/32)
	// Precompute retain = 1 - decay to avoid per-iteration subtraction.
	forwardDecay := float32(0.0625)
	forwardRetain := float32(1.0) - forwardDecay
	if allowWeakTransients {
		forwardDecay = 0.03125
		forwardRetain = float32(1.0) - forwardDecay
	}

	var maxMaskMetric int
	tfChannel := 0

	len2 := samplesPerChannel / 2
	var energy []float32
	if energyBuf != nil && len(energyBuf) >= len2 {
		energy = energyBuf[:len2]
	} else {
		energy = make([]float32, len2)
	}

	// Stereo can process both channels in one pass over the interleaved PCM
	// while preserving each channel's exact arithmetic order.
	if channels == 2 {
		var energyR []float32
		if deferStereoToneDetect {
			energyR = e.scratch.transientEnergyR[:len2]
		} else if len(toneBuf) >= len2 {
			energyR = toneBuf[:len2]
		} else {
			energyR = make([]float32, len2)
		}

		const (
			hpFeedback     = float32(0.5)
			backwardRetain = float32(0.875)
			backwardScale  = float32(0.125)
			warmupPairs    = 6
		)
		var hp0L, hp1L float32
		var hp0R, hp1R float32
		var maskL, maskR float32
		meanL := float32(0)
		meanR := float32(0)
		idx := 0
		_ = pcm[4*len2-1]
		for i := range len2 {
			xL0 := float32(pcm[idx])
			xR0 := float32(pcm[idx+1])
			xL1 := float32(pcm[idx+2])
			xR1 := float32(pcm[idx+3])
			if deferStereoToneDetect {
				toneBuf[i<<1] = xL0 + xR0
				toneBuf[(i<<1)+1] = xL1 + xR1
			}
			idx += 4

			// L and R high-pass filter computations interleaved so the CPU
			// can overlap their independent chains to hide IIR multiply latency.
			yL0 := hp0L + xL0; yR0 := hp0R + xR0
			hp00L := hp0L; hp00R := hp0R
			hp0L = hp0L - xL0 + hpFeedback*hp1L
			hp0R = hp0R - xR0 + hpFeedback*hp1R
			hp1L = xL0 - hp00L; hp1R = xR0 - hp00R

			yL1 := hp0L + xL1; yR1 := hp0R + xR1
			hp00L = hp0L; hp00R = hp0R
			hp0L = hp0L - xL1 + hpFeedback*hp1L
			hp0R = hp0R - xR1 + hpFeedback*hp1R
			hp1L = xL1 - hp00L; hp1R = xR1 - hp00R

			if i < warmupPairs {
				yL0, yL1, yR0, yR1 = 0, 0, 0, 0
			}

			// Energy and masking for both channels interleaved.
			pairL := yL0*yL0 + yL1*yL1
			pairR := yR0*yR0 + yR1*yR1
			meanL += pairL; meanR += pairR
			maskL = pairL + forwardRetain*maskL
			maskR = pairR + forwardRetain*maskR
			energy[i] = forwardDecay * maskL
			energyR[i] = forwardDecay * maskR
		}

		var maxEL, maxER float32
		maskL = 0
		maskR = 0
		for i := len2 - 1; i >= 0; i-- {
			maskL = energy[i] + backwardRetain*maskL
			maskR = energyR[i] + backwardRetain*maskR
			eiL := backwardScale * maskL
			eiR := backwardScale * maskR
			energy[i] = eiL
			energyR[i] = eiR
			// Branchless running max (FMAXS): bit-identical for these non-negative
			// finite energies, avoids a per-sample data-dependent branch.
			maxEL = max(maxEL, eiL)
			maxER = max(maxER, eiR)
		}

		const epsilon = 1e-15
		normEL := float32(64*len2) / (opusmath.SqrtF32(meanL*maxEL*float32(0.5*float32(len2))) + epsilon)
		normER := float32(64*len2) / (opusmath.SqrtF32(meanR*maxER*float32(0.5*float32(len2))) + epsilon)

		const epsF32 = float32(1e-15)
		var unmaskL, unmaskR int
		for i := 12; i < len2-5; i += 4 {
			idL := min(int(normEL*(energy[i]+epsF32)), 127)
			unmaskL += transientInvTable[idL]

			idR := min(int(normER*(energyR[i]+epsF32)), 127)
			unmaskR += transientInvTable[idR]
		}

		maskMetricL := 0
		if len2 > 17 {
			maskMetricL = 64 * unmaskL * 4 / (6 * (len2 - 17))
		}
		if maskMetricL > maxMaskMetric {
			tfChannel = 0
			maxMaskMetric = maskMetricL
		}
		maskMetricR := 0
		if len2 > 17 {
			maskMetricR = 64 * unmaskR * 4 / (6 * (len2 - 17))
		}
		if maskMetricR > maxMaskMetric {
			tfChannel = 1
			maxMaskMetric = maskMetricR
		}
		if deferStereoToneDetect {
			toneFreq, toneishness := toneDetectFloat32Mono(toneBuf[:samplesPerChannel], e.toneDetectFs(), toneLPCStereoLane4)
			result.ToneFreq = toneFreq
			result.Toneishness = toneishness
		}
		goto transientMetricsDone
	}

	// Process each channel
	for c := range channels {
		// Fuse the HP filter with pair-energy accumulation so we don't round-trip
		// through a temporary sample buffer before masking.
		_ = energy[len2-1]
		const (
			hpFeedback     = float32(0.5)
			backwardRetain = float32(0.875)
			backwardScale  = float32(0.125)
			warmupPairs    = 6
		)
		var hp0, hp1 float32
		var mask float32
		mean := float32(0)
		if channels == 1 {
			src := pcm[:samplesPerChannel]
			_ = src[2*len2-1]
			for i := range len2 {
				j := i << 1

				x0 := float32(src[j])
				if deferMonoToneDetect {
					monoToneX[j] = x0
				}
				y0 := hp0 + x0
				hp00 := hp0
				hp0 = hp0 - x0 + hpFeedback*hp1
				hp1 = x0 - hp00

				x1 := float32(src[j+1])
				if deferMonoToneDetect {
					monoToneX[j+1] = x1
				}
				y1 := hp0 + x1
				hp00 = hp0
				hp0 = hp0 - x1 + hpFeedback*hp1
				hp1 = x1 - hp00

				if i < warmupPairs {
					y0 = 0
					y1 = 0
				}

				pair := y0*y0 + y1*y1
				mean += pair
				mask = pair + forwardRetain*mask
				energy[i] = forwardDecay * mask
			}
			if deferMonoToneDetect && samplesPerChannel > 2*len2 {
				monoToneX[samplesPerChannel-1] = float32(src[samplesPerChannel-1])
			}
		} else {
			stride := channels
			idx := c
			_ = pcm[(2*len2-1)*stride+c]
			for i := range len2 {
				x0 := float32(pcm[idx])
				idx += stride
				y0 := hp0 + x0
				hp00 := hp0
				hp0 = hp0 - x0 + hpFeedback*hp1
				hp1 = x0 - hp00

				x1 := float32(pcm[idx])
				idx += stride
				y1 := hp0 + x1
				hp00 = hp0
				hp0 = hp0 - x1 + hpFeedback*hp1
				hp1 = x1 - hp00

				if i < warmupPairs {
					y0 = 0
					y1 = 0
				}

				pair := y0*y0 + y1*y1
				mean += pair
				mask = pair + forwardRetain*mask
				energy[i] = forwardDecay * mask
			}
		}

		// Backward pass: compute pre-echo threshold
		// Backward masking: 13.9 dB/ms (decay = 0.125)
		var maxE float32
		mask = 0
		for i := len2 - 1; i >= 0; i-- {
			mask = energy[i] + backwardRetain*mask
			ei := backwardScale * mask
			energy[i] = ei
			maxE = max(maxE, ei)
		}

		// Compute frame energy as geometric mean of mean and max
		// This is a compromise between old and new transient detectors
		meanGeom := opusmath.SqrtF32(mean * maxE * float32(0.5*float32(len2)))

		// Inverse of mean energy (with epsilon to avoid division by zero)
		const epsilon = 1e-15
		normE := float32(64*len2) / (meanGeom + epsilon)

		// Compute harmonic mean using inverse table
		// Skip unreliable boundaries, sample every 4th point
		const epsF32 = float32(1e-15)
		var unmask int
		for i := 12; i < len2-5; i += 4 {
			// Map energy to table index
			// For non-negative values, int(x) truncates toward zero which equals floor.
			// energy[i] + epsilon is always >= 0, so int() is equivalent to math.Floor.
			id := min(int(normE*(energy[i]+epsF32)), 127)
			unmask += transientInvTable[id]
		}

		// Use the exact integer normalization from libopus:
		// mask_metric = 64*unmask*4/(6*(len2-17))
		maskMetric := 0
		if len2 > 17 {
			maskMetric = 64 * unmask * 4 / (6 * (len2 - 17))
		}

		if maskMetric > maxMaskMetric {
			tfChannel = c
			maxMaskMetric = maskMetric
		}
	}

	if deferMonoToneDetect {
		toneFreq, toneishness := toneDetectFloat32Mono(monoToneX, e.toneDetectFs(), false)
		result.ToneFreq = toneFreq
		result.Toneishness = toneishness
	}

transientMetricsDone:
	result.TfChannel = tfChannel

	// Transient decision: mask_metric > 200
	result.IsTransient = maxMaskMetric > 200

	// Prevent the transient detector from confusing the partial cycle of a
	// very low frequency tone with a transient.
	// Reference: libopus celt/celt_encoder.c lines 445-451
	// toneishness > 0.98 AND tone_freq < 0.026 radians/sample (~198 Hz at 48kHz)
	// Note: This check ONLY applies to very low frequency tones. Higher frequency
	// pure tones (e.g., 440 Hz) can legitimately trigger transient detection,
	// especially on the first frame where pre-emphasis buffer is empty.
	if result.Toneishness > 0.98 && result.ToneFreq >= 0 && result.ToneFreq < 0.026 {
		result.IsTransient = false
		maxMaskMetric = 0
	}

	// Weak transient handling for hybrid mode
	if allowWeakTransients && result.IsTransient && maxMaskMetric < 600 {
		result.IsTransient = false
		result.WeakTransient = true
	}
	result.MaskMetric = float32(maxMaskMetric)

	// Compute tf_estimate from mask_metric
	// tf_max = max(0, sqrt(27 * mask_metric) - 42)
	// tf_estimate = sqrt(max(0, 0.0069 * min(163, tf_max) - 0.139))
	// Avoid math.Max/math.Min calls -- use branchless clamping.
	tfMax := opusmath.SqrtF32(float32(27*maxMaskMetric)) - 42
	if tfMax < 0 {
		tfMax = 0
	}
	if tfMax > 163 {
		tfMax = 163
	}
	tfEstimateSquared := float32(0.0069)*tfMax - float32(0.139)
	if tfEstimateSquared < 0 {
		tfEstimateSquared = 0
	}
	result.TfEstimate = opusmath.SqrtF32(tfEstimateSquared)

	// Clamp to [0, 1] range
	if result.TfEstimate > 1.0 {
		result.TfEstimate = 1.0
	}

	return result
}

// DetectTransient analyzes PCM for sudden energy changes.
// Returns true if the frame should use short MDCT blocks.
//
// Transient detection identifies frames with:
// - Sharp attacks (drum hits, plucks)
// - Sudden silence
// - Energy jumps > 6dB between adjacent sub-blocks
//
// When transient is detected, the encoder uses multiple short MDCTs instead
// of one long MDCT for better time resolution at the cost of frequency resolution.
//
// Parameters:
//   - pcm: input PCM samples (mono or interleaved stereo)
//   - frameSize: frame size in samples (120, 240, 480, or 960)
//
// Returns: true if transient detected and short blocks should be used
//
// Reference: RFC 6716 Section 4.3.1, libopus celt/celt_encoder.c
func (e *Encoder) DetectTransient(pcm []float32, frameSize int) bool {
	return e.TransientAnalysis(pcm, frameSize, false).IsTransient
}

// PatchTransientDecisionWithScratch looks for sudden energy increases to decide
// whether to force short blocks, taking the spread-old-energy workspace from
// caller-owned scratch. It mirrors libopus celt/celt_encoder.c
// patch_transient_decision().
func PatchTransientDecisionWithScratch(newE []celtGLog, oldE []celtGLog, nbEBands, start, end, channels int, spreadOld []celtGLog) bool {
	if len(newE) < end || len(oldE) < end {
		return false
	}

	// Apply an aggressive (-6 dB/Bark) spreading function to the old frame
	// to avoid false detection caused by irrelevant bands.
	// GCONST(1.0f) in libopus is 1.0 in the log-energy domain (corresponds to ~6dB).
	if len(spreadOld) < end {
		spreadOld = make([]celtGLog, end)
	} else {
		spreadOld = spreadOld[:end]
	}

	if channels == 1 {
		spreadOld[start] = oldE[start]
		for i := start + 1; i < end; i++ {
			v := spreadOld[i-1] - 1.0
			if oldE[i] > v {
				v = oldE[i]
			}
			spreadOld[i] = v
		}
	} else {
		// Stereo: use max of left and right channel
		v := oldE[start]
		if oldE[start+nbEBands] > v {
			v = oldE[start+nbEBands]
		}
		spreadOld[start] = v
		for i := start + 1; i < end; i++ {
			v = oldE[i]
			if oldE[i+nbEBands] > v {
				v = oldE[i+nbEBands]
			}
			if prev := spreadOld[i-1] - 1.0; prev > v {
				v = prev
			}
			spreadOld[i] = v
		}
	}

	// Backward pass: spread from high to low frequencies
	for i := end - 2; i >= start; i-- {
		if v := spreadOld[i+1] - 1.0; v > spreadOld[i] {
			spreadOld[i] = v
		}
	}

	// Compute mean increase
	var meanDiff celtGLog
	startBand := max(start, 2)

	for c := range channels {
		for i := startBand; i < end-1; i++ {
			x1 := newE[i+c*nbEBands]
			if x1 < 0 {
				x1 = 0
			}
			x2 := spreadOld[i]
			if x2 < 0 {
				x2 = 0
			}
			if diff := x1 - x2; diff > 0 {
				meanDiff += diff
			}
		}
	}

	numBands := max(end-1-startBand, 1)
	meanDiff /= celtGLog(channels * numBands)

	// Return true if mean increase > 1.0 (in log domain, this is ~6 dB)
	return meanDiff > 1.0
}
