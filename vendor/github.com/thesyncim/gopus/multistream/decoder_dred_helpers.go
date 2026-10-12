//go:build gopus_dred || gopus_osce

package multistream

import (
	"github.com/thesyncim/gopus/internal/dnnblob"
	internaldred "github.com/thesyncim/gopus/internal/dred"
	"github.com/thesyncim/gopus/internal/dred/rdovae"
	"github.com/thesyncim/gopus/internal/lpcnetplc"
)

type decoderDREDState struct {
	dredDNNBlob     *dnnblob.Blob
	dredModel       *rdovae.Decoder
	dredModelLoaded bool
	dredData        [][]byte
	dredCache       []internaldred.Cache
	dredDecoded     []internaldred.Decoded
	dredProcesses   []rdovae.Processor
	dredPLC         []lpcnetplc.State
	dredRecovery    []int
	dredBlend       []int
	dredAnalysis    []lpcnetplc.Analysis
	dredPredictor   []lpcnetplc.Predictor
	dredFARGAN      []lpcnetplc.FARGAN
	dredBridge      []decoderDRED48kBridgeState
	dredPCM32       [][]float32
}

type decoderDRED48kBridgeState struct {
	dredPLCPCM        [4 * lpcnetplc.FrameSize]int16
	dredPLCUpdate     [4 * lpcnetplc.FrameSize]float32
	dredPLCFill       int
	dredPLCPreemphMem float32
	dredLastNeural    bool
}

func (d *Decoder) dredState() *decoderDREDState {
	if d == nil {
		return nil
	}
	return d.dred
}

func (d *Decoder) ensureDREDState() *decoderDREDState {
	if d == nil {
		return nil
	}
	if d.dred == nil {
		d.dred = &decoderDREDState{}
	}
	return d.dred
}

func (d *Decoder) maybeDropDREDState() {
	if d == nil || d.dred == nil {
		return
	}
	s := d.dred
	if s.dredDNNBlob == nil && !s.dredModelLoaded && len(s.dredCache) == 0 && len(s.dredPLC) == 0 {
		d.dred = nil
	}
}

// setDREDDecoderBlob mirrors the standalone libopus OpusDREDDecoder
// OPUS_SET_DNN_BLOB path.
func (d *Decoder) setDREDDecoderBlob(blob *dnnblob.Blob) {
	s := d.ensureDREDState()
	if s == nil {
		return
	}
	s.dredDNNBlob = blob
	s.dredModel = nil
	s.dredModelLoaded = false
	if blob != nil && blob.SupportsDREDDecoder() {
		if model, err := rdovae.LoadDecoder(blob); err == nil {
			s.dredModel = model
			s.dredModelLoaded = true
		}
	}
	if !s.dredModelLoaded {
		d.clearDREDPayloadState()
		clear(s.dredProcesses)
		if !d.dredNeuralConcealmentAvailable() {
			for i := range s.dredPLC {
				s.dredPLC[i].Reset()
			}
		}
		d.releaseDREDSidecar()
		d.maybeDropDREDState()
	}
}

func (d *Decoder) ensureDREDPLCState() *decoderDREDState {
	s := d.ensureDREDState()
	if s == nil {
		return nil
	}
	streams := len(d.decoders)
	if streams <= 0 {
		return s
	}
	if len(s.dredPLC) != streams {
		s.dredPLC = resizeDREDState(s.dredPLC, streams)
	}
	if len(s.dredRecovery) != streams {
		s.dredRecovery = resizeDREDState(s.dredRecovery, streams)
	}
	if len(s.dredBlend) != streams {
		s.dredBlend = resizeDREDState(s.dredBlend, streams)
	}
	if len(s.dredAnalysis) != streams {
		s.dredAnalysis = resizeDREDState(s.dredAnalysis, streams)
	}
	if len(s.dredPredictor) != streams {
		s.dredPredictor = resizeDREDState(s.dredPredictor, streams)
	}
	if len(s.dredFARGAN) != streams {
		s.dredFARGAN = resizeDREDState(s.dredFARGAN, streams)
	}
	return s
}

func resizeDREDState[T any](state []T, streams int) []T {
	if len(state) == streams {
		return state
	}
	resized := make([]T, streams)
	copy(resized, state)
	return resized
}

