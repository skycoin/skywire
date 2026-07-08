// Package lpcnetplc implements the libopus LPCNet packet-loss concealment and
// DRED recovery decoder.
//
// It ports the deep PLC pipeline from libopus 1.6.1 dnn/lpcnet_plc.c and its
// neural building blocks: the feature-prediction model (Model / LoadModel), the
// burg/pitch analysis front end (Analysis), the PitchDNN pitch-period network
// (PitchDNN) and the FARGAN auto-regressive vocoder (FARGAN / FARGANConditioner)
// that resynthesizes time-domain audio. State carries the per-decoder
// concealment state that the DRED recovery path mutates before resynthesis.
//
// All weights are bound from a validated dnnblob.Blob; the math mirrors libopus
// bit-for-bit and is held in place by the parity tests in this package and the
// decoder PLC/DRED parity tests that consume it.
package lpcnetplc

import "github.com/thesyncim/gopus/internal/opusmath"

// Constants mirrored from libopus 1.6.1 dnn/lpcnet headers.
const (
	NumFeatures       = 20
	NumTotalFeatures  = 36
	FrameSize         = 160
	ContVectors       = 5
	PLCBufSize        = (ContVectors + 10) * FrameSize
	FARGANContSamples = 320
	MaxFEC            = 104
)

// State mirrors the low-cost LPCNet PLC state that the libopus DRED recovery
// path mutates before audio concealment: the retained feature queue, predictor
// backups, feature history and PCM history used around the predictor-driven
// part of lpcnet_plc_conceal().
type State struct {
	runtimeInit bool
	fec         [MaxFEC][NumFeatures]float32
	fecReadPos  int
	fecFillPos  int
	fecSkip     int
	analysisGap int
	analysisPos int
	predictPos  int
	pcm         [PLCBufSize]float32
	blend       int
	features    [NumTotalFeatures]float32
	cont        [ContVectors * NumFeatures]float32
	lossCount   int
	plcNet      predictorState
	plcBak      [2]predictorState
}

// Reset clears the retained queue state and resets blend to the libopus
// post-update default.
func (s *State) Reset() {
	if s == nil {
		return
	}
	*s = State{
		runtimeInit: true,
		analysisGap: 1,
		analysisPos: PLCBufSize,
		predictPos:  PLCBufSize,
	}
}

func (s *State) ensureRuntimeInit() {
	if s == nil || s.runtimeInit {
		return
	}
	s.runtimeInit = true
	s.analysisGap = 1
	s.analysisPos = PLCBufSize
	s.predictPos = PLCBufSize
}

// FECClear mirrors lpcnet_plc_fec_clear().
func (s *State) FECClear() {
	if s == nil {
		return
	}
	s.ensureRuntimeInit()
	s.fecReadPos = 0
	s.fecFillPos = 0
	s.fecSkip = 0
}

// FECAdd mirrors lpcnet_plc_fec_add(). A nil features slice records a skipped
// positive feature offset.
func (s *State) FECAdd(features []float32) {
	if s == nil {
		return
	}
	s.ensureRuntimeInit()
	if features == nil {
		s.fecSkip++
		return
	}
	if s.fecFillPos >= MaxFEC || len(features) < NumFeatures {
		return
	}
	copy(s.fec[s.fecFillPos][:], features[:NumFeatures])
	s.fecFillPos++
}

// MarkUpdated mirrors lpcnet_plc_update()'s blend reset.
func (s *State) MarkUpdated() {
	if s == nil {
		return
	}
	s.ensureRuntimeInit()
	s.lossCount = 0
	s.blend = 0
}

// ClearBlend mirrors the libopus good-packet handoff when PLC updates are
// skipped: a successful packet exits concealment by clearing only the blend
// flag, leaving queued/history state intact.
func (s *State) ClearBlend() {
	if s == nil {
		return
	}
	s.ensureRuntimeInit()
	s.blend = 0
}

// MarkConcealed mirrors lpcnet_plc_conceal()'s post-blend state.
func (s *State) MarkConcealed() {
	if s == nil {
		return
	}
	s.ensureRuntimeInit()
	s.blend = 1
}

