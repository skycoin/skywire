package lpcnetplc

import (
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/opusmath"
)

const (
	analysisLPCOrder     = 16
	analysisPreemphasis  = 0.85
	analysisOverlapSize  = 160
	analysisTrainingOff  = 80
	analysisWindow5ms    = 4
	analysisWindowSize   = FrameSize + analysisOverlapSize
	analysisFreqSize     = analysisWindowSize/2 + 1
	analysisPitchBufSize = PitchMaxPeriod + 2*FrameSize
)

// libopus DRED reference builds are scalar, with asm/rtcd/intrinsics disabled.
var useNEONAnalysisKernels = false

type analysisScratch struct {
	frame       [FrameSize]float32
	window      [analysisWindowSize]float32
	alignedIn   [FrameSize]float32
	spectrum    [analysisFreqSize]complex64
	fftIn       [analysisWindowSize]complex64
	fftOut      [analysisWindowSize]complex64
	fftScratch  [analysisWindowSize]celt.KissCpx
	bandEnergy  [NumBands]float32
	logBands    [NumBands]float32
	pitchXCorr  [pitchXcorrFeatures]float32
	pitchEner   [pitchXcorrFeatures]float32
	lpcInput    [FrameSize + analysisLPCOrder]float32
	lpcEx       [NumBands]float32
	lpcTmp      [NumBands]float32
	lpcInterp   [analysisFreqSize]float32
	inverseSpec [analysisFreqSize]complex64
	inverseReal [analysisWindowSize]float32
	burgIn      [FrameSize]float32
	burgBands   [NumBands]float32
	burgLog     [NumBands]float32
	burgTemp    [2 * NumBands]float32
	// libopus dnn/burg.c:silk_burg_analysis uses C double for Burg work arrays.
	burgAF    [analysisLPCOrder]opusmath.CReal
	burgFirst [analysisLPCOrder]opusmath.CReal
	burgLast  [analysisLPCOrder]opusmath.CReal
	burgCAF   [analysisLPCOrder + 1]opusmath.CReal
	burgCAB   [analysisLPCOrder + 1]opusmath.CReal
}

// Analysis mirrors the retained libopus LPCNet encoder-analysis state used by
// the PLC/DRED concealment front end.
type Analysis struct {
	pitch         PitchDNN
	analysisMem   [analysisOverlapSize]float32
	memPreemph    float32
	prevIF        [pitchIFMaxFreq]complex64
	ifFeatures    [pitchIFFeatures]float32
	xcorrFeatures [pitchXcorrFeatures]float32
	dnnPitch      float32
	pitchMem      [analysisLPCOrder]float32
	pitchFilt     float32
	excBuf        [analysisPitchBufSize]float32
	lpBuf         [analysisPitchBufSize]float32
	lpMem         [4]float32
	lpc           [analysisLPCOrder]float32
	features      [NumTotalFeatures]float32
	scratch       analysisScratch
	dredEncoder   bool
}

// SetModel binds the shared pitch model family used by libopus LPCNet
// analysis. The retained analysis state is reset after a successful bind.
func (a *Analysis) SetModel(blob *dnnblob.Blob) error {
	if err := a.pitch.SetModel(blob); err != nil {
		a.Reset()
		return err
	}
	a.Reset()
	return nil
}

// SetModelPreservingState replaces the pitch model without clearing retained
// analysis state.
func (a *Analysis) SetModelPreservingState(blob *dnnblob.Blob) error {
	if err := a.pitch.SetModelPreservingState(blob); err != nil {
		return err
	}
	return nil
}

// Loaded reports whether the analysis runtime currently has the pitch model
// family required by libopus.
func (a *Analysis) Loaded() bool {
	return a != nil && a.pitch.Loaded()
}

// SetDREDEncoderMode selects the libopus DRED encoder-side band-energy
// accumulation shape used before RDOVAE latent extraction.
func (a *Analysis) SetDREDEncoderMode(enabled bool) {
	if a == nil {
		return
	}
	a.dredEncoder = enabled
}

// Reset clears the retained analysis state but preserves the bound model.
func (a *Analysis) Reset() {
	if a == nil {
		return
	}
	a.analysisMem = [analysisOverlapSize]float32{}
	a.memPreemph = 0
	a.prevIF = [pitchIFMaxFreq]complex64{}
	a.ifFeatures = [pitchIFFeatures]float32{}
	a.xcorrFeatures = [pitchXcorrFeatures]float32{}
	a.dnnPitch = 0
	a.pitchMem = [analysisLPCOrder]float32{}
	a.pitchFilt = 0
	a.excBuf = [analysisPitchBufSize]float32{}
	a.lpBuf = [analysisPitchBufSize]float32{}
	a.lpMem = [4]float32{}
	a.lpc = [analysisLPCOrder]float32{}
	a.features = [NumTotalFeatures]float32{}
	a.pitch.Reset()
}