func (d *Decoder) ensureDRED48kState(s *decoderDREDState) {
	streams := len(d.decoders)
	if len(s.dredBridge) != streams {
		s.dredBridge = resizeDREDState(s.dredBridge, streams)
	}
	if len(s.dredPCM32) != streams {
		s.dredPCM32 = makeDREDPCM32Scratch(streams, d.coupledStreams)
	}
}

func (d *Decoder) ensureDREDSidecar() {
	s := d.ensureDREDPLCState()
	if s == nil || len(s.dredCache) != 0 {
		return
	}
	streams := len(d.decoders)
	if streams <= 0 {
		return
	}
	if len(s.dredDecoded) != streams {
		s.dredDecoded = resizeDREDState(s.dredDecoded, streams)
	}
	if len(s.dredProcesses) != streams {
		s.dredProcesses = resizeDREDState(s.dredProcesses, streams)
	}
	d.ensureDRED48kState(s)
	if len(s.dredData) != streams {
		s.dredData = makeDREDBuffers(streams)
	}
	s.dredCache = make([]internaldred.Cache, streams)
}

func (d *Decoder) releaseDREDSidecar() {
	s := d.dredState()
	if s == nil {
		return
	}
	s.dredDecoded = nil
	s.dredProcesses = nil
	s.dredData = nil
	s.dredCache = nil
	if !d.dredNeuralConcealmentAvailable() {
		s.dredPLC = nil
		s.dredRecovery = nil
		s.dredBlend = nil
		s.dredAnalysis = nil
		s.dredPredictor = nil
		s.dredFARGAN = nil
		s.dredBridge = nil
		s.dredPCM32 = nil
	}
}

func (d *Decoder) resetDREDRuntimeState() {
	s := d.dredState()
	d.clearRawSILKHistory()
	if s == nil {
		return
	}
	for i := range s.dredPLC {
		s.dredPLC[i].Reset()
	}
	for i := range s.dredAnalysis {
		s.dredAnalysis[i].Reset()
	}
	for i := range s.dredPredictor {
		s.dredPredictor[i].Reset()
	}
	for i := range s.dredFARGAN {
		s.dredFARGAN[i].Reset()
	}
	for i := range s.dredRecovery {
		s.dredRecovery[i] = 0
	}
	clear(s.dredBlend)
	for i := range s.dredBridge {
		s.dredBridge[i] = decoderDRED48kBridgeState{}
	}
}

func (d *Decoder) clearRawSILKHistory() {
	if d == nil {
		return
	}
	for stream := range d.rawSILKHistory {
		clear(d.rawSILKHistory[stream])
		d.rawSILKHistoryPos[stream] = 0
		d.rawSILKHistoryFill[stream] = 0
		d.pcmHistorySynced[stream] = false
		d.directRawCapture[stream] = false
	}
}

func makeDREDBuffers(streams int) [][]byte {
	if streams <= 0 {
		return nil
	}
	bufs := make([][]byte, streams)
	for i := range bufs {
		bufs[i] = make([]byte, internaldred.MaxDataSize)
	}
	return bufs
}

func makeDREDPCM32Scratch(streams, coupledStreams int) [][]float32 {
	if streams <= 0 {
		return nil
	}
	bufs := make([][]float32, streams)
	for i := range bufs {
		bufs[i] = make([]float32, maxOpusPacketDuration48*streamChannels(i, coupledStreams))
	}
	return bufs
}

func (d *Decoder) dredSidecarActive() bool {
	s := d.dredState()
	if s == nil {
		return false
	}
	for i := range s.dredCache {
		if !s.dredCache[i].Empty() {
			return true
		}
	}
	for i := range s.dredRecovery {
		if s.dredRecovery[i] != 0 {
			return true
		}
	}
	for i := range s.dredBridge {
		b := &s.dredBridge[i]
		if b.dredLastNeural || b.dredPLCFill != 0 || b.dredPLCPreemphMem != 0 {
			return true
		}
	}
	for i := range s.dredPLC {
		if s.dredPLC[i].FECFillPos() != 0 || s.dredPLC[i].FECSkip() != 0 {
			return true
		}
	}
	return false
}

func (d *Decoder) dredPayloadScannerActive() bool {
	s := d.dredState()
	return s != nil && s.dredModelLoaded && !d.ignoreExtensions
}