// Blend reports the retained libopus LPCNet PLC blend flag.
func (s *State) Blend() int {
	if s == nil {
		return 0
	}
	s.ensureRuntimeInit()
	return s.blend
}

// FECFillPos reports how many concrete feature vectors are queued.
func (s *State) FECFillPos() int {
	if s == nil {
		return 0
	}
	s.ensureRuntimeInit()
	return s.fecFillPos
}

// FECReadPos reports the next queued feature vector read position.
func (s *State) FECReadPos() int {
	if s == nil {
		return 0
	}
	s.ensureRuntimeInit()
	return s.fecReadPos
}

// FECSkip reports how many positive feature offsets were recorded as missing.
func (s *State) FECSkip() int {
	if s == nil {
		return 0
	}
	s.ensureRuntimeInit()
	return s.fecSkip
}

// FillQueuedFeatures copies one queued feature vector into dst and returns the
// number of floats written.
func (s *State) FillQueuedFeatures(slot int, dst []float32) int {
	if s != nil {
		s.ensureRuntimeInit()
	}
	if s == nil || slot < 0 || slot >= s.fecFillPos {
		return 0
	}
	n := min(NumFeatures, len(dst))
	copy(dst[:n], s.fec[slot][:n])
	return n
}

// AnalysisGap reports whether the retained PCM history has a gap for the
// predictor-side analysis loop.
func (s *State) AnalysisGap() int {
	if s == nil {
		return 0
	}
	s.ensureRuntimeInit()
	return s.analysisGap
}

// AnalysisPos reports the retained PCM-analysis cursor in samples.
func (s *State) AnalysisPos() int {
	if s == nil {
		return 0
	}
	s.ensureRuntimeInit()
	return s.analysisPos
}

// PredictPos reports the retained prediction cursor in samples.
func (s *State) PredictPos() int {
	if s == nil {
		return 0
	}
	s.ensureRuntimeInit()
	return s.predictPos
}

// LossCount reports the retained libopus concealment loss counter.
func (s *State) LossCount() int {
	if s == nil {
		return 0
	}
	s.ensureRuntimeInit()
	return s.lossCount
}

// FillContFeatures copies the retained continuity feature queue into dst and
// returns the number of floats written.
func (s *State) FillContFeatures(dst []float32) int {
	if s == nil {
		return 0
	}
	s.ensureRuntimeInit()
	n := min(len(s.cont), len(dst))
	copy(dst[:n], s.cont[:n])
	return n
}

// FillPCMHistory copies the retained PCM history window into dst and returns
// the number of floats written.
func (s *State) FillPCMHistory(dst []float32) int {
	if s == nil {
		return 0
	}
	s.ensureRuntimeInit()
	n := min(len(s.pcm), len(dst))
	copy(dst[:n], s.pcm[:n])
	return n
}

// FillCurrentFeatures copies the retained current feature vector into dst and
// returns the number of floats written.
func (s *State) FillCurrentFeatures(dst []float32) int {
	if s == nil {
		return 0
	}
	s.ensureRuntimeInit()
	n := min(NumFeatures, len(dst))
	copy(dst[:n], s.features[:n])
	return n
}

// QueueFeatures mirrors queue_features() for the 5-vector continuity history.
func (s *State) QueueFeatures(features []float32) {
	if s == nil || len(features) < NumFeatures {
		return
	}
	s.ensureRuntimeInit()
	copy(s.cont[:(ContVectors-1)*NumFeatures], s.cont[NumFeatures:])
	copy(s.cont[(ContVectors-1)*NumFeatures:], features[:NumFeatures])
}

// SyncPredictor snapshots the current predictor state into the retained
// plc_net mirror.
func (s *State) SyncPredictor(p *Predictor) {
	if s == nil || p == nil {
		return
	}
	s.ensureRuntimeInit()
	p.copyState(&s.plcNet)
}

