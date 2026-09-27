package silk

import "math"

// Pitch estimation constants from libopus pitch_est_defines.h
const (
	peMaxNbSubfr       = 4
	peSubfrLengthMS    = 5
	peLTPMemLengthMS   = 20 // 4 * PE_SUBFR_LENGTH_MS
	peMaxLagMS         = 18 // 18 ms -> 56 Hz
	peMinLagMS         = 2  // 2 ms -> 500 Hz
	peDSrchLength      = 24
	peNbStage3Lags     = 5
	peNbCbksStage2     = 3
	peNbCbksStage2Ext  = 11
	peNbCbksStage3Max  = 34
	peNbCbksStage3Mid  = 24
	peNbCbksStage3Min  = 16
	peNbCbksStage310ms = 12
	peNbCbksStage210ms = 3

	// Aliases for libopus_decode.go compatibility
	peMinLagMs          = peMinLagMS
	peMaxLagMs          = peMaxLagMS
	peNbCbksStage2_10ms = peNbCbksStage210ms
	peNbCbksStage3_10ms = peNbCbksStage310ms

	// Bias constants from libopus
	peShortlagBias    = 0.2 // for logarithmic weighting
	pePrevlagBias     = 0.2 // for logarithmic weighting
	peFlatcontourBias = 0.05
)

// Pitch contour codebooks for stage 2 (matching libopus silk_CB_lags_stage2)
var pitchCBLagsStage2 = [peMaxNbSubfr][peNbCbksStage2Ext]int8{
	{0, 2, -1, -1, -1, 0, 0, 1, 1, 0, 1},
	{0, 1, 0, 0, 0, 0, 0, 1, 0, 0, 0},
	{0, 0, 1, 0, 0, 0, 1, 0, 0, 0, 0},
	{0, -1, 2, 1, 0, 1, 1, 0, 0, -1, -1},
}

// Pitch contour codebooks for stage 3 (matching libopus silk_CB_lags_stage3)
var pitchCBLagsStage3 = [peMaxNbSubfr][peNbCbksStage3Max]int8{
	{0, 0, 1, -1, 0, 1, -1, 0, -1, 1, -2, 2, -2, -2, 2, -3, 2, 3, -3, -4, 3, -4, 4, 4, -5, 5, -6, -5, 6, -7, 6, 5, 8, -9},
	{0, 0, 1, 0, 0, 0, 0, 0, 0, 0, -1, 1, 0, 0, 1, -1, 0, 1, -1, -1, 1, -1, 2, 1, -1, 2, -2, -2, 2, -2, 2, 2, 3, -3},
	{0, 1, 0, 0, 0, 0, 0, 0, 1, 0, 1, 0, 0, 1, -1, 1, 0, 0, 2, 1, -1, 2, -1, -1, 2, -1, 2, 2, -1, 3, -2, -2, -2, 3},
	{0, 1, 0, 0, 1, 0, 1, -1, 2, -1, 2, -1, 2, 3, -2, 3, -2, -2, 4, 4, -3, 5, -3, -4, 6, -4, 6, 5, -5, 8, -6, -5, -7, 9},
}

// Lag range for stage 3 by complexity (matching libopus silk_Lag_range_stage3)
var pitchLagRangeStage3 = [3][peMaxNbSubfr][2]int8{
	// Low complexity
	{{-5, 8}, {-1, 6}, {-1, 6}, {-4, 10}},
	// Mid complexity
	{{-6, 10}, {-2, 6}, {-1, 6}, {-5, 10}},
	// Max complexity
	{{-9, 12}, {-3, 7}, {-2, 7}, {-7, 13}},
}

// Number of codebook searches per stage 3 complexity
var pitchNbCbkSearchsStage3 = [3]int{peNbCbksStage3Min, peNbCbksStage3Mid, peNbCbksStage3Max}

// Pitch contour codebooks for 10ms frames (stage 2)
var pitchCBLagsStage210ms = [2][peNbCbksStage210ms]int8{
	{0, 1, 0},
	{0, 0, 1},
}

// Pitch contour codebooks for 10ms frames (stage 3)
var pitchCBLagsStage310ms = [2][peNbCbksStage310ms]int8{
	{0, 0, 1, -1, 1, -1, 2, -2, 2, -2, 3, -3},
	{0, 1, 0, 1, -1, 2, -1, 2, -2, 3, -2, 3},
}

// Lag range for stage 3 10ms frames
var pitchLagRangeStage310ms = [2][2]int8{
	{-3, 7},
	{-2, 7},
}

var pitchCBLagsStage2Slice = [][]int8{
	pitchCBLagsStage2[0][:],
	pitchCBLagsStage2[1][:],
	pitchCBLagsStage2[2][:],
	pitchCBLagsStage2[3][:],
}

var pitchCBLagsStage210msSlice = [][]int8{
	pitchCBLagsStage210ms[0][:],
	pitchCBLagsStage210ms[1][:],
}

var pitchCBLagsStage3Slice = [][]int8{
	pitchCBLagsStage3[0][:],
	pitchCBLagsStage3[1][:],
	pitchCBLagsStage3[2][:],
	pitchCBLagsStage3[3][:],
}