func (d *Decoder) clearDREDPayloadState() {
	s := d.dredState()
	if s == nil {
		return
	}
	for i := range s.dredCache {
		s.dredCache[i].Clear()
		s.dredDecoded[i].Clear()
		s.dredPLC[i].FECClear()
		if i < len(s.dredRecovery) {
			s.dredRecovery[i] = 0
		}
		s.dredBlend[i] = s.dredPLC[i].Blend()
	}
	for i := range s.dredBridge {
		s.dredBridge[i] = decoderDRED48kBridgeState{}
	}
}

func (d *Decoder) invalidateDREDPayloadState() {
	s := d.dredState()
	if s == nil || len(s.dredCache) == 0 {
		return
	}
	for i := range s.dredCache {
		s.dredCache[i].Invalidate()
		s.dredDecoded[i].Invalidate()
		if i < len(s.dredRecovery) {
			s.dredRecovery[i] = 0
		}
		s.dredBlend[i] = s.dredPLC[i].Blend()
	}
}

func (d *Decoder) maybeCacheDREDPayload(stream int, packet []byte) {
	s := d.dredState()
	if s == nil || !s.dredModelLoaded || d.ignoreExtensions || stream < 0 || len(packet) == 0 {
		return
	}
	payload, frameOffset, ok, err := findDREDPayload(packet)
	if err != nil || !ok {
		return
	}
	d.ensureDREDSidecar()
	s = d.dredState()
	if s == nil || stream >= len(s.dredData) || len(payload) > len(s.dredData[stream]) {
		return
	}
	s.dredBlend[stream] = s.dredPLC[stream].Blend()
	if err := s.dredCache[stream].Store(s.dredData[stream], payload, frameOffset); err != nil {
		return
	}
	minFeatureFrames := 2 * internaldred.NumRedundancyFrames
	if _, err := s.dredDecoded[stream].Decode(payload, frameOffset, minFeatureFrames); err != nil {
		s.dredCache[stream].Invalidate()
		s.dredDecoded[stream].Invalidate()
		s.dredPLC[stream].FECClear()
		return
	}
	s.dredModel.DecodeAllWithProcessor(&s.dredProcesses[stream], s.dredDecoded[stream].Features[:], s.dredDecoded[stream].State[:], s.dredDecoded[stream].Latents[:], s.dredDecoded[stream].NbLatents)
}

func (d *Decoder) markDREDUpdated(stream int) {
	s := d.dredState()
	if s == nil || len(s.dredPLC) == 0 || stream < 0 || stream >= len(s.dredPLC) {
		return
	}
	s.dredPLC[stream].MarkUpdated()
	if stream < len(s.dredRecovery) {
		s.dredRecovery[stream] = 0
	}
	if stream < len(s.dredBridge) {
		s.dredBridge[stream].dredLastNeural = false
	}
}

func (d *Decoder) markDREDConcealedAll() {
	s := d.dredState()
	if s == nil || len(s.dredPLC) == 0 {
		return
	}
	for i := range s.dredPLC {
		s.dredPLC[i].MarkConcealed()
	}
}

func (d *Decoder) cachedDREDMaxAvailableSamples(stream, maxDredSamples int) int {
	return d.cachedDREDResult(stream, maxDredSamples).MaxAvailableSamples()
}

func (d *Decoder) cachedDREDResult(stream, maxDredSamples int) internaldred.Result {
	s := d.dredState()
	if s == nil || stream < 0 || stream >= len(s.dredCache) || s.dredCache[stream].Empty() || !s.dredModelLoaded || d.ignoreExtensions {
		return internaldred.Result{}
	}
	return s.dredCache[stream].Result(internaldred.Request{
		MaxDREDSamples: maxDredSamples,
		SampleRate:     int(d.sampleRate),
	})
}

func (d *Decoder) cachedDREDFeatureWindow(stream, maxDredSamples, decodeOffsetSamples, frameSizeSamples, initFrames int) internaldred.FeatureWindow {
	s := d.dredState()
	if s == nil || stream < 0 || stream >= len(s.dredDecoded) {
		return internaldred.FeatureWindow{}
	}
	result := d.cachedDREDResult(stream, maxDredSamples)
	return internaldred.ProcessedFeatureWindow(result, &s.dredDecoded[stream], decodeOffsetSamples, frameSizeSamples, initFrames)
}