// RestorePredictor restores the retained plc_net state into the predictor.
func (s *State) RestorePredictor(p *Predictor) {
	if s == nil || p == nil {
		return
	}
	s.ensureRuntimeInit()
	p.setState(&s.plcNet)
}

// RestorePredictorBackup restores one retained plc_bak snapshot into the
// predictor and plc_net mirror.
func (s *State) RestorePredictorBackup(p *Predictor, idx int) {
	if s == nil || p == nil || idx < 0 || idx >= len(s.plcBak) {
		return
	}
	s.ensureRuntimeInit()
	p.setState(&s.plcBak[idx])
	p.copyState(&s.plcNet)
}

func (s *State) rotatePredictorBackup() {
	s.ensureRuntimeInit()
	s.plcBak[0] = s.plcBak[1]
	s.plcBak[1] = s.plcNet
}

// StepFECOrPredict mirrors the predictor-side backup rotation and
// get_fec_or_pred() call that libopus uses inside lpcnet_plc_conceal().
func (s *State) StepFECOrPredict(p *Predictor, out []float32) bool {
	if s == nil || p == nil || !p.Loaded() || len(out) < NumFeatures {
		return false
	}
	s.ensureRuntimeInit()
	p.copyState(&s.plcNet)
	s.rotatePredictorBackup()
	gotFEC := s.ConsumeFECOrPredict(p, out)
	p.copyState(&s.plcNet)
	return gotFEC
}

func (s *State) primeFirstLossPrefillCurrentPredictor(p *Predictor) int {
	if s == nil || p == nil || !p.Loaded() {
		return 0
	}
	s.ensureRuntimeInit()
	for range 2 {
		s.StepFECOrPredict(p, s.features[:NumFeatures])
		s.QueueFeatures(s.features[:NumFeatures])
	}
	return 2
}

// PrimeFirstLossPrefill mirrors the post-analysis first-loss predictor prefill
// that runs before FARGAN continuity priming. It restores plc_bak[0], then
// generates and queues the two extra causal feature vectors libopus needs.
func (s *State) PrimeFirstLossPrefill(p *Predictor) int {
	if s == nil || p == nil || !p.Loaded() || s.blend != 0 {
		return 0
	}
	s.ensureRuntimeInit()
	s.RestorePredictorBackup(p, 0)
	return s.primeFirstLossPrefillCurrentPredictor(p)
}

// PrimeFirstLossContinuity mirrors the bounded post-analysis FARGAN continuity
// priming inside lpcnet_plc_conceal(). It assumes the caller already has the
// continuity features and PCM tail the analysis loop would have produced.
func (s *State) PrimeFirstLossContinuity(f *FARGAN) int {
	if s == nil || f == nil || !f.Loaded() || s.blend != 0 {
		return 0
	}
	s.ensureRuntimeInit()
	if n := f.PrimeContinuity(s.pcm[PLCBufSize-FARGANContSamples:], s.cont[:]); n == FARGANContSamples {
		s.analysisGap = 0
		return n
	}
	return 0
}

var concealmentAttTable = [...]float32{0, 0, -.2, -.2, -.4, -.4, -.8, -.8, -1.6, -1.6}

// ConcealmentFeatureStep mirrors the predictor-backed feature evolution inside
// lpcnet_plc_conceal(), excluding the final FARGAN audio synthesis.
func (s *State) ConcealmentFeatureStep(p *Predictor) bool {
	if s == nil || p == nil || !p.Loaded() {
		return false
	}
	s.ensureRuntimeInit()
	gotFEC := s.StepFECOrPredict(p, s.features[:NumFeatures])
	if gotFEC {
		s.lossCount = 0
	} else {
		s.lossCount++
	}
	if s.lossCount >= len(concealmentAttTable) {
		s.features[0] = maxF32(-15, s.features[0]+concealmentAttTable[len(concealmentAttTable)-1]-2*float32(s.lossCount-(len(concealmentAttTable)-1)))
	} else {
		s.features[0] = maxF32(-15, s.features[0]+concealmentAttTable[s.lossCount])
	}
	s.QueueFeatures(s.features[:NumFeatures])
	return gotFEC
}