var pitchCBLagsStage310msSlice = [][]int8{
	pitchCBLagsStage310ms[0][:],
	pitchCBLagsStage310ms[1][:],
}

// PitchAnalysisState holds state for pitch analysis across frames
type PitchAnalysisState struct {
	prevLag int32   // Previous frame's pitch lag (libopus int domain)
	ltpCorr float32 // LTP correlation from previous frame (silk_float in libopus)
}

type pitchEncodeParams struct {
	contourIdx  int
	lagIdx      int
	contourICDF []uint8
	lagLowICDF  []uint8
}

// detectPitch performs three-stage coarse-to-fine pitch detection matching libopus.
// Input samples must be in int16 scale (same scale as silk_pitch_analysis_core_FLP).
// Returns pitch lags for each subframe (voiced frames only).
//
// Per libopus pitch_analysis_core_FLP.c:
// Stage 1: Coarse search at 4kHz with normalized autocorrelation
// Stage 2: Refined search at 8kHz with contour codebook
// Stage 3: Fine search at full rate with interpolation
func (e *Encoder) detectPitch(pcm []float32, numSubframes int, searchThres1, searchThres2 float32) ([]int32, int, int) {
	config := GetBandwidthConfig(e.bandwidth)
	fsKHz := config.SampleRate / 1000

	// Frame length parameters matching libopus
	frameLength := (peLTPMemLengthMS + numSubframes*peSubfrLengthMS) * fsKHz
	frameLength4kHz := (peLTPMemLengthMS + numSubframes*peSubfrLengthMS) * 4
	frameLength8kHz := (peLTPMemLengthMS + numSubframes*peSubfrLengthMS) * 8
	sfLength := peSubfrLengthMS * fsKHz
	sfLength4kHz := peSubfrLengthMS * 4
	sfLength8kHz := peSubfrLengthMS * 8
	minLag := peMinLagMS * fsKHz
	minLag4kHz := peMinLagMS * 4
	minLag8kHz := peMinLagMS * 8
	maxLag := peMaxLagMS*fsKHz - 1
	maxPitchLag := peMaxLagMS * fsKHz
	maxLag4kHz := peMaxLagMS * 4
	maxLag8kHz := peMaxLagMS*8 - 1
	maxPitchLag8kHz := peMaxLagMS * 8

	// Ensure we have enough samples
	if len(pcm) < frameLength {
		frameLength = len(pcm)
	}
	if frameLength <= 0 {
		return nil, 0, 0
	}

	frameFix := ensureInt16Slice(&e.scratchFrame16Fix, frameLength)
	// Input is already in int16 scale (libopus pitch_analysis_core_FLP expects that).
	floatToInt16SliceScaled(frameFix, pcm[:frameLength], 1.0)

	// Resample to 8kHz using libopus down2/down2_3
	var frame8Fix []int16
	switch fsKHz {
	case 16:
		frame8Fix = ensureInt16Slice(&e.scratchFrame8Fix, frameLength8kHz)
		var st [2]int32
		outLen := resamplerDown2(&st, frame8Fix, frameFix)
		frame8Fix = frame8Fix[:outLen]
	case 12:
		frame8Fix = ensureInt16Slice(&e.scratchFrame8Fix, frameLength8kHz)
		var st [6]int32
		scratch := ensureInt32Slice(&e.scratchResampler, frameLength+4)
		outLen := resamplerDown2_3(&st, frame8Fix, frameFix, scratch)
		frame8Fix = frame8Fix[:outLen]
	case 8:
		frame8Fix = frameFix
	default:
		frame8Fix = frameFix
	}

	frame8kHz := ensureFloat32Slice(&e.scratchFrame8kHz, len(frame8Fix))
	int16ToFloat32Slice(frame8kHz, frame8Fix)
	if len(frame8kHz) > frameLength8kHz {
		frame8kHz = frame8kHz[:frameLength8kHz]
	}
	// Match libopus pitch_analysis_core_FLP.c: at Fs=8 kHz the stage-2 search
	// uses the original float input "frame" (not the int16-roundtrip frame_8kHz).
	// This avoids extra quantization in NB mode.
	stage2Frame := frame8kHz
	if fsKHz == 8 {
		stage2Len := min(frameLength8kHz, len(pcm))
		if stage2Len > 0 {
			stage2Frame = pcm[:stage2Len]
		} else {
			stage2Frame = nil
		}
	}

	// Decimate to 4kHz using down2
	frame4Fix := ensureInt16Slice(&e.scratchFrame4Fix, len(frame8Fix)/2)
	var st4 [2]int32
	outLen4 := resamplerDown2(&st4, frame4Fix, frame8Fix)
	frame4Fix = frame4Fix[:outLen4]

	// Apply simple low-pass filter to 4kHz signal (matching libopus silk_ADD_SAT16 on float signal filled from FIX)
	for i := len(frame4Fix) - 1; i > 0; i-- {
		frame4Fix[i] = silkSAT16(int32(frame4Fix[i]) + int32(frame4Fix[i-1]))
	}

	frame4kHz := ensureFloat32Slice(&e.scratchFrame4kHz, len(frame4Fix))
	int16ToFloat32Slice(frame4kHz, frame4Fix)
	if len(frame4kHz) > frameLength4kHz {
		frame4kHz = frame4kHz[:frameLength4kHz]
	}

	// Stage 1: Coarse search at 4kHz using scratch buffer
	C := ensureFloat32Slice(&e.scratchPitchC, maxLag4kHz+5)
	for i := range C {
		C[i] = 0 // Clear
	}

	targetStart := sfLength4kHz * 4 // Start after LTP memory
	if targetStart >= len(frame4kHz) {
		targetStart = len(frame4kHz) / 2
	}

	// Compute normalized autocorrelation at 4kHz. In
	// silk/float/pitch_analysis_core_FLP.c, cross_corr, normalizer, energy,
	// and energy_tmp are C double; C/CC outputs and thresholds are silk_float.
	// Process pairs of subframes for stage 1
	for k := 0; k < numSubframes/2; k++ {
		targetIdx := targetStart + k*sfLength8kHz
		if targetIdx+sfLength8kHz > len(frame4kHz) {
			break
		}
		target := frame4kHz[targetIdx : targetIdx+sfLength8kHz]

		// Compute energy of target (double, matches silk_energy_FLP)
		targetEnergy := energyFLP(target)

		// Search all lags with recursive energy update
		basisIdx := max(targetIdx-minLag4kHz, 0)

		// Initial energy at minimum lag
		basisEnd := min(basisIdx+sfLength8kHz, len(frame4kHz))
		basisEnergy := energyFLP(frame4kHz[basisIdx:basisEnd])

		// Compute normalizer with bias scaled to float input.
		normBias := silkCReal(sfLength8kHz) * 4000.0
		normalizer := targetEnergy + basisEnergy + normBias

		// Compute cross-correlation vector (matches celt_pitch_xcorr order)
		xcorrLen := maxLag4kHz - minLag4kHz + 1
		xcorr := ensureFloat32Slice(&e.scratchPitchXcorr, xcorrLen)
		xcorr = xcorr[:xcorrLen]
		xcorrStart := max(targetIdx-maxLag4kHz, 0)
		celtPitchXcorrFloat(target, frame4kHz[xcorrStart:], xcorr, sfLength8kHz, xcorrLen)
		var xcorrVal float32
		if maxLag4kHz-minLag4kHz < len(xcorr) {
			xcorrVal = xcorr[maxLag4kHz-minLag4kHz]
		}
		if minLag4kHz < len(C) {
			C[minLag4kHz] += float32(2.0 * silkCReal(xcorrVal) / normalizer)
		}

		// Recursive update for remaining lags
		for d := minLag4kHz + 1; d <= maxLag4kHz; d++ {
			basisIdx--
			if basisIdx < 0 {
				break
			}

			// Fetch cross-correlation for this lag
			xcorrVal = 0
			idx := maxLag4kHz - d
			if idx >= 0 && idx < len(xcorr) {
				xcorrVal = xcorr[idx]
			}

			// Update energy recursively
			if basisIdx >= 0 && basisIdx+sfLength8kHz < len(frame4kHz) {
				vNew := frame4kHz[basisIdx]
				vOld := frame4kHz[basisIdx+sfLength8kHz]
				basisEnergy += silkCReal(vNew)*silkCReal(vNew) - silkCReal(vOld)*silkCReal(vOld)
			}
			normalizer = targetEnergy + basisEnergy + normBias

			if d < len(C) {
				C[d] += float32(2.0 * silkCReal(xcorrVal) / normalizer)
			}
		}
	}

	// Apply short-lag bias (matching libopus)
	for i := maxLag4kHz; i >= minLag4kHz; i-- {
		if i < len(C) {
			C[i] -= C[i] * float32(i) / 4096.0
		}
	}

	// Find top candidates using insertion sort (match libopus in-place behavior)
	complexity := int(e.pitchEstimationComplexity)
	if complexity < 0 {
		complexity = 0
	} else if complexity > 2 {
		complexity = 2
	}
	lengthDSrch := min(4+2*complexity, peDSrchLength)

	dSrch := ensureInt32Slice(&e.scratchDSrch, peDSrchLength)
	for i := 0; i < lengthDSrch; i++ {
		dSrch[i] = int32(i)
	}
	if minLag4kHz < 0 {
		minLag4kHz = 0
	}
	if maxLag4kHz >= len(C) {
		maxLag4kHz = len(C) - 1
	}
	if maxLag4kHz < minLag4kHz {
		maxLag4kHz = minLag4kHz
	}
	cSlice := C[minLag4kHz : maxLag4kHz+1]
	if lengthDSrch > len(cSlice) {
		lengthDSrch = len(cSlice)
	}
	// In-place insertion sort for first K candidates (libopus silk_insertion_sort_decreasing_FLP).
	for i := 1; i < lengthDSrch; i++ {
		value := cSlice[i]
		j := i - 1
		for j >= 0 && value > cSlice[j] {
			cSlice[j+1] = cSlice[j]
			dSrch[j+1] = dSrch[j]
			j--
		}
		cSlice[j+1] = value
		dSrch[j+1] = int32(i)
	}
	for i := lengthDSrch; i < len(cSlice); i++ {
		value := cSlice[i]
		if value > cSlice[lengthDSrch-1] {
			j := lengthDSrch - 2
			for j >= 0 && value > cSlice[j] {
				cSlice[j+1] = cSlice[j]
				dSrch[j+1] = dSrch[j]
				j--
			}
			cSlice[j+1] = value
			dSrch[j+1] = int32(i)
		}
	}

	// Check if correlation is too low
	Cmax := cSlice[0]
	if Cmax < 0.2 {
		// Unvoiced - libopus returns zero lags in this case.
		pitchLags := ensureInt32Slice(&e.scratchPitchLags, numSubframes)
		for i := range pitchLags {
			pitchLags[i] = 0
		}
		e.pitchState.ltpCorr = 0
		e.pitchState.prevLag = 0
		return pitchLags, 0, 0
	}

	// Threshold for candidate selection
	if searchThres1 <= 0 {
		searchThres1 = 0.8 - 0.5*float32(complexity)/2.0
	}
	threshold := searchThres1 * Cmax

	// Convert to 8kHz indices and expand search range
	dCompLen := peMaxLagMS*8 + 5
	dComp := ensureInt16Slice(&e.scratchDComp, dCompLen)
	for i := range dComp {
		dComp[i] = 0 // Clear
	}
	for i := 0; i < lengthDSrch; i++ {
		if cSlice[i] <= threshold {
			break
		}
		d := (int(dSrch[i]) + minLag4kHz) * 2
		dSrch[i] = int32(d)
		if d >= minLag8kHz && d <= maxLag8kHz {
			dComp[d] = 1
		}
	}

	// Convolution to expand search range (stage 2 d_srch list)
	for i := maxLag8kHz + 3; i >= minLag8kHz; i-- {
		if i < len(dComp) && i-1 >= 0 && i-2 >= 0 {
			dComp[i] += dComp[i-1] + dComp[i-2]
		}
	}

	// Collect expanded search lags for codebook search
	lengthDSrch = 0
	for i := minLag8kHz; i <= maxLag8kHz; i++ {
		if i+1 < len(dComp) && dComp[i+1] > 0 {
			if lengthDSrch < len(dSrch) {
				dSrch[lengthDSrch] = int32(i)
				lengthDSrch++
			}
		}
	}
	// Second convolution to build d_comp lag list (libopus stage 2 precompute gating)
	for i := maxLag8kHz + 3; i >= minLag8kHz; i-- {
		if i < len(dComp) && i-1 >= 0 && i-2 >= 0 && i-3 >= 0 {
			dComp[i] += dComp[i-1] + dComp[i-2] + dComp[i-3]
		}
	}
	// Build length_d_comp list from second convolution.
	// This mirrors libopus pitch_analysis_core_FLP.c and stores lag-2 values.
	lengthDComp := 0
	var dCompLags [peMaxLagMS*16 + 8]int16
	for i := minLag8kHz; i < maxLag8kHz+4; i++ {
		if i < len(dComp) && dComp[i] > 0 {
			dCompLags[lengthDComp] = int16(i - 2)
			lengthDComp++
		}
	}

	// Pre-compute C[k][d] correlations for Stage-2 lag/codebook search.
	const cArrayDim = (peMaxLagMS*16)/2 + 5 // (PE_MAX_LAG >> 1) + 5
	var corrC [peMaxNbSubfr][cArrayDim]float32

	targetStart8k := peLTPMemLengthMS * 8
	targetIdx := targetStart8k
	for k := range numSubframes {
		if targetIdx+sfLength8kHz > len(stage2Frame) {
			break
		}
		target := stage2Frame[targetIdx : targetIdx+sfLength8kHz]
		energyTmp := energyFLP(target) + 1.0
		for j := 0; j < lengthDComp; j++ {
			d := int(dCompLags[j])
			basisIdx := targetIdx - d
			if basisIdx >= 0 && basisIdx+sfLength8kHz <= len(stage2Frame) {
				basis := stage2Frame[basisIdx : basisIdx+sfLength8kHz]
				xcorr := innerProductFLP(basis, target, sfLength8kHz)
				if xcorr > 0 {
					energy := energyFLP(basis)
					if d >= 0 && d < cArrayDim {
						corrC[k][d] = float32(2.0 * xcorr / (energy + energyTmp))
					}
				}
			}
		}
		targetIdx += sfLength8kHz
	}

	// Search over lag range and contour codebook
	CCmaxStage2 := float32(-1000.0)
	CCmaxBStage2 := float32(-1000.0)
	CBimax := 0
	lag := -1

	// Previous lag handling
	prevLag := int(e.pitchState.prevLag)
	var prevLagLog2 float32
	if prevLag > 0 {
		prevLag8k := prevLag
		if fsKHz == 12 {
			prevLag8k = prevLag * 2 / 3
		} else if fsKHz == 16 {
			prevLag8k = prevLag / 2
		}
		prevLagLog2 = silkLog2Float(float32(prevLag8k))
	}

	// Determine number of codebook searches
	var cbkSize, nbCbkSearch int
	var lagCBPtr *[peMaxNbSubfr][peNbCbksStage2Ext]int8
	var lagCBPtr10ms *[2][peNbCbksStage210ms]int8
	if numSubframes == peMaxNbSubfr {
		cbkSize = peNbCbksStage2Ext
		lagCBPtr = &pitchCBLagsStage2
		if fsKHz == 8 && complexity > 0 {
			nbCbkSearch = peNbCbksStage2Ext
		} else {
			nbCbkSearch = peNbCbksStage2
		}
	} else {
		cbkSize = peNbCbksStage210ms
		lagCBPtr10ms = &pitchCBLagsStage210ms
		nbCbkSearch = peNbCbksStage210ms
	}

	if searchThres2 <= 0 {
		searchThres2 = 0.4 - 0.25*float32(complexity)/2.0
	}

	var ccBuf [peNbCbksStage2Ext]float32
	for k := 0; k < lengthDSrch; k++ {
		d := int(dSrch[k])

		cc := ccBuf[:nbCbkSearch]
		for j := 0; j < nbCbkSearch; j++ {
			cc[j] = 0
			for i := range numSubframes {
				var cbOffset int8
				if lagCBPtr != nil && i < peMaxNbSubfr && j < cbkSize {
					cbOffset = lagCBPtr[i][j]
				} else if lagCBPtr10ms != nil && i < 2 && j < cbkSize {
					cbOffset = lagCBPtr10ms[i][j]
				}
				lagIdx := d + int(cbOffset)
				if lagIdx >= 0 && lagIdx < cArrayDim && i < peMaxNbSubfr {
					cc[j] += corrC[i][lagIdx]
				}
			}
		}

		CCmaxNew := float32(-1000.0)
		CBimaxNew := 0
		for i := 0; i < nbCbkSearch; i++ {
			if cc[i] > CCmaxNew {
				CCmaxNew = cc[i]
				CBimaxNew = i
			}
		}

		lagLog2 := silkLog2Float(float32(d))
		CCmaxNewB := CCmaxNew - float32(peShortlagBias)*float32(numSubframes)*lagLog2
		if prevLag > 0 {
			deltaLagLog2Sqr := lagLog2 - prevLagLog2
			deltaLagLog2Sqr *= deltaLagLog2Sqr
			CCmaxNewB -= float32(pePrevlagBias) * float32(numSubframes) * e.pitchState.ltpCorr *
				deltaLagLog2Sqr / (deltaLagLog2Sqr + 0.5)
		}
		if CCmaxNewB > CCmaxBStage2 && CCmaxNew > float32(numSubframes)*searchThres2 {
			CCmaxBStage2 = CCmaxNewB
			CCmaxStage2 = CCmaxNew
			lag = d
			CBimax = CBimaxNew
		}
	}

	if lag == -1 {
		pitchLags := ensureInt32Slice(&e.scratchPitchLags, numSubframes)
		for i := range pitchLags {
			pitchLags[i] = 0
		}
		e.pitchState.ltpCorr = 0
		e.pitchState.prevLag = 0
		return pitchLags, 0, 0
	}

	// Update LTP correlation
	if lag > 0 {
		e.pitchState.ltpCorr = CCmaxStage2 / float32(numSubframes)
	}
	// Stage 3: Fine search at full rate (if not 8kHz) - use scratch buffer
	pitchLags := ensureInt32Slice(&e.scratchPitchLags, numSubframes)

	if fsKHz > 8 {
		if fsKHz == 12 {
			lag = (lag*3 + 1) / 2
		} else if fsKHz == 16 {
			lag *= 2
		}

		if lag < minLag {
			lag = minLag
		}
		if lag > maxLag {
			lag = maxLag
		}

		startLag := max(lag-2, minLag)
		endLag := min(lag+2, maxLag)

		corrSt3 := ensureFloat32Slice(&e.scratchPitchCorrSt3, numSubframes*peNbCbksStage3Max*peNbStage3Lags)
		energySt3 := ensureFloat32Slice(&e.scratchPitchEnergySt3, numSubframes*peNbCbksStage3Max*peNbStage3Lags)
		pitchAnalysisCalcCorrSt3(corrSt3, pcm, startLag, sfLength, numSubframes, complexity)
		pitchAnalysisCalcEnergySt3(energySt3, pcm, startLag, sfLength, numSubframes, complexity)

		lagCounter := 0
		contourBias := float32(peFlatcontourBias) / float32(lag)

		var lagCBStage3 [][]int8
		var nbCbkSearch3 int
		if numSubframes == peMaxNbSubfr {
			nbCbkSearch3 = pitchNbCbkSearchsStage3[complexity]
			lagCBStage3 = pitchCBLagsStage3Slice
		} else {
			nbCbkSearch3 = peNbCbksStage310ms
			lagCBStage3 = pitchCBLagsStage310msSlice
		}

		targetStartFull := peLTPMemLengthMS * fsKHz
		targetEnd := min(targetStartFull+numSubframes*sfLength, len(pcm))
		energyTmp := energyFLP(pcm[targetStartFull:targetEnd]) + 1.0

		lagNew := lag
		CBimax = 0
		CCmaxSt3 := float32(-1000.0)

		// libopus always uses silk_CB_lags_stage3[0] for the max_lag limit
		// check, even for 10ms frames (see pitch_analysis_core_FLP.c line 450).
		cbOffsetCheck := pitchCBLagsStage3Slice[0]

		for d := startLag; d <= endLag; d++ {
			for j := 0; j < nbCbkSearch3; j++ {
				crossCorr := silkCReal(0)
				energy := energyTmp
				for k := range numSubframes {
					idx := pitchStage3Index(k, j, lagCounter)
					crossCorr += silkCReal(corrSt3[idx])
					energy += silkCReal(energySt3[idx])
				}
				cc := float32(0)
				if crossCorr > 0 {
					cc = float32(2.0 * crossCorr / energy)
					cc *= 1.0 - contourBias*float32(j)
				}

				if cc > CCmaxSt3 {
					limitOffset := 0
					if j < len(cbOffsetCheck) {
						limitOffset = int(cbOffsetCheck[j])
					}
					if d+limitOffset <= maxLag {
						CCmaxSt3 = cc
						lagNew = d
						CBimax = j
					}
				}
			}
			lagCounter++
		}
		for k := range numSubframes {
			cbOffset := lagCBStage3[k][CBimax]
			pitchLag := min(max(lagNew+int(cbOffset), minLag), maxPitchLag)
			pitchLags[k] = int32(pitchLag)
		}
		lag = lagNew
	} else {
		// 8kHz: use stage 2 results directly
		for k := range numSubframes {
			var cbOffset int8
			if lagCBPtr != nil && k < peMaxNbSubfr && CBimax < cbkSize {
				cbOffset = lagCBPtr[k][CBimax]
			} else if lagCBPtr10ms != nil && k < 2 && CBimax < cbkSize {
				cbOffset = lagCBPtr10ms[k][CBimax]
			}
			pitchLag := min(max(lag+int(cbOffset), minLag8kHz), maxPitchLag8kHz)
			pitchLags[k] = int32(pitchLag)
		}
	}

	// Update state for next frame
	if len(pitchLags) > 0 {
		e.pitchState.prevLag = pitchLags[len(pitchLags)-1]
	} else {
		e.pitchState.prevLag = 0
	}

	return pitchLags, lag - minLag, CBimax
}