// ComputeSingleFrameFeaturesFloat mirrors libopus
// lpcnet_compute_single_frame_features_float(). It retains encoder-analysis
// state across calls and writes one 36-float feature vector into dst.
func (a *Analysis) ComputeSingleFrameFeaturesFloat(dst, pcm []float32) int {
	if a == nil || !a.Loaded() || len(dst) < NumTotalFeatures || len(pcm) < FrameSize {
		return 0
	}
	copy(a.scratch.frame[:], pcm[:FrameSize])
	preemphasisInPlace(a.scratch.frame[:], &a.memPreemph, analysisPreemphasis)
	a.computeFrameFeatures(a.scratch.frame[:])
	copy(dst[:NumTotalFeatures], a.features[:])
	return NumTotalFeatures
}

// BurgCepstralAnalysis mirrors libopus burg_cepstral_analysis(). It writes the
// 36-float Burg cepstrum used by the first-loss PLC analysis loop.
func (a *Analysis) BurgCepstralAnalysis(dst, pcm []float32) int {
	if a == nil || len(dst) < 2*NumBands || len(pcm) < FrameSize {
		return 0
	}
	a.computeBurgCepstrum(dst[:NumBands], pcm[:FrameSize/2], FrameSize/2, analysisLPCOrder)
	a.computeBurgCepstrum(dst[NumBands:2*NumBands], pcm[FrameSize/2:FrameSize], FrameSize/2, analysisLPCOrder)
	for i := range NumBands {
		c0 := dst[i]
		c1 := dst[NumBands+i]
		dst[i] = .5 * (c0 + c1)
		dst[NumBands+i] = c0 - c1
	}
	return 2 * NumBands
}

// PrimeHistoryFramesFloat replays a run of normalized 10 ms PCM frames through
// the retained analysis frontend so first-loss CELT entry can start from the
// same recent 16 kHz history window used for PLC state priming.
func (a *Analysis) PrimeHistoryFramesFloat(history []float32) int {
	if a == nil || !a.Loaded() || len(history) < FrameSize {
		return 0
	}
	var features [NumTotalFeatures]float32
	total := 0
	for offset := 0; offset+FrameSize <= len(history); offset += FrameSize {
		frame := history[offset : offset+FrameSize]
		if n := a.ComputeSingleFrameFeaturesFloat(features[:], frame); n != NumTotalFeatures {
			return total
		}
		total += FrameSize
	}
	return total
}