func (d *Decoder) cachedDREDRecoveryWindow(stream, maxDredSamples, decodeOffsetSamples, frameSizeSamples int) internaldred.FeatureWindow {
	s := d.dredState()
	if s == nil || stream < 0 || stream >= len(s.dredPLC) {
		return internaldred.FeatureWindow{}
	}
	initFrames := 0
	if s.dredBlend[stream] == 0 {
		initFrames = 2
	}
	return d.cachedDREDFeatureWindow(stream, maxDredSamples, decodeOffsetSamples, frameSizeSamples, initFrames)
}

func (d *Decoder) queueCachedDREDRecovery(stream, maxDredSamples, decodeOffsetSamples, frameSizeSamples int) internaldred.FeatureWindow {
	s := d.dredState()
	if s == nil || stream < 0 || stream >= len(s.dredDecoded) || stream >= len(s.dredPLC) {
		return internaldred.FeatureWindow{}
	}
	initFrames := 0
	if s.dredBlend[stream] == 0 {
		initFrames = 2
	}
	return internaldred.QueueProcessedFeaturesWithInitFrames(&s.dredPLC[stream], d.cachedDREDResult(stream, maxDredSamples), &s.dredDecoded[stream], decodeOffsetSamples, frameSizeSamples, initFrames)
}

func (d *Decoder) dredNeuralConcealmentAvailable() bool {
	if d == nil {
		return false
	}
	switch d.sampleRate {
	case 8000, 12000, 16000, 24000, 48000, 96000:
	default:
		return false
	}
	return d.dnnBlob != nil &&
		d.pitchDNNLoaded &&
		d.plcModelLoaded &&
		d.farganModelLoaded
}

func (d *Decoder) ensureDREDNeuralRuntime(stream int) bool {
	s := d.ensureDREDPLCState()
	if s == nil || stream < 0 || stream >= len(s.dredAnalysis) || stream >= len(s.dredPredictor) || stream >= len(s.dredFARGAN) {
		return false
	}
	if s.dredAnalysis[stream].Loaded() && s.dredPredictor[stream].Loaded() && s.dredFARGAN[stream].Loaded() {
		return true
	}
	var (
		analysis  lpcnetplc.Analysis
		predictor lpcnetplc.Predictor
		fargan    lpcnetplc.FARGAN
	)
	if err := analysis.SetModel(d.dnnBlob); err != nil {
		return false
	}
	if err := predictor.SetModel(d.dnnBlob); err != nil {
		return false
	}
	if err := fargan.SetModel(d.dnnBlob); err != nil {
		return false
	}
	s.dredAnalysis[stream] = analysis
	s.dredPredictor[stream] = predictor
	s.dredFARGAN[stream] = fargan
	return true
}

func (d *Decoder) bindDREDNeuralModels(blob *dnnblob.Blob, models dnnblob.DecoderModelState) {
	s := d.dredState()
	if s == nil {
		return
	}
	for i := range s.dredPLC {
		if models.PitchDNN {
			if s.dredAnalysis[i].Loaded() {
				_ = s.dredAnalysis[i].SetModelPreservingState(blob)
			} else {
				_ = s.dredAnalysis[i].SetModel(blob)
			}
		} else {
			s.dredAnalysis[i] = lpcnetplc.Analysis{}
		}
		if models.PLC {
			if s.dredPredictor[i].Loaded() {
				_ = s.dredPredictor[i].SetModelPreservingState(blob)
			} else {
				_ = s.dredPredictor[i].SetModel(blob)
			}
		} else {
			s.dredPredictor[i] = lpcnetplc.Predictor{}
			s.dredPLC[i].Reset()
		}
		if models.FARGAN {
			if s.dredFARGAN[i].Loaded() {
				_ = s.dredFARGAN[i].SetModelPreservingState(blob)
			} else {
				_ = s.dredFARGAN[i].SetModel(blob)
			}
		} else {
			s.dredFARGAN[i] = lpcnetplc.FARGAN{}
		}
	}
	if !models.PLC {
		clear(s.dredRecovery)
		clear(s.dredBlend)
		for i := range s.dredBridge {
			s.dredBridge[i] = decoderDRED48kBridgeState{}
		}
		for i := range d.directRawCapture {
			d.directRawCapture[i] = false
			if i < len(d.pcmHistorySynced) {
				d.pcmHistorySynced[i] = false
			}
		}
	}
}