func silkLog2Float(x float32) float32 {
	if x <= 0 {
		return 0
	}
	return silkLog2F32(x)
}

// downsampleLowpass performs downsampling with a simple low-pass filter.
// This matches libopus's approach of using a 3-tap filter for anti-aliasing.
func downsampleLowpass(signal []float32, factor int) []float32 {
	if factor <= 1 {
		return signal
	}

	n := len(signal) / factor
	ds := make([]float32, n)
	downsampleLowpassToBuffer(signal, factor, ds)
	return ds
}

// downsampleLowpassToBuffer performs the actual downsampling into a provided buffer.
func downsampleLowpassToBuffer(signal []float32, factor int, ds []float32) {
	n := len(ds)
	// 3-tap low-pass filter: [0.25, 0.5, 0.25]
	offset := factor / 2
	for i := range n {
		idx := i * factor
		if i == 0 {
			// First sample: use available samples
			ds[i] = 0.5*signal[idx] + 0.5*signal[idx+offset]
		} else if idx+offset < len(signal) && idx-offset >= 0 {
			ds[i] = 0.25*signal[idx-offset] + 0.5*signal[idx] + 0.25*signal[idx+offset]
		} else {
			ds[i] = signal[idx]
		}
	}
}

// downsample reduces sample rate by averaging factor samples.
// Kept for backward compatibility with existing tests.
func downsample(signal []float32, factor int) []float32 {
	if factor <= 1 {
		return signal
	}

	n := len(signal) / factor
	ds := make([]float32, n)

	for i := range n {
		var sum float32
		for j := range factor {
			sum += signal[i*factor+j]
		}
		ds[i] = sum / float32(factor)
	}

	return ds
}