func (a *Analysis) computeFrameFeatures(in []float32) {
	copy(a.scratch.alignedIn[:analysisTrainingOff], a.analysisMem[analysisOverlapSize-analysisTrainingOff:])
	a.frameAnalysis(a.scratch.spectrum[:], a.scratch.bandEnergy[:], in)

	a.ifFeatures[0] = clampUnit((1.0 / 64.0) * (10*log10f(1e-15+real(a.scratch.spectrum[0])*real(a.scratch.spectrum[0])) - 6))
	for i := 1; i < pitchIFMaxFreq; i++ {
		prod := mulConj(a.scratch.spectrum[i], a.prevIF[i])
		norm := float32(1.0) / opusmath.SqrtF32(1e-15+real(prod)*real(prod)+imag(prod)*imag(prod))
		prod *= complex(norm, 0)
		a.ifFeatures[3*i-2] = real(prod)
		a.ifFeatures[3*i-1] = imag(prod)
		energy := real(a.scratch.spectrum[i])*real(a.scratch.spectrum[i]) + imag(a.scratch.spectrum[i])*imag(a.scratch.spectrum[i])
		a.ifFeatures[3*i] = clampUnit((1.0 / 64.0) * (10*log10f(1e-15+energy) - 6))
	}
	copy(a.prevIF[:], a.scratch.spectrum[:pitchIFMaxFreq])

	logMax := float32(-2)
	follow := float32(-2)
	for i := range NumBands {
		v := log10f(1e-2 + a.scratch.bandEnergy[i])
		v = maxF32(logMax-8, maxF32(follow-2.5, v))
		a.scratch.logBands[i] = v
		logMax = maxF32(logMax, v)
		follow = maxF32(follow-2.5, v)
	}
	dctTransform(a.features[:NumBands], a.scratch.logBands[:])
	a.features[0] -= 4
	lpcFromCepstrum(a.lpc[:], a.features[:NumBands], &a.scratch)
	for i := range analysisLPCOrder {
		a.features[NumBands+2+i] = a.lpc[i]
	}

	copy(a.excBuf[:PitchMaxPeriod], a.excBuf[FrameSize:FrameSize+PitchMaxPeriod])
	copy(a.lpBuf[:PitchMaxPeriod], a.lpBuf[FrameSize:FrameSize+PitchMaxPeriod])
	copy(a.scratch.alignedIn[analysisTrainingOff:], in[:FrameSize-analysisTrainingOff])
	copy(a.scratch.lpcInput[:analysisLPCOrder], a.pitchMem[:])
	copy(a.scratch.lpcInput[analysisLPCOrder:], a.scratch.alignedIn[:])
	copy(a.pitchMem[:], a.scratch.alignedIn[FrameSize-analysisLPCOrder:])
	celtFIRFloat(a.scratch.lpcInput[:], a.lpc[:], a.lpBuf[PitchMaxPeriod:PitchMaxPeriod+FrameSize])
	for i := range FrameSize {
		a.excBuf[PitchMaxPeriod+i] = a.lpBuf[PitchMaxPeriod+i] + 0.7*a.pitchFilt
		a.pitchFilt = a.lpBuf[PitchMaxPeriod+i]
	}
	biquadInPlace(a.lpBuf[PitchMaxPeriod:PitchMaxPeriod+FrameSize], a.lpMem[:2])

	buf := a.excBuf[:]
	pitchXCorrFloat(a.scratch.pitchXCorr[:], buf[PitchMaxPeriod:PitchMaxPeriod+FrameSize], buf[:PitchMaxPeriod+FrameSize], FrameSize, pitchXcorrFeatures)
	ener0 := innerProdFloat(buf[PitchMaxPeriod:PitchMaxPeriod+FrameSize], buf[PitchMaxPeriod:PitchMaxPeriod+FrameSize], FrameSize)
	// libopus dnn/lpcnet_enc.c:compute_frame_features stores ener1 as C double.
	ener1 := opusmath.CReal(innerProdFloat(buf[:FrameSize], buf[:FrameSize], FrameSize))
	// libopus evaluates the left-associative "1 + ener0" in float before
	// promoting the sum for "+ ener1".
	ener0Base := float32(1 + ener0)
	for i := range pitchXcorrFeatures {
		ener := float32(opusmath.CReal(ener0Base) + ener1)
		a.xcorrFeatures[i] = 2 * a.scratch.pitchXCorr[i]
		a.scratch.pitchEner[i] = ener
		ener1 += opusmath.CReal(buf[i+FrameSize])*opusmath.CReal(buf[i+FrameSize]) - opusmath.CReal(buf[i])*opusmath.CReal(buf[i])
	}
	for i := range pitchXcorrFeatures {
		a.xcorrFeatures[i] /= a.scratch.pitchEner[i]
	}

	a.dnnPitch = a.pitch.Compute(a.ifFeatures[:], a.xcorrFeatures[:])
	// libopus dnn/lpcnet_enc.c:compute_frame_features uses C floor()/pow().
	pitchExp := (1.0 / 60.0) * ((opusmath.CReal(a.dnnPitch) + 1.5) * 60.0)
	pitch := int(opusmath.FloorCRealToInt32(0.5 + 256.0/opusmath.PowCReal(2.0, pitchExp)))
	xx := innerProdFloat(a.lpBuf[PitchMaxPeriod:PitchMaxPeriod+FrameSize], a.lpBuf[PitchMaxPeriod:PitchMaxPeriod+FrameSize], FrameSize)
	yy := innerProdFloat(a.lpBuf[PitchMaxPeriod-pitch:PitchMaxPeriod-pitch+FrameSize], a.lpBuf[PitchMaxPeriod-pitch:PitchMaxPeriod-pitch+FrameSize], FrameSize)
	xy := innerProdFloat(a.lpBuf[PitchMaxPeriod:PitchMaxPeriod+FrameSize], a.lpBuf[PitchMaxPeriod-pitch:PitchMaxPeriod-pitch+FrameSize], FrameSize)
	// libopus dnn/lpcnet_enc.c:compute_frame_features uses C sqrt()/log()/exp().
	frameCorrArg := float32(1) + xx*yy
	frameCorr := float32(opusmath.CReal(xy) / opusmath.SqrtCReal(opusmath.CReal(frameCorrArg)))
	frameCorr = float32(opusmath.LogCReal(1.0+opusmath.ExpCReal(opusmath.CReal(5*frameCorr))) / opusmath.LogCReal(1.0+opusmath.ExpCReal(5.0)))
	a.features[NumBands] = a.dnnPitch
	a.features[NumBands+1] = frameCorr - .5
}