func (d *Decoder) streamPacketHasDREDPayload(packet []byte) bool {
	if packet == nil || len(packet) == 0 || d.ignoreExtensions {
		return false
	}
	_, _, ok, err := findDREDPayload(packet)
	return err == nil && ok
}

func (d *Decoder) markDREDUpdatedPCMFrame(stream int, samples []int16) {
	s := d.dredState()
	if s == nil || stream < 0 || stream >= len(s.dredPLC) || len(samples) < lpcnetplc.FrameSize {
		return
	}
	usable := len(samples) - len(samples)%lpcnetplc.FrameSize
	for offset := 0; offset+lpcnetplc.FrameSize <= usable; offset += lpcnetplc.FrameSize {
		s.dredPLC[stream].MarkUpdatedFrameInt16(samples[offset : offset+lpcnetplc.FrameSize])
	}
}

func (d *Decoder) ensureDREDHookStorage(stream int) bool {
	if d == nil || stream < 0 || stream >= len(d.decoders) {
		return false
	}
	streams := len(d.decoders)
	if len(d.rawSILKFrameHooks) != streams {
		d.rawSILKHistoryPos = resizeDREDState(d.rawSILKHistoryPos, streams)
		d.rawSILKHistoryFill = resizeDREDState(d.rawSILKHistoryFill, streams)
		d.pcmHistorySynced = resizeDREDState(d.pcmHistorySynced, streams)
		d.directRawCapture = resizeDREDState(d.directRawCapture, streams)
		d.rawSILKHistory = resizeDREDState(d.rawSILKHistory, streams)
		d.rawSILKFrameHooks = resizeDREDState(d.rawSILKFrameHooks, streams)
		d.rawSILKLossHooks = resizeDREDState(d.rawSILKLossHooks, streams)
		d.deepPLCLossHooks = resizeDREDState(d.deepPLCLossHooks, streams)
		d.deepPLCHookUsed = resizeDREDState(d.deepPLCHookUsed, streams)
		d.dredGenerateHooks = resizeDREDState(d.dredGenerateHooks, streams)
	}
	return true
}

func (d *Decoder) ensureRawSILKHistory(stream int) bool {
	if !d.ensureDREDHookStorage(stream) {
		return false
	}
	if len(d.rawSILKHistory[stream]) != lpcnetplc.PLCBufSize {
		d.rawSILKHistory[stream] = make([]int16, lpcnetplc.PLCBufSize)
	}
	return d.ensureDREDCallbacks(stream)
}

func (d *Decoder) ensureDREDCallbacks(stream int) bool {
	if !d.ensureDREDHookStorage(stream) {
		return false
	}
	if d.rawSILKFrameHooks[stream] == nil {
		streamIndex := stream
		d.rawSILKFrameHooks[stream] = func(samples []int16) {
			d.recordRawSILKFrame(streamIndex, samples)
		}
	}
	if d.rawSILKLossHooks[stream] == nil {
		streamIndex := stream
		d.rawSILKLossHooks[stream] = func(samples []int16) {
			d.recordRawSILKFrame(streamIndex, samples)
		}
	}
	if d.deepPLCLossHooks[stream] == nil {
		streamIndex := stream
		d.deepPLCLossHooks[stream] = func(samples []float32) (bool, int) {
			if !d.generateDREDNeuralFrames16k(streamIndex, samples, len(samples)) {
				return false, 0
			}
			d.deepPLCHookUsed[streamIndex] = true
			return true, 0
		}
	}
	if d.dredGenerateHooks[stream] == nil {
		streamIndex := stream
		d.dredGenerateHooks[stream] = func(frame []float32) bool {
			return d.generateDREDPLCFrame(streamIndex, frame)
		}
	}
	return true
}