// autocorrPitchSearch finds best pitch lag using silk_float-width normalized autocorrelation.
// Uses bias toward shorter lags to avoid octave errors.
// Kept for backward compatibility with existing tests.
func autocorrPitchSearch(signal []float32, minLag, maxLag int) int {
	n := len(signal)
	if maxLag >= n {
		maxLag = n - 1
	}
	if minLag < 1 {
		minLag = 1
	}
	if minLag > maxLag {
		return minLag
	}

	bestLag := minLag
	bestCorr := float32(-1)

	for lag := minLag; lag <= maxLag; lag++ {
		var corr, energy1, energy2 float32
		for i := lag; i < n; i++ {
			corr += noFMA32(signal[i], signal[i-lag])
			energy1 += noFMA32(signal[i], signal[i])
			energy2 += noFMA32(signal[i-lag], signal[i-lag])
		}

		if energy1 < 1e-10 || energy2 < 1e-10 {
			continue
		}

		// Normalized correlation
		normCorr := corr / sqrt32(energy1*energy2)

		// Bias toward shorter lags to avoid octave errors
		// Per draft-vos-silk-01 Section 2.1.2.5
		normCorr *= 1.0 - 0.001*float32(lag-minLag)

		if normCorr > bestCorr {
			bestCorr = normCorr
			bestLag = lag
		}
	}

	return bestLag
}