func (a *Analysis) computeBurgCepstrum(dst, pcm []float32, length, order int) {
	for i := 0; i < length-1; i++ {
		a.scratch.burgIn[i] = pcm[i+1] - analysisPreemphasis*pcm[i]
	}
	g := burgAnalysis(a.scratch.burgTemp[:analysisLPCOrder], a.scratch.burgIn[:length-1], 1e-3, length-1, 1, order, &a.scratch)
	g /= float32(length - 2*(order-1))
	clear(a.scratch.window[:])
	a.scratch.window[0] = 1
	for i := range order {
		// libopus dnn/freq.c:compute_burg_cepstrum uses C pow().
		a.scratch.window[i+1] = float32(-opusmath.CReal(a.scratch.burgTemp[i]) * opusmath.PowCReal(0.995, opusmath.CReal(i+1)))
	}
	for i := range analysisWindowSize {
		a.scratch.fftIn[i] = complex(a.scratch.window[i], 0)
	}
	scale := float32(1.0 / analysisWindowSize)
	celt.KissFFT32ToScaledWithScratch(a.scratch.fftOut[:], a.scratch.fftIn[:], scale, a.scratch.fftScratch[:])
	for i := range analysisFreqSize {
		a.scratch.spectrum[i] = a.scratch.fftOut[i]
	}
	computeBandEnergyInverse(a.scratch.burgBands[:], a.scratch.spectrum[:])
	logMax := float32(-2)
	follow := float32(-2)
	// libopus dnn/freq.c:compute_burg_cepstrum uses unsuffixed .45.
	gainScale := 0.45 * opusmath.CReal(g) * opusmath.CReal(float32(1.0)/float32(analysisWindowSize*analysisWindowSize*analysisWindowSize))
	for i := range NumBands {
		a.scratch.burgBands[i] = float32(opusmath.CReal(a.scratch.burgBands[i]) * gainScale)
		// libopus dnn/freq.c:compute_burg_cepstrum uses C log10().
		v := float32(opusmath.Log10CReal(1e-2 + opusmath.CReal(a.scratch.burgBands[i])))
		v = maxF32(logMax-8, maxF32(follow-2.5, v))
		a.scratch.burgLog[i] = v
		logMax = maxF32(logMax, v)
		follow = maxF32(follow-2.5, v)
	}
	dctTransform(dst[:NumBands], a.scratch.burgLog[:])
	dst[0] -= 4
}

func (a *Analysis) frameAnalysis(spectrum []complex64, ex, in []float32) {
	copy(a.scratch.window[:analysisOverlapSize], a.analysisMem[:])
	copy(a.scratch.window[analysisOverlapSize:], in[:FrameSize])
	copy(a.analysisMem[:], in[FrameSize-analysisOverlapSize:])
	applyAnalysisWindow(a.scratch.window[:])
	scale := float32(1.0 / analysisWindowSize)
	for i := range analysisWindowSize {
		a.scratch.fftIn[i] = complex(a.scratch.window[i], 0)
	}
	celt.KissFFT32ToScaledWithScratch(a.scratch.fftOut[:], a.scratch.fftIn[:], scale, a.scratch.fftScratch[:])
	for i := range analysisFreqSize {
		spectrum[i] = a.scratch.fftOut[i]
	}
	computeBandEnergy(ex, spectrum, a.dredEncoder)
}

func preemphasisInPlace(x []float32, mem *float32, coef float32) {
	if len(x) == 0 || mem == nil {
		return
	}
	m := *mem
	for i := range x {
		y := x[i] + m
		m = -coef * x[i]
		x[i] = y
	}
	*mem = m
}

func applyAnalysisWindow(x []float32) {
	for i := range analysisOverlapSize {
		x[i] *= analysisHalfWindow[i]
		x[analysisWindowSize-1-i] *= analysisHalfWindow[i]
	}
}

func computeBandEnergy(bandE []float32, spectrum []complex64, dredEncoder bool) {
	var sum [NumBands]float32
	for i := range NumBands - 1 {
		bandSize := (analysisBandEdges[i+1] - analysisBandEdges[i]) * analysisWindow5ms
		for j := range bandSize {
			frac := float32(j) / float32(bandSize)
			idx := analysisBandEdges[i]*analysisWindow5ms + j
			re := real(spectrum[idx])
			im := imag(spectrum[idx])
			tmp := re * re
			tmp += im * im
			// libopus dnn/freq.c:lpcn_compute_band_energy relies on rounded
			// float products/adds here; avoid Go contracting the weighted add.
			left := noFMA32Mul(1-frac, tmp)
			right := noFMA32Mul(frac, tmp)
			sum[i] = round32(sum[i] + left)
			sum[i+1] = round32(sum[i+1] + right)
		}
	}
	sum[0] *= 2
	sum[NumBands-1] *= 2
	copy(bandE[:NumBands], sum[:])
}

func computeBandEnergyInverse(bandE []float32, spectrum []complex64) {
	var sum [NumBands]float32
	for i := range NumBands - 1 {
		bandSize := (analysisBandEdges[i+1] - analysisBandEdges[i]) * analysisWindow5ms
		for j := range bandSize {
			frac := float32(j) / float32(bandSize)
			idx := analysisBandEdges[i]*analysisWindow5ms + j
			re := real(spectrum[idx])
			im := imag(spectrum[idx])
			tmp := re * re
			tmp += im * im
			// libopus dnn/freq.c:compute_band_energy_inverse has unsuffixed 1e-9.
			tmp = float32(1.0 / (opusmath.CReal(tmp) + 1e-9))
			sum[i] += (1 - frac) * tmp
			sum[i+1] += frac * tmp
		}
	}
	sum[0] *= 2
	sum[NumBands-1] *= 2
	copy(bandE[:NumBands], sum[:])
}

func dctTransform(out, in []float32) {
	// libopus dnn/freq.c:dct multiplies by sqrt(2./NB_BANDS) as C double.
	scale := opusmath.SqrtCReal(2.0 / opusmath.CReal(NumBands))
	for i := range NumBands {
		var sum float32
		for j := range NumBands {
			sum += in[j] * analysisDCTTable[j*NumBands+i]
		}
		out[i] = float32(opusmath.CReal(sum) * scale)
	}
}