// PrimeFirstLossWithAnalysis mirrors the full first-loss history-analysis
// branch of lpcnet_plc_conceal(). It replays retained PCM history through Burg
// cepstral analysis and LPCNet feature extraction, advances predictor backup
// state, generates the two extra causal feature vectors, and primes FARGAN
// continuity.
func (s *State) PrimeFirstLossWithAnalysis(a *Analysis, p *Predictor, f *FARGAN) bool {
	if s == nil || a == nil || p == nil || f == nil || !a.Loaded() || !p.Loaded() || !f.Loaded() || s.blend != 0 {
		return false
	}
	s.ensureRuntimeInit()
	s.RestorePredictorBackup(p, 0)
	count := 0
	var plcFeatures [InputSize]float32
	for s.analysisPos+FrameSize <= PLCBufSize {
		if s.analysisPos < 0 {
			return false
		}
		history := s.pcm[s.analysisPos : s.analysisPos+FrameSize]
		for i := range FrameSize {
			a.scratch.frame[i] = 32768 * history[i]
		}
		if n := a.BurgCepstralAnalysis(plcFeatures[:], a.scratch.frame[:]); n != 2*NumBands {
			return false
		}
		if n := a.ComputeSingleFrameFeaturesFloat(s.features[:], a.scratch.frame[:]); n != NumTotalFeatures {
			return false
		}
		if (s.analysisGap == 0 || count > 0) && s.analysisPos >= s.predictPos {
			s.QueueFeatures(s.features[:NumFeatures])
			copy(plcFeatures[2*NumBands:], s.features[:NumFeatures])
			plcFeatures[2*NumBands+NumFeatures] = 1
			p.copyState(&s.plcNet)
			s.rotatePredictorBackup()
			if n := p.Predict(s.features[:NumFeatures], plcFeatures[:]); n != NumFeatures {
				return false
			}
			p.copyState(&s.plcNet)
		}
		s.analysisPos += FrameSize
		count++
	}
	if s.primeFirstLossPrefillCurrentPredictor(p) != 2 {
		return false
	}
	if n := f.PrimeContinuity(s.pcm[PLCBufSize-FARGANContSamples:], s.cont[:]); n != FARGANContSamples {
		return false
	}
	s.analysisGap = 0
	return true
}

func (s *State) concealFrameFloatAfterPrimingStatus(p *Predictor, f *FARGAN, frame []float32) (bool, bool) {
	if s == nil || p == nil || f == nil || !p.Loaded() || !f.Loaded() || len(frame) < FrameSize {
		return false, false
	}
	s.ensureRuntimeInit()
	gotFEC := s.StepFECOrPredict(p, s.features[:NumFeatures])
	if gotFEC {
		s.lossCount = 0
	} else {
		s.lossCount++
	}
	if s.lossCount >= len(concealmentAttTable) {
		s.features[0] = maxF32(-15, s.features[0]+concealmentAttTable[len(concealmentAttTable)-1]-2*float32(s.lossCount-(len(concealmentAttTable)-1)))
	} else {
		s.features[0] = maxF32(-15, s.features[0]+concealmentAttTable[s.lossCount])
	}
	if n := f.Synthesize(frame[:FrameSize], s.features[:NumFeatures]); n != FrameSize {
		return false, gotFEC
	}
	quantizePCMInt16LikeInPlace(frame[:FrameSize])
	s.QueueFeatures(s.features[:NumFeatures])
	s.FinishConcealedFrameFloat(frame[:FrameSize])
	return true, gotFEC
}

// ConcealFrameFloat mirrors the bounded post-analysis branch of
// lpcnet_plc_conceal(). When blend is zero it assumes the caller already has
// the continuity features and PCM tail the history-analysis branch would have
// produced, then synthesizes one concealed frame. It reports whether the step
// consumed queued FEC, not whether concealment succeeded.
func (s *State) ConcealFrameFloat(p *Predictor, f *FARGAN, frame []float32) bool {
	if s == nil || p == nil || f == nil || !p.Loaded() || !f.Loaded() || len(frame) < FrameSize {
		return false
	}
	s.ensureRuntimeInit()
	if s.blend == 0 {
		if s.PrimeFirstLossPrefill(p) != 2 {
			return false
		}
		if s.PrimeFirstLossContinuity(f) != FARGANContSamples {
			return false
		}
	}
	ok, gotFEC := s.concealFrameFloatAfterPrimingStatus(p, f, frame)
	if !ok {
		return false
	}
	return gotFEC
}