// lagrangianInterpolate performs Lagrangian interpolation for sub-sample pitch refinement.
// Given correlation values at lags [d-1, d, d+1], returns fractional offset.
// This matches libopus's approach in remove_doubling/pitch_search.
func lagrangianInterpolate(corrMinus, corrCenter, corrPlus float32) float32 {
	// Quadratic interpolation to find sub-sample offset
	// offset = 0.5 * (corrMinus - corrPlus) / (corrMinus - 2*corrCenter + corrPlus)
	denom := corrMinus - 2*corrCenter + corrPlus
	if abs32(denom) < 1e-10 {
		return 0
	}
	offset := 0.5 * (corrMinus - corrPlus) / denom

	// Clamp to [-0.5, 0.5]
	if offset < -0.5 {
		offset = -0.5
	}
	if offset > 0.5 {
		offset = 0.5
	}
	return offset
}

// energyFLP computes sum of squares of a float32 array.
// Matches libopus silk/float/energy_FLP.c: silk_energy_FLP returns C double.
func energyFLP(data []float32) silkCReal {
	return energyF32Libopus(data, len(data))
}

// innerProductFLP is in inner_product_flp.go.

func pitchStage3Index(k, j, lag int) int {
	return (k*peNbCbksStage3Max+j)*peNbStage3Lags + lag
}