func idctTransform(out, in []float32) {
	// libopus dnn/freq.c:idct multiplies by sqrt(2./NB_BANDS) as C double.
	scale := opusmath.SqrtCReal(2.0 / opusmath.CReal(NumBands))
	for i := range NumBands {
		var sum float32
		for j := range NumBands {
			sum += in[j] * analysisDCTTable[i*NumBands+j]
		}
		out[i] = float32(opusmath.CReal(sum) * scale)
	}
}

func lpcFromCepstrum(lpc, cepstrum []float32, scratch *analysisScratch) float32 {
	copy(scratch.lpcTmp[:], cepstrum[:NumBands])
	scratch.lpcTmp[0] += 4
	idctTransform(scratch.lpcEx[:], scratch.lpcTmp[:])
	for i := range NumBands {
		// libopus dnn/freq.c:lpc_from_cepstrum uses C pow().
		scratch.lpcEx[i] = float32(opusmath.PowCReal(10.0, opusmath.CReal(scratch.lpcEx[i])) * opusmath.CReal(analysisCompensation[i]))
	}
	return lpcFromBands(lpc, scratch.lpcEx[:], scratch)
}

func lpcFromBands(lpc, ex []float32, scratch *analysisScratch) float32 {
	interpBandGain(scratch.lpcInterp[:], ex)
	scratch.lpcInterp[analysisFreqSize-1] = 0
	for i := range analysisFreqSize {
		scratch.inverseSpec[i] = complex(scratch.lpcInterp[i], 0)
	}
	inverseTransform(scratch.inverseReal[:], scratch.inverseSpec[:], scratch)
	var ac [analysisLPCOrder + 1]float32
	copy(ac[:], scratch.inverseReal[:analysisLPCOrder+1])
	// libopus dnn/freq.c:lpc_from_bands uses unsuffixed C real constants here.
	ac[0] = float32(opusmath.CReal(ac[0]) + opusmath.CReal(ac[0])*1e-4 + 26.0/38.0)
	for i := 1; i <= analysisLPCOrder; i++ {
		ac[i] = float32(opusmath.CReal(ac[i]) * (1.0 - 6e-5*opusmath.CReal(i*i)))
	}
	return lpcnLPC(lpc, ac[:], analysisLPCOrder)
}

func interpBandGain(dst, bandE []float32) {
	clear(dst[:analysisFreqSize])
	for i := range NumBands - 1 {
		bandSize := (analysisBandEdges[i+1] - analysisBandEdges[i]) * analysisWindow5ms
		for j := range bandSize {
			frac := float32(j) / float32(bandSize)
			// libopus dnn/freq.c:interp_band_gain contracts the final add on arm64:
			// frac*bandE[i+1] is rounded, then (1-frac)*bandE[i] is fused in.
			v := fma32(1-frac, bandE[i], noFMA32Mul(frac, bandE[i+1]))
			dst[analysisBandEdges[i]*analysisWindow5ms+j] = v
		}
	}
}

func inverseTransform(out []float32, in []complex64, scratch *analysisScratch) {
	scale := float32(1.0 / analysisWindowSize)
	copy(scratch.fftIn[:analysisFreqSize], in[:analysisFreqSize])
	for i := analysisFreqSize; i < analysisWindowSize; i++ {
		v := scratch.fftIn[analysisWindowSize-i]
		scratch.fftIn[i] = complex(real(v), -imag(v))
	}
	celt.KissFFT32ToScaledWithScratch(scratch.fftOut[:], scratch.fftIn[:], scale, scratch.fftScratch[:])
	outputScale := float32(analysisWindowSize)
	out[0] = outputScale * real(scratch.fftOut[0])
	for i := 1; i < analysisWindowSize; i++ {
		out[i] = outputScale * real(scratch.fftOut[analysisWindowSize-i])
	}
}

func lpcnLPC(lpc, ac []float32, order int) float32 {
	var rc [analysisLPCOrder]float32
	clear(lpc[:order])
	clear(rc[:order])
	err := ac[0]
	if ac[0] == 0 {
		return err
	}
	for i := range order {
		rr := lpcnRR(lpc[:order], ac, i)
		rr += ac[i+1]
		r := -rr / err
		rc[i] = r
		lpc[i] = r
		for j := 0; j < (i+1)>>1; j++ {
			tmp1 := lpc[j]
			tmp2 := lpc[i-1-j]
			lpc[j] = fma32(r, tmp2, tmp1)
			lpc[i-1-j] = fma32(r, tmp1, tmp2)
		}
		err = fma32(-(r * r), err, err)
		if err < .001*ac[0] {
			break
		}
	}
	return err
}