func (d *Decoder) generateDREDPLCFrame(stream int, frame []float32) bool {
	s := d.dredState()
	if s == nil || stream < 0 || stream >= len(s.dredPLC) || stream >= len(s.dredAnalysis) ||
		stream >= len(s.dredPredictor) || stream >= len(s.dredFARGAN) || len(frame) < lpcnetplc.FrameSize {
		return false
	}
	plc := &s.dredPLC[stream]
	if plc.Blend() == 0 {
		return plc.GenerateConcealedFrameFloatWithAnalysis(&s.dredAnalysis[stream], &s.dredPredictor[stream], &s.dredFARGAN[stream], frame[:lpcnetplc.FrameSize])
	}
	return plc.GenerateConcealedFrameFloat(&s.dredPredictor[stream], &s.dredFARGAN[stream], frame[:lpcnetplc.FrameSize])
}

func (d *Decoder) recordRawSILKFrame(stream int, samples []int16) {
	if d == nil || stream < 0 || stream >= len(d.rawSILKHistory) || len(samples) < lpcnetplc.FrameSize {
		return
	}
	history := d.rawSILKHistory[stream]
	capacity := len(history)
	if capacity < lpcnetplc.FrameSize {
		return
	}
	for offset := 0; offset+lpcnetplc.FrameSize <= len(samples); offset += lpcnetplc.FrameSize {
		copy(history[d.rawSILKHistoryPos[stream]:], samples[offset:offset+lpcnetplc.FrameSize])
		d.rawSILKHistoryPos[stream] = (d.rawSILKHistoryPos[stream] + lpcnetplc.FrameSize) % capacity
		if d.rawSILKHistoryFill[stream] < capacity {
			d.rawSILKHistoryFill[stream] += lpcnetplc.FrameSize
		}
	}
	if d.directRawCapture[stream] {
		d.markDREDUpdatedPCMFrame(stream, samples)
		if s := d.dredState(); s != nil && stream < len(s.dredPLC) {
			d.pcmHistorySynced[stream] = true
		}
	}
}

func (d *Decoder) replayRawSILKHistory(stream int) bool {
	if d == nil || stream < 0 || stream >= len(d.rawSILKHistory) || d.pcmHistorySynced[stream] {
		return false
	}
	fill := d.rawSILKHistoryFill[stream]
	if fill < lpcnetplc.FrameSize {
		return false
	}
	s := d.dredState()
	if s == nil || stream >= len(s.dredPLC) {
		return false
	}
	history := d.rawSILKHistory[stream]
	capacity := len(history)
	start := (d.rawSILKHistoryPos[stream] - fill + capacity) % capacity
	for offset := 0; offset < fill; offset += lpcnetplc.FrameSize {
		frameStart := (start + offset) % capacity
		s.dredPLC[stream].MarkUpdatedFrameInt16(history[frameStart : frameStart+lpcnetplc.FrameSize])
	}
	d.pcmHistorySynced[stream] = true
	return true
}

func (d *Decoder) beginDREDRawMonoFrameCapture(stream int, st *streamState, mode int, packet []byte) bool {
	if d == nil || st == nil || (mode != streamModeSILK && mode != streamModeHybrid) {
		return false
	}
	if !d.ensureRawSILKHistory(stream) {
		return false
	}
	if s := d.dredState(); s != nil && s.dredModelLoaded && d.streamPacketHasDREDPayload(packet) {
		d.ensureDREDSidecar()
	}
	d.directRawCapture[stream] = false
	if d.dredNeuralConcealmentAvailable() && d.ensureDREDNeuralRuntime(stream) {
		// Feed every loaded-model raw update into the child PLC runtime, as libopus does.
		d.replayRawSILKHistory(stream)
		d.directRawCapture[stream] = true
	}
	switch mode {
	case streamModeSILK:
		st.silkDec.SetRawMonoFrameHook(d.rawSILKFrameHooks[stream])
		if d.plcModelLoaded {
			st.silkDec.SetRawMonoLossFrameHook(d.rawSILKLossHooks[stream])
		} else {
			st.silkDec.SetRawMonoLossFrameHook(nil)
		}
	case streamModeHybrid:
		st.hybridDec.SetRawMonoFrameHook(d.rawSILKFrameHooks[stream])
		if d.plcModelLoaded {
			st.hybridDec.SetRawMonoLossFrameHook(d.rawSILKLossHooks[stream])
		} else {
			st.hybridDec.SetRawMonoLossFrameHook(nil)
		}
	default:
		return false
	}
	return true
}