func pitchAnalysisCalcCorrSt3(out []float32, frame []float32, startLag, sfLength, nbSubfr, complexity int) {
	if nbSubfr <= 0 || sfLength <= 0 {
		return
	}

	var lagRange [][2]int8
	var lagCB [][]int8
	var nbCbkSearch int
	if nbSubfr == peMaxNbSubfr {
		lagRange = pitchLagRangeStage3[complexity][:]
		lagCB = pitchCBLagsStage3Slice
		nbCbkSearch = pitchNbCbkSearchsStage3[complexity]
	} else {
		lagRange = pitchLagRangeStage310ms[:]
		lagCB = pitchCBLagsStage310msSlice
		nbCbkSearch = peNbCbksStage310ms
	}

	targetIdx := sfLength * 4
	var scratchMem [22]float32
	for k := range nbSubfr {
		lagLow := int(lagRange[k][0])
		lagHigh := int(lagRange[k][1])
		if lagHigh < lagLow {
			targetIdx += sfLength
			continue
		}
		lagCount := min(lagHigh-lagLow+1, len(scratchMem))
		for i := range lagCount {
			scratchMem[i] = 0
		}
		for j := lagLow; j <= lagHigh && (j-lagLow) < lagCount; j++ {
			basisIdx := targetIdx - startLag - j
			if basisIdx < 0 || basisIdx+sfLength > len(frame) || targetIdx+sfLength > len(frame) {
				continue
			}
			scratchMem[j-lagLow] = float32(innerProductFLP(frame[basisIdx:], frame[targetIdx:], sfLength))
		}
		delta := lagLow
		for i := 0; i < nbCbkSearch; i++ {
			idx := int(lagCB[k][i]) - delta
			for j := range peNbStage3Lags {
				if idx+j < 0 || idx+j >= lagCount {
					out[pitchStage3Index(k, i, j)] = 0
					continue
				}
				out[pitchStage3Index(k, i, j)] = scratchMem[idx+j]
			}
		}
		targetIdx += sfLength
	}
}