func lpcnRR(lpc, ac []float32, i int) float32 {
	var rr float32
	j := 0
	// libopus dnn/freq.c:lpcn_lpc is compiled as 16/4-wide vector products on
	// arm64 clang, with rounded FMUL products reduced in lane order; scalar
	// leftovers use FMADD.
	for ; j+15 < i; j += 16 {
		rr += noFMA32Mul(lpc[j+0], ac[i-j-0])
		rr += noFMA32Mul(lpc[j+1], ac[i-j-1])
		rr += noFMA32Mul(lpc[j+2], ac[i-j-2])
		rr += noFMA32Mul(lpc[j+3], ac[i-j-3])
		rr += noFMA32Mul(lpc[j+4], ac[i-j-4])
		rr += noFMA32Mul(lpc[j+5], ac[i-j-5])
		rr += noFMA32Mul(lpc[j+6], ac[i-j-6])
		rr += noFMA32Mul(lpc[j+7], ac[i-j-7])
		rr += noFMA32Mul(lpc[j+8], ac[i-j-8])
		rr += noFMA32Mul(lpc[j+9], ac[i-j-9])
		rr += noFMA32Mul(lpc[j+10], ac[i-j-10])
		rr += noFMA32Mul(lpc[j+11], ac[i-j-11])
		rr += noFMA32Mul(lpc[j+12], ac[i-j-12])
		rr += noFMA32Mul(lpc[j+13], ac[i-j-13])
		rr += noFMA32Mul(lpc[j+14], ac[i-j-14])
		rr += noFMA32Mul(lpc[j+15], ac[i-j-15])
	}
	for ; j+3 < i; j += 4 {
		rr += noFMA32Mul(lpc[j+0], ac[i-j-0])
		rr += noFMA32Mul(lpc[j+1], ac[i-j-1])
		rr += noFMA32Mul(lpc[j+2], ac[i-j-2])
		rr += noFMA32Mul(lpc[j+3], ac[i-j-3])
	}
	for ; j < i; j++ {
		rr = fma32(lpc[j], ac[i-j], rr)
	}
	return rr
}

// round32 forces x to float32 precision. Go's arm64 backend may contract a*b+c
// into a single FMADD (one rounding), which diverges from scalar libopus (two
// roundings); wrapping the product as round32(a*b) materializes it at float32
// precision so the surrounding add/sub cannot fuse, matching the scalar
// reference on every build. It is the cheap barrier — an FMUL+FADD pair rather
// than the FMUL+FMOV+FMOV+FADD of a Float32bits round-trip — and a no-op on
// amd64 and the purego oracle, which do not contract FP. Keep this tiny; its
// fusion-defeating codegen is guarded by the package parity tests.
func round32(x float32) float32 {
	return float32(x)
}

func noFMA32Mul(a, b float32) float32 {
	return round32(a * b)
}

func burgAnalysis(dst, x []float32, minInvGain float32, subfrLength, nbSubfr, order int, scratch *analysisScratch) float32 {
	totalLen := nbSubfr * subfrLength
	if totalLen > len(x) || order > analysisLPCOrder {
		clear(dst[:order])
		return 0
	}
	af := scratch.burgAF[:order]
	first := scratch.burgFirst[:order]
	last := scratch.burgLast[:order]
	caf := scratch.burgCAF[:order+1]
	cab := scratch.burgCAB[:order+1]
	for i := range af {
		af[i] = 0
		first[i] = 0
		last[i] = 0
	}
	for i := range caf {
		caf[i] = 0
		cab[i] = 0
	}

	c0 := energyCReal(x[:totalLen])
	for s := range nbSubfr {
		xPtr := x[s*subfrLength:]
		for n := 1; n <= order; n++ {
			first[n-1] += innerProdCReal(xPtr[:subfrLength-n], xPtr[n:subfrLength], subfrLength-n)
		}
	}
	copy(last, first)
	caf[0] = c0 + 1e-5*c0 + 1e-9
	cab[0] = caf[0]
	invGain := opusmath.CReal(1.0)
	reachedMaxGain := false

	for n := range order {
		for s := range nbSubfr {
			xPtr := x[s*subfrLength:]
			tmp1 := opusmath.CReal(xPtr[n])
			tmp2 := opusmath.CReal(xPtr[subfrLength-n-1])
			for k := 0; k < n; k++ {
				first[k] -= opusmath.CReal(xPtr[n]) * opusmath.CReal(xPtr[n-k-1])
				last[k] -= opusmath.CReal(xPtr[subfrLength-n-1]) * opusmath.CReal(xPtr[subfrLength-n+k])
				atmp := af[k]
				tmp1 += opusmath.CReal(xPtr[n-k-1]) * atmp
				tmp2 += opusmath.CReal(xPtr[subfrLength-n+k]) * atmp
			}
			for k := 0; k <= n; k++ {
				caf[k] -= tmp1 * opusmath.CReal(xPtr[n-k])
				cab[k] -= tmp2 * opusmath.CReal(xPtr[subfrLength-n+k-1])
			}
		}
		tmp1 := first[n]
		tmp2 := last[n]
		for k := 0; k < n; k++ {
			atmp := af[k]
			tmp1 += last[n-k-1] * atmp
			tmp2 += first[n-k-1] * atmp
		}
		caf[n+1] = tmp1
		cab[n+1] = tmp2

		num := cab[n+1]
		nrgB := cab[0]
		nrgF := caf[0]
		for k := 0; k < n; k++ {
			atmp := af[k]
			num += cab[n-k] * atmp
			nrgB += cab[k+1] * atmp
			nrgF += caf[k+1] * atmp
		}
		rc := -2.0 * num / (nrgF + nrgB)
		tmp1 = invGain * (1.0 - rc*rc)
		if tmp1 <= opusmath.CReal(minInvGain) {
			rc = opusmath.SqrtCReal(1.0 - opusmath.CReal(minInvGain)/invGain)
			if num > 0 {
				rc = -rc
			}
			invGain = opusmath.CReal(minInvGain)
			reachedMaxGain = true
		} else {
			invGain = tmp1
		}

		for k := 0; k < (n+1)>>1; k++ {
			tmp1 = af[k]
			tmp2 = af[n-k-1]
			af[k] = tmp1 + rc*tmp2
			af[n-k-1] = tmp2 + rc*tmp1
		}
		af[n] = rc
		if reachedMaxGain {
			for k := n + 1; k < order; k++ {
				af[k] = 0
			}
			break
		}
		for k := 0; k <= n+1; k++ {
			tmp1 = caf[k]
			caf[k] += rc * cab[n-k+1]
			cab[n-k+1] += rc * tmp1
		}
	}

	var nrgF opusmath.CReal
	if reachedMaxGain {
		for k := range order {
			dst[k] = float32(-af[k])
		}
		for s := range nbSubfr {
			c0 -= energyCReal(x[s*subfrLength : s*subfrLength+order])
		}
		nrgF = c0 * invGain
	} else {
		nrgF = caf[0]
		tmp1 := opusmath.CReal(1.0)
		for k := range order {
			atmp := af[k]
			nrgF += caf[k+1] * atmp
			tmp1 += atmp * atmp
			dst[k] = float32(-atmp)
		}
		nrgF -= 1e-5 * c0 * tmp1
	}
	if nrgF < 0 {
		return 0
	}
	return float32(nrgF)
}