func (d *Decoder) endDREDRawMonoFrameCapture(stream int, st *streamState) {
	if d == nil || st == nil {
		return
	}
	st.silkDec.SetRawMonoFrameHook(nil)
	st.silkDec.SetRawMonoLossFrameHook(nil)
	st.hybridDec.SetRawMonoFrameHook(nil)
	st.hybridDec.SetRawMonoLossFrameHook(nil)
	if stream >= 0 && stream < len(d.directRawCapture) {
		d.directRawCapture[stream] = false
	}
}

func (d *Decoder) prepareDRED48kNeuralEntry(stream, frameSize int, st *streamState) {
	s := d.dredState()
	if s == nil || st == nil || st.celtDec == nil || stream < 0 || stream >= len(s.dredPLC) || stream >= len(s.dredBridge) || frameSize <= 0 {
		return
	}
	plc := &s.dredPLC[stream]
	bridge := &s.dredBridge[stream]
	if !bridge.dredLastNeural && bridge.dredPLCFill == 0 && plc.FECFillPos() == 0 && plc.FECSkip() == 0 {
		plc.FECClear()
	}
	if st.celtDec.LastPLCFrameWasNeural() || plc.Blend() != 0 {
		return
	}
	if st.lastMode != streamModeCELT && st.lastMode != streamModeHybrid {
		return
	}
	samples, preemphMem := st.celtDec.FillPLCUpdate16kMonoWithPreemphasisMem(bridge.dredPLCUpdate[:])
	bridge.dredPLCPreemphMem = preemphMem
	for offset := 0; offset+lpcnetplc.FrameSize <= samples; offset += lpcnetplc.FrameSize {
		plc.MarkUpdatedFrameFloat(bridge.dredPLCUpdate[offset : offset+lpcnetplc.FrameSize])
	}
}

func (d *Decoder) generateDREDNeuralFrames16k(stream int, dst []float32, samplesPerChannel int) bool {
	s := d.dredState()
	if s == nil ||
		stream < 0 ||
		stream >= len(s.dredPLC) ||
		stream >= len(s.dredAnalysis) ||
		stream >= len(s.dredPredictor) ||
		stream >= len(s.dredFARGAN) ||
		samplesPerChannel < lpcnetplc.FrameSize ||
		samplesPerChannel%lpcnetplc.FrameSize != 0 ||
		len(dst) < samplesPerChannel ||
		!d.ensureDREDNeuralRuntime(stream) {
		return false
	}
	plc := &s.dredPLC[stream]
	analysis := &s.dredAnalysis[stream]
	predictor := &s.dredPredictor[stream]
	fargan := &s.dredFARGAN[stream]
	for offset := 0; offset+lpcnetplc.FrameSize <= samplesPerChannel; offset += lpcnetplc.FrameSize {
		frame := dst[offset : offset+lpcnetplc.FrameSize]
		if plc.Blend() == 0 {
			if !plc.GenerateConcealedFrameFloatWithAnalysis(analysis, predictor, fargan, frame) {
				return false
			}
			continue
		}
		if !plc.GenerateConcealedFrameFloat(predictor, fargan, frame) {
			return false
		}
	}
	return true
}