// ConcealFrameFloatWithAnalysis mirrors the full lpcnet_plc_conceal() float
// path, including the first-loss history replay through Burg/LPCNet analysis
// before the final predictor/FARGAN synthesis step. It reports whether the step
// consumed queued FEC, not whether concealment succeeded.
func (s *State) ConcealFrameFloatWithAnalysis(a *Analysis, p *Predictor, f *FARGAN, frame []float32) bool {
	if s == nil || a == nil || p == nil || f == nil || !a.Loaded() || !p.Loaded() || !f.Loaded() || len(frame) < FrameSize {
		return false
	}
	s.ensureRuntimeInit()
	if s.blend == 0 {
		if !s.PrimeFirstLossWithAnalysis(a, p, f) {
			return false
		}
	}
	ok, gotFEC := s.concealFrameFloatAfterPrimingStatus(p, f, frame)
	if !ok {
		return false
	}
	return gotFEC
}

// GenerateConcealedFrameFloat mirrors the bounded post-analysis branch of
// lpcnet_plc_conceal() and reports whether concealment succeeded.
func (s *State) GenerateConcealedFrameFloat(p *Predictor, f *FARGAN, frame []float32) bool {
	if s == nil || p == nil || f == nil || !p.Loaded() || !f.Loaded() || len(frame) < FrameSize {
		return false
	}
	s.ensureRuntimeInit()
	if s.blend == 0 {
		if s.PrimeFirstLossPrefill(p) != 2 {
			return false
		}
		if s.PrimeFirstLossContinuity(f) != FARGANContSamples {
			return false
		}
	}
	ok, _ := s.concealFrameFloatAfterPrimingStatus(p, f, frame)
	return ok
}

// GenerateConcealedFrameFloatWithAnalysis mirrors the full lpcnet_plc_conceal()
// float path and reports whether concealment succeeded.
func (s *State) GenerateConcealedFrameFloatWithAnalysis(a *Analysis, p *Predictor, f *FARGAN, frame []float32) bool {
	if s == nil || a == nil || p == nil || f == nil || !a.Loaded() || !p.Loaded() || !f.Loaded() || len(frame) < FrameSize {
		return false
	}
	s.ensureRuntimeInit()
	if s.blend == 0 {
		if !s.PrimeFirstLossWithAnalysis(a, p, f) {
			return false
		}
	}
	ok, _ := s.concealFrameFloatAfterPrimingStatus(p, f, frame)
	return ok
}

// ReplaceHistoryFromFramesFloat replaces the retained PCM-history window with a
// fresh run of decoded 10 ms frames without disturbing queued FEC or predictor
// state. This mirrors libopus update_plc_state() semantics at the lpcnet state
// level when neural PLC is entered from CELT decode history.
func (s *State) ReplaceHistoryFromFramesFloat(frames []float32) int {
	if s == nil || len(frames) < FrameSize {
		return 0
	}
	s.ensureRuntimeInit()
	frameCount := len(frames) / FrameSize
	if frameCount <= 0 {
		return 0
	}
	if frameCount*FrameSize > PLCBufSize {
		frameCount = PLCBufSize / FrameSize
		frames = frames[len(frames)-frameCount*FrameSize:]
	} else {
		frames = frames[:frameCount*FrameSize]
	}

	clear(s.pcm[:])
	s.analysisGap = 1
	s.analysisPos = PLCBufSize
	s.predictPos = PLCBufSize
	s.lossCount = 0
	s.blend = 0

	total := 0
	for offset := 0; offset+FrameSize <= len(frames); offset += FrameSize {
		total += s.MarkUpdatedFrameFloat(frames[offset : offset+FrameSize])
	}
	return total
}