func energyCReal(x []float32) opusmath.CReal {
	var sum opusmath.CReal
	for _, v := range x {
		sum += opusmath.CReal(v) * opusmath.CReal(v)
	}
	return sum
}

func innerProdCReal(x, y []float32, n int) opusmath.CReal {
	var sum opusmath.CReal
	for i := range n {
		sum += opusmath.CReal(x[i]) * opusmath.CReal(y[i])
	}
	return sum
}

func celtFIRFloat(inWithHistory, coeffs, out []float32) {
	var rnum [analysisLPCOrder]float32
	for i := range analysisLPCOrder {
		rnum[i] = coeffs[analysisLPCOrder-i-1]
	}
	i := 0
	for ; i < FrameSize-3; i += 4 {
		sum := [4]float32{
			inWithHistory[analysisLPCOrder+i],
			inWithHistory[analysisLPCOrder+i+1],
			inWithHistory[analysisLPCOrder+i+2],
			inWithHistory[analysisLPCOrder+i+3],
		}
		xcorrKernel4Float32(rnum[:], inWithHistory[i:], &sum, analysisLPCOrder)
		out[i] = sum[0]
		out[i+1] = sum[1]
		out[i+2] = sum[2]
		out[i+3] = sum[3]
	}
	for ; i < FrameSize; i++ {
		sum := inWithHistory[analysisLPCOrder+i]
		for j := range analysisLPCOrder {
			sum += rnum[j] * inWithHistory[i+j]
		}
		out[i] = sum
	}
}

func xcorrKernel4Float32(x, y []float32, sum *[4]float32, length int) {
	if length < 3 {
		return
	}
	xi := 0
	yi := 0
	y3 := float32(0)
	y0 := y[yi]
	yi++
	y1 := y[yi]
	yi++
	y2 := y[yi]
	yi++
	j := 0
	for ; j < length-3; j += 4 {
		tmp := x[xi]
		xi++
		y3 = y[yi]
		yi++
		sum[0] += tmp * y0
		sum[1] += tmp * y1
		sum[2] += tmp * y2
		sum[3] += tmp * y3
		tmp = x[xi]
		xi++
		y0 = y[yi]
		yi++
		sum[0] += tmp * y1
		sum[1] += tmp * y2
		sum[2] += tmp * y3
		sum[3] += tmp * y0
		tmp = x[xi]
		xi++
		y1 = y[yi]
		yi++
		sum[0] += tmp * y2
		sum[1] += tmp * y3
		sum[2] += tmp * y0
		sum[3] += tmp * y1
		tmp = x[xi]
		xi++
		y2 = y[yi]
		yi++
		sum[0] += tmp * y3
		sum[1] += tmp * y0
		sum[2] += tmp * y1
		sum[3] += tmp * y2
	}
	j++
	if j < length {
		tmp := x[xi]
		xi++
		y3 = y[yi]
		yi++
		sum[0] += tmp * y0
		sum[1] += tmp * y1
		sum[2] += tmp * y2
		sum[3] += tmp * y3
	}
	j++
	if j < length {
		tmp := x[xi]
		xi++
		y0 = y[yi]
		yi++
		sum[0] += tmp * y1
		sum[1] += tmp * y2
		sum[2] += tmp * y3
		sum[3] += tmp * y0
	}
	if j < length {
		tmp := x[xi]
		y1 = y[yi]
		sum[0] += tmp * y2
		sum[1] += tmp * y3
		sum[2] += tmp * y0
		sum[3] += tmp * y1
	}
}