func (d *Decoder) decodeDREDSILKOrHybridPLCStream(stream, frameSize int, st *streamState) ([]float32, bool, error) {
	if st == nil || st.channels < 1 || st.channels > 2 || !d.ensureDREDCallbacks(stream) {
		return nil, false, nil
	}
	d.deepPLCHookUsed[stream] = false
	hook := d.deepPLCLossHooks[stream]

	var (
		decoded []float32
		err     error
	)
	switch st.lastMode {
	case streamModeSILK:
		st.silkDec.SetDeepPLCLossMonoHook(hook)
		decoded, err = st.finishDecode32(st.decodeSILKToFloat32(nil, frameSize, st.lastPacketStereo, int(st.lastBandwidth)))
		st.silkDec.SetDeepPLCLossMonoHook(nil)
	case streamModeHybrid:
		st.hybridDec.SetDeepPLCLossMonoHook(hook)
		decoded = st.framePCMFor(frameSize * int(st.channels))
		err = st.hybridDec.DecodePLCToFloat32WithPacketStereoInto(frameSize, st.lastPacketStereo, decoded)
		if err == nil {
			decoded, err = st.finishDecode32(decoded, nil)
		}
		st.hybridDec.SetDeepPLCLossMonoHook(nil)
	default:
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !d.deepPLCHookUsed[stream] {
		return nil, false, nil
	}
	return decoded, true, nil
}

func (d *Decoder) decodeDREDPLCStream(stream, frameSize int) ([]float32, bool, error) {
	s := d.dredState()
	if d == nil || stream < 0 || stream >= len(d.decoders) || !d.dredNeuralConcealmentAvailable() {
		return nil, false, nil
	}
	st, ok := d.decoders[stream].(*streamState)
	if !ok || st == nil || st.channels < 1 || st.channels > 2 {
		return nil, false, nil
	}
	hasSidecar := s != nil &&
		stream < len(s.dredCache) && !s.dredCache[stream].Empty() &&
		s.dredModelLoaded && !d.ignoreExtensions
	mainNeuralReady := st.complexity >= 5
	if !hasSidecar && !mainNeuralReady {
		return nil, false, nil
	}
	if st.lastMode == streamModeSILK || st.lastMode == streamModeHybrid {
		if !hasSidecar && st.silkDec.GetSampleRateKHz() != 16 {
			return nil, false, nil
		}
	}
	// celt_decode_lost selects neural PLC only outside its native 96 kHz mode.
	if st.lastMode == streamModeCELT && st.sampleRate == 96000 {
		return nil, false, nil
	}
	if !d.ensureDREDNeuralRuntime(stream) {
		return nil, false, nil
	}
	s = d.dredState()
	if s == nil || stream >= len(s.dredPLC) {
		return nil, false, nil
	}
	if st.lastMode == streamModeSILK || st.lastMode == streamModeHybrid {
		d.ensureRawSILKHistory(stream)
		d.replayRawSILKHistory(stream)
		decoded, ok, err := d.decodeDREDSILKOrHybridPLCStream(stream, frameSize, st)
		if err != nil || !ok {
			return decoded, ok, err
		}
		if hasSidecar && stream < len(s.dredRecovery) {
			s.dredRecovery[stream] += frameSize
		}
		st.recordDecodeCall(frameSize, 0)
		st.lastFinalRangeDataLen = 0
		return decoded, true, nil
	}
	if st.celtDec == nil || st.lastMode != streamModeCELT {
		return nil, false, nil
	}
	if !d.ensureDREDCallbacks(stream) {
		return nil, false, nil
	}
	d.ensureDRED48kState(s)
	if stream >= len(s.dredBridge) || stream >= len(s.dredPCM32) {
		return nil, false, nil
	}
	if frameSize <= 0 {
		frameSize = int(st.lastFrameSize)
	}
	if frameSize <= 0 {
		frameSize = 960
	}
	frameSize48 := frameSize
	downsample := 1
	sampleRate := int(st.sampleRate)
	if sampleRate != 48000 {
		frameSize48 = st.frameSize48FromAPI(frameSize)
		downsample = 48000 / sampleRate
	}
	samples := frameSize * int(st.channels)
	var out []float32
	if stream < len(s.dredPCM32) && len(s.dredPCM32[stream]) >= samples {
		out = s.dredPCM32[stream][:samples]
	} else {
		out = make([]float32, samples)
	}
	bridge := &s.dredBridge[stream]
	plc := &s.dredPLC[stream]

	d.prepareDRED48kNeuralEntry(stream, frameSize, st)
	generate := d.dredGenerateHooks[stream]

	var okConceal bool
	if plc.FECFillPos() > plc.FECReadPos() {
		okConceal = st.celtDec.ConcealDRED48kDownsampleToFloat32(out, frameSize48, downsample, &bridge.dredLastNeural, bridge.dredPLCPCM[:], &bridge.dredPLCFill, &bridge.dredPLCPreemphMem, generate)
	} else {
		okConceal = st.celtDec.ConcealPLCNeural48kDownsampleToFloat32(out, frameSize48, downsample, &bridge.dredLastNeural, bridge.dredPLCPCM[:], &bridge.dredPLCFill, &bridge.dredPLCPreemphMem, generate)
	}
	if !okConceal {
		return nil, false, nil
	}
	if hasSidecar && stream < len(s.dredRecovery) {
		s.dredRecovery[stream] += frameSize
	}
	st.recordDecodeCall(frameSize, 0)
	st.lastFinalRangeDataLen = 0
	st.applyOutputGain32(out)
	return out, true, nil
}