// MarkUpdatedFrameFloat mirrors the PCM-history and cursor maintenance in
// lpcnet_plc_update() for one 10 ms frame on the float path.
func (s *State) MarkUpdatedFrameFloat(frame []float32) int {
	if s == nil || len(frame) < FrameSize {
		return 0
	}
	s.startUpdatedFrame()
	for i := range FrameSize {
		s.pcm[PLCBufSize-FrameSize+i] = quantizePCMUpdateFloat(frame[i])
	}
	s.finishUpdatedFrame()
	return FrameSize
}

// MarkUpdatedFrameInt16 mirrors lpcnet_plc_update() once decoded PCM already
// exists on libopus's exact opus_int16 grid.
func (s *State) MarkUpdatedFrameInt16(frame []int16) int {
	if s == nil || len(frame) < FrameSize {
		return 0
	}
	s.startUpdatedFrame()
	for i := range FrameSize {
		s.pcm[PLCBufSize-FrameSize+i] = float32(frame[i]) * (1.0 / 32768.0)
	}
	s.finishUpdatedFrame()
	return FrameSize
}

// MarkUpdatedFrameWithAnalysis mirrors the good-packet lpcnet_plc_update()
// cadence for one 10 ms frame while also advancing the retained LPCNet
// analysis frontend and continuity vectors.
func (s *State) MarkUpdatedFrameWithAnalysis(a *Analysis, frame []float32) int {
	if s == nil || len(frame) < FrameSize {
		return 0
	}
	if s.MarkUpdatedFrameFloat(frame) != FrameSize {
		return 0
	}
	if a == nil || !a.Loaded() {
		return FrameSize
	}
	if n := a.ComputeSingleFrameFeaturesFloat(s.features[:], frame[:FrameSize]); n != NumTotalFeatures {
		return FrameSize
	}
	s.QueueFeatures(s.features[:NumFeatures])
	s.analysisGap = 0
	return FrameSize
}

// FinishConcealedFrameFloat mirrors the retained PCM-history tail and cursor
// maintenance that lpcnet_plc_conceal() performs after feature synthesis.
func (s *State) FinishConcealedFrameFloat(frame []float32) int {
	if s == nil || len(frame) < FrameSize {
		return 0
	}
	s.ensureRuntimeInit()
	if s.analysisPos-FrameSize >= 0 {
		s.analysisPos -= FrameSize
	} else {
		s.analysisGap = 1
	}
	s.predictPos = PLCBufSize
	copy(s.pcm[:PLCBufSize-FrameSize], s.pcm[FrameSize:])
	copy(s.pcm[PLCBufSize-FrameSize:], frame[:FrameSize])
	s.blend = 1
	return FrameSize
}

func (s *State) startUpdatedFrame() {
	s.ensureRuntimeInit()
	if s.analysisPos-FrameSize >= 0 {
		s.analysisPos -= FrameSize
	} else {
		s.analysisGap = 1
	}
	if s.predictPos-FrameSize >= 0 {
		s.predictPos -= FrameSize
	}
	copy(s.pcm[:PLCBufSize-FrameSize], s.pcm[FrameSize:])
}

func (s *State) finishUpdatedFrame() {
	s.lossCount = 0
	s.blend = 0
}

func maxF32(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

func quantizePCMInt16LikeInPlace(frame []float32) {
	for i := range frame {
		frame[i] = quantizePCMInt16Like(frame[i])
	}
}

func quantizePCMUpdateFloat(sample float32) float32 {
	return float32(float32ToOpusInt16(sample)) * (1.0 / 32768.0)
}

func float32ToOpusInt16(sample float32) int16 {
	return opusmath.Float32ToInt16(sample)
}

func quantizePCMInt16Like(sample float32) float32 {
	v := sample * 32768
	if v < -32767 {
		v = -32767
	}
	if v > 32767 {
		v = 32767
	}
	v = float32(opusmath.FloorHalfPlusF32ToInt32(v))
	return v * (1.0 / 32768.0)
}