func pitchAnalysisCalcEnergySt3(out []float32, frame []float32, startLag, sfLength, nbSubfr, complexity int) {
	if nbSubfr <= 0 || sfLength <= 0 {
		return
	}

	var lagRange [][2]int8
	var lagCB [][]int8
	var nbCbkSearch int
	if nbSubfr == peMaxNbSubfr {
		lagRange = pitchLagRangeStage3[complexity][:]
		lagCB = pitchCBLagsStage3Slice
		nbCbkSearch = pitchNbCbkSearchsStage3[complexity]
	} else {
		lagRange = pitchLagRangeStage310ms[:]
		lagCB = pitchCBLagsStage310msSlice
		nbCbkSearch = peNbCbksStage310ms
	}

	targetIdx := sfLength * 4
	var scratchMem [22]float32
	for k := range nbSubfr {
		lagLow := int(lagRange[k][0])
		lagHigh := int(lagRange[k][1])
		if lagHigh < lagLow {
			targetIdx += sfLength
			continue
		}
		lagCount := min(lagHigh-lagLow+1, len(scratchMem))

		basisStart := targetIdx - (startLag + lagLow)
		if basisStart < 0 || basisStart+sfLength > len(frame) {
			targetIdx += sfLength
			continue
		}
		energy := energyFLP(frame[basisStart:basisStart+sfLength]) + 1e-3
		scratchMem[0] = float32(energy)
		for i := 1; i < lagCount; i++ {
			energy -= silkCReal(frame[basisStart+sfLength-i]) * silkCReal(frame[basisStart+sfLength-i])
			energy += silkCReal(frame[basisStart-i]) * silkCReal(frame[basisStart-i])
			scratchMem[i] = float32(energy)
		}

		delta := lagLow
		for i := 0; i < nbCbkSearch; i++ {
			idx := int(lagCB[k][i]) - delta
			for j := range peNbStage3Lags {
				if idx+j < 0 || idx+j >= lagCount {
					out[pitchStage3Index(k, i, j)] = 0
					continue
				}
				out[pitchStage3Index(k, i, j)] = scratchMem[idx+j]
			}
		}
		targetIdx += sfLength
	}
}