func biquadInPlace(y, mem []float32) {
	b0 := float32(-0.84946)
	b1 := float32(1.0)
	a0 := float32(-1.54220)
	a1 := float32(0.70781)
	mem0 := mem[0]
	mem1 := mem[1]
	for i := range y {
		xi := y[i]
		yi := xi + mem0
		mem00 := mem0
		mem0 = (b0-a0)*xi + mem1 - a0*mem0
		mem1 = (b1-a1)*xi + 1e-30 - a1*mem00
		y[i] = yi
	}
	mem[0] = mem0
	mem[1] = mem1
}

func pitchXCorrFloat(dst, x, y []float32, length, maxPitch int) {
	if useNEONAnalysisKernels {
		pitchXCorrFloatNEON(dst, x, y, length, maxPitch)
		return
	}
	for i := range maxPitch {
		var sum float32
		for j := range length {
			sum += x[j] * y[i+j]
		}
		dst[i] = sum
	}
}

func innerProdFloat(x, y []float32, length int) float32 {
	if useNEONAnalysisKernels {
		return innerProdFloatNEON(x, y, length)
	}
	// libopus celt/pitch.h:celt_inner_prod_c is MAC16_16 (c + a*b) in the
	// float build. At -O3 the scalar DRED reference build auto-vectorizes the
	// FRAME_SIZE reduction into 4-wide NEON: every product is a rounded FMUL,
	// then the products are reduced into a single accumulator in index order
	// with plain FADDs (no FMA fusion). Round each product before accumulating
	// so the Go arm64 backend cannot fuse this into FMADDS and diverge from the
	// reference by a last bit (this surfaces in features[19] frame_corr).
	var sum float32
	for i := range length {
		sum += noFMA32Mul(x[i], y[i])
	}
	return sum
}

func pitchXCorrFloatNEON(dst, x, y []float32, length, maxPitch int) {
	i := 0
	for ; i < maxPitch-3; i += 4 {
		var sum0, sum1, sum2, sum3 float32
		for j := range length {
			xj := x[j]
			base := i + j
			sum0 = fma32(xj, y[base], sum0)
			sum1 = fma32(xj, y[base+1], sum1)
			sum2 = fma32(xj, y[base+2], sum2)
			sum3 = fma32(xj, y[base+3], sum3)
		}
		dst[i] = sum0
		dst[i+1] = sum1
		dst[i+2] = sum2
		dst[i+3] = sum3
	}
	for ; i < maxPitch; i++ {
		dst[i] = innerProdFloatNEON(x, y[i:], length)
	}
}

func innerProdFloatNEON(x, y []float32, length int) float32 {
	var acc [4]float32
	i := 0
	for ; i < length-7; i += 8 {
		for lane := range 4 {
			acc[lane] = fma32(x[i+lane], y[i+lane], acc[lane])
		}
		for lane := range 4 {
			acc[lane] = fma32(x[i+4+lane], y[i+4+lane], acc[lane])
		}
	}
	if length-i >= 4 {
		for lane := range 4 {
			acc[lane] = fma32(x[i+lane], y[i+lane], acc[lane])
		}
		i += 4
	}
	sum0 := round32(acc[0] + acc[2])
	sum1 := round32(acc[1] + acc[3])
	sum := round32(sum0 + sum1)
	for ; i < length; i++ {
		sum = fma32(x[i], y[i], sum)
	}
	return sum
}

func log10f(x float32) float32 {
	return 0.3010299957 * opusmath.CeltLog2(x)
}

func clampUnit(x float32) float32 {
	if x < -1 {
		return -1
	}
	if x > 1 {
		return 1
	}
	return x
}

func mulConj(a, b complex64) complex64 {
	return complex(real(a)*real(b)+imag(a)*imag(b), imag(a)*real(b)-real(a)*imag(b))
}

var analysisBandEdges = [...]int{
	0, 1, 2, 3, 4, 5, 6, 7, 8, 10, 12, 14, 16, 20, 24, 28, 34, 40,
}

var analysisCompensation = [...]float32{
	0.8, 1, 1, 1, 1, 1, 1, 1, 0.666667, 0.5, 0.5, 0.5, 0.333333, 0.25, 0.25, 0.2, 0.166667, 0.173913,
}