func (e *Encoder) preparePitchLags(pitchLags []int32, numSubframes, lagIndex, contourIndex int) pitchEncodeParams {
	config := GetBandwidthConfig(e.bandwidth)
	fsKHz := config.SampleRate / 1000

	_, contourICDF, lagLowICDF := pitchLagTables(fsKHz, numSubframes)

	// Clamp indices for safety
	if lagIndex < 0 {
		lagIndex = 0
	}
	if lagIndex > config.PitchLagMax-config.PitchLagMin {
		lagIndex = config.PitchLagMax - config.PitchLagMin
	}

	return pitchEncodeParams{
		contourIdx:  contourIndex,
		lagIdx:      lagIndex,
		contourICDF: contourICDF,
		lagLowICDF:  lagLowICDF,
	}
}

func pitchLagTables(fsKHz, numSubframes int) ([][]int8, []uint8, []uint8) {
	use10ms := numSubframes != maxNbSubfr
	var contourTable [][]int8
	var contourICDF []uint8

	if fsKHz == 8 {
		if use10ms {
			contourTable = pitchCBLagsStage210msSlice
			contourICDF = silk_pitch_contour_10_ms_NB_iCDF
		} else {
			contourTable = pitchCBLagsStage2Slice
			contourICDF = silk_pitch_contour_NB_iCDF
		}
	} else {
		if use10ms {
			contourTable = pitchCBLagsStage310msSlice
			contourICDF = silk_pitch_contour_10_ms_iCDF
		} else {
			contourTable = pitchCBLagsStage3Slice
			contourICDF = silk_pitch_contour_iCDF
		}
	}

	var lagLowICDF []uint8
	switch fsKHz {
	case 16:
		lagLowICDF = silk_uniform8_iCDF
	case 12:
		lagLowICDF = silk_uniform6_iCDF
	default:
		lagLowICDF = silk_uniform4_iCDF
	}

	return contourTable, contourICDF, lagLowICDF
}

func (e *Encoder) encodePitchLagsWithParams(params pitchEncodeParams, condCoding int) {
	config := GetBandwidthConfig(e.bandwidth)
	fsKHz := config.SampleRate / 1000

	encodeAbsolute := true
	if condCoding == codeConditionally && e.ecPrevSignalType == typeVoiced {
		delta := params.lagIdx - int(e.ecPrevLagIndex)
		if delta < -8 || delta > 11 {
			delta = 0
		} else {
			delta += 9
			encodeAbsolute = false
		}
		e.rangeEncoder.EncodeICDF(delta, silk_pitch_delta_iCDF, 8)
	}

	if encodeAbsolute {
		divisor := max(
			// 8 for 16kHz, 6 for 12kHz, 4 for 8kHz
			fsKHz/2, 1)
		lagHigh := params.lagIdx / divisor
		lagLow := params.lagIdx - lagHigh*divisor
		if lagHigh > 31 {
			lagHigh = 31
		}

		if lagLow > len(params.lagLowICDF)-1 {
			lagLow = len(params.lagLowICDF) - 1
		}

		e.rangeEncoder.EncodeICDF(lagHigh, silk_pitch_lag_iCDF, 8)
		e.rangeEncoder.EncodeICDF(lagLow, params.lagLowICDF, 8)
	}

	if params.contourIdx < 0 {
		params.contourIdx = 0
	}
	if params.contourIdx > len(params.contourICDF)-1 {
		params.contourIdx = len(params.contourICDF) - 1
	}
	e.rangeEncoder.EncodeICDF(params.contourIdx, params.contourICDF, 8)
	e.ecPrevLagIndex = int16(params.lagIdx)
}

// findBestPitchContour finds the contour that best matches the pitch lag pattern.
// Returns contour index and base lag.
func findBestPitchContour(pitchLags []int32, numSubframes int, minLag, maxLag int, contours [][]int8, cbkSize int) (int, int) {
	bestContour := 0
	bestBase := minLag
	bestDist := math.MaxInt32

	for cIdx := range cbkSize {
		var sumLag int
		for sf := 0; sf < numSubframes && sf < len(pitchLags); sf++ {
			sumLag += int(pitchLags[sf]) - int(contours[sf][cIdx])
		}
		baseLag := min(max(sumLag/numSubframes, minLag), maxLag)

		var dist int
		for sf := 0; sf < numSubframes && sf < len(pitchLags); sf++ {
			predicted := baseLag + int(contours[sf][cIdx])
			diff := int(pitchLags[sf]) - predicted
			dist += diff * diff
		}
		if dist < bestDist {
			bestDist = dist
			bestContour = cIdx
			bestBase = baseLag
		}
	}

	return bestContour, bestBase
}

// pitchMax returns the larger of a and b.
func pitchMax(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// pitchMin returns the smaller of a and b.
func pitchMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}
