//go:build gopus_dred || gopus_osce

package gopus

import (
	"github.com/thesyncim/gopus/internal/dnnblob"
	internaldred "github.com/thesyncim/gopus/internal/dred"
	"github.com/thesyncim/gopus/internal/dred/rdovae"
	"github.com/thesyncim/gopus/internal/lpcnetplc"
	"github.com/thesyncim/gopus/internal/silk"
)

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

func (d *Decoder) dredPayloadState() *decoderDREDPayloadState {
	if s := d.dredState(); s != nil {
		return s.decoderDREDPayloadState
	}
	return nil
}

func (d *Decoder) dredRecoveryState() *decoderDREDRecoveryState {
	if s := d.dredState(); s != nil {
		return s.decoderDREDRecoveryState
	}
	return nil
}

func (d *Decoder) dredNeuralState() *decoderDREDNeuralState {
	if s := d.dredState(); s != nil {
		return s.decoderDREDNeuralState
	}
	return nil
}

func (d *Decoder) dred48kBridgeState() *decoderDRED48kBridgeState {
	if s := d.dredState(); s != nil {
		return s.decoderDRED48kBridgeState
	}
	return nil
}

func (d *Decoder) ensureDREDPayloadState() *decoderDREDPayloadState {
	s := d.ensureDREDState()
	if s == nil {
		return nil
	}
	if s.decoderDREDPayloadState == nil {
		s.decoderDREDPayloadState = &decoderDREDPayloadState{}
	}
	return s.decoderDREDPayloadState
}

func (d *Decoder) ensureDREDRecoveryState() *decoderDREDRecoveryState {
	s := d.ensureDREDState()
	if s == nil {
		return nil
	}
	if s.decoderDREDRecoveryState == nil {
		s.decoderDREDRecoveryState = &decoderDREDRecoveryState{}
	}
	return s.decoderDREDRecoveryState
}

func (d *Decoder) ensureDREDNeuralState() *decoderDREDNeuralState {
	s := d.ensureDREDState()
	if s == nil {
		return nil
	}
	if s.decoderDREDNeuralState == nil {
		s.decoderDREDNeuralState = &decoderDREDNeuralState{}
	}
	return s.decoderDREDNeuralState
}

func (d *Decoder) ensureDRED48kBridgeState() *decoderDRED48kBridgeState {
	s := d.ensureDREDState()
	if s == nil {
		return nil
	}
	if s.decoderDRED48kBridgeState == nil {
		s.decoderDRED48kBridgeState = &decoderDRED48kBridgeState{}
	}
	return s.decoderDRED48kBridgeState
}

func (d *Decoder) dredNeuralModelsLoaded() bool {
	if d == nil {
		return false
	}
	return d.pitchDNNLoaded || d.plcModelLoaded || d.farganModelLoaded
}

func (d *Decoder) dredDecodeSidecarPossible() bool {
	return d != nil && (d.dredPayloadScannerActive() || d.dredCachedPayloadActive() || d.dredGoodPacketMarkerActive())
}

func (d *Decoder) dredNeuralRuntimeLoaded() bool {
	n := d.dredNeuralState()
	return n != nil &&
		(!d.pitchDNNLoaded || (n.pitchDNNLoaded && n.dredAnalysis.Loaded())) &&
		(!d.plcModelLoaded || (n.plcModelLoaded && n.dredPredictor.Loaded())) &&
		(!d.farganModelLoaded || (n.farganModelLoaded && n.dredFARGAN.Loaded()))
}

func (d *Decoder) resetDREDNeuralRuntime() {
	n := d.dredNeuralState()
	if n == nil {
		return
	}
	n.dredRawHistoryUpdated = false
	n.pitchDNNLoaded = false
	n.plcModelLoaded = false
	n.farganModelLoaded = false
	n.dredAnalysis.Reset()
	n.dredPredictor.Reset()
	n.dredFARGAN.Reset()
}

func (d *Decoder) ensureDREDNeuralRuntimeLoaded() bool {
	if d == nil || !d.dredNeuralModelsLoaded() || d.dnnBlob == nil {
		return false
	}
	if d.dredNeuralRuntimeLoaded() {
		return true
	}
	var (
		analysis  lpcnetplc.Analysis
		predictor lpcnetplc.Predictor
		fargan    lpcnetplc.FARGAN
	)
	if d.pitchDNNLoaded {
		if err := analysis.SetModel(d.dnnBlob); err != nil {
			return false
		}
	}
	if d.plcModelLoaded {
		if err := predictor.SetModel(d.dnnBlob); err != nil {
			return false
		}
	}
	if d.farganModelLoaded {
		if err := fargan.SetModel(d.dnnBlob); err != nil {
			return false
		}
	}
	n := d.ensureDREDNeuralState()
	if n == nil {
		return false
	}
	n.pitchDNNLoaded = d.pitchDNNLoaded && analysis.Loaded()
	n.plcModelLoaded = d.plcModelLoaded && predictor.Loaded()
	n.farganModelLoaded = d.farganModelLoaded && fargan.Loaded()
	n.dredAnalysis = analysis
	n.dredPredictor = predictor
	n.dredFARGAN = fargan
	return true
}

func (d *Decoder) dropDREDPayloadStateIfDormant() {
	if d == nil {
		return
	}
	p := d.dredPayloadState()
	if p == nil {
		return
	}
	if p.dredDNNBlob == nil && !p.dredModelLoaded {
		d.dred.decoderDREDPayloadState = nil
	}
	d.maybeDropDREDState()
}

func (d *Decoder) dropDREDRecoveryStateIfDormant() {
	if d == nil {
		return
	}
	r := d.dredRecoveryState()
	if r == nil {
		return
	}
	if (d.dredPayloadState() == nil || !d.dredPayloadState().dredModelLoaded) && !d.dredNeuralModelsLoaded() {
		d.dred.decoderDREDRecoveryState = nil
	}
	d.maybeDropDREDState()
}

func (d *Decoder) dropDREDNeuralStateIfDormant() {
	if d == nil {
		return
	}
	n := d.dredNeuralState()
	if n == nil {
		return
	}
	if !n.pitchDNNLoaded && !n.plcModelLoaded && !n.farganModelLoaded {
		d.dred.decoderDREDNeuralState = nil
	}
	d.dropDRED48kBridgeStateIfDormant()
	d.dropDREDRecoveryStateIfDormant()
	d.maybeDropDREDState()
}

func (d *Decoder) dropDRED48kBridgeStateIfDormant() {
	if d == nil {
		return
	}
	b := d.dred48kBridgeState()
	if b == nil {
		return
	}
	if !d.dredNeuralModelsLoaded() {
		d.dred.decoderDRED48kBridgeState = nil
	}
	d.maybeDropDREDState()
}

func (d *Decoder) maybeDropDREDState() {
	if d == nil || d.dred == nil {
		return
	}
	if d.dred.decoderDREDPayloadState == nil &&
		d.dred.decoderDREDRecoveryState == nil &&
		d.dred.decoderDREDNeuralState == nil &&
		d.dred.decoderDRED48kBridgeState == nil {
		d.dred = nil
	}
}

func (d *Decoder) resetDREDRuntimeState() {
	if s := d.dredState(); s != nil {
		if p := s.decoderDREDPayloadState; p != nil {
			p.dredData = nil
			p.dredProcess = rdovae.Processor{}
		}
		s.decoderDREDRecoveryState = nil
		s.decoderDREDNeuralState = nil
		s.decoderDRED48kBridgeState = nil
		d.maybeDropDREDState()
	}
}

func (d *Decoder) dredNeuralConfigEligible() bool {
	if d == nil || d.channels < 1 || d.channels > 2 {
		return false
	}
	switch d.sampleRate {
	case 8000, 12000, 16000, 24000, 48000:
		return true
	default:
		return false
	}
}

func (d *Decoder) dredRuntimeSampleRate() int {
	if d == nil {
		return 0
	}
	return int(d.sampleRate)
}

// setDNNBlob mirrors the main libopus decoder OPUS_SET_DNN_BLOB surface.
func (d *Decoder) setDNNBlob(blob *dnnblob.Blob) error {
	var (
		models    dnnblob.DecoderModelState
		analysis  lpcnetplc.Analysis
		predictor lpcnetplc.Predictor
		fargan    lpcnetplc.FARGAN
	)
	if blob != nil {
		models = blob.DecoderModels()
		if models.PitchDNN {
			if err := analysis.SetModel(blob); err != nil {
				return err
			}
		}
		if models.PLC {
			if err := predictor.SetModel(blob); err != nil {
				return err
			}
		}
		if models.FARGAN {
			if err := fargan.SetModel(blob); err != nil {
				return err
			}
		}
	}

	d.dnnBlob = blob
	d.pitchDNNLoaded = models.PitchDNN
	d.plcModelLoaded = models.PLC
	d.farganModelLoaded = models.FARGAN
	d.setOSCEModelState(models)
	// Bind the extra-control OSCE BWE model when its weights are present. The
	// helper is a no-op outside of `gopus_osce` builds so the
	// shared DRED path remains untouched.
	if err := d.bindOSCEBWEModel(blob, models.OSCEBWE); err != nil {
		return err
	}
	// Bind the extra-control OSCE LACE/NoLACE postfilter models when their
	// weights are present. Same optional-model pattern as bindOSCEBWEModel.
	if err := d.bindOSCELACEModel(blob, models.OSCE); err != nil {
		return err
	}

	n := d.dredNeuralState()
	if !models.PitchDNN && !models.PLC && !models.FARGAN {
		if n != nil {
			d.resetDREDNeuralRuntime()
			d.resetDRED48kNeuralBridge()
			d.dropDREDNeuralStateIfDormant()
		}
		return nil
	}

	if !d.dredNeuralConfigEligible() {
		if n != nil {
			d.resetDREDNeuralRuntime()
			d.resetDRED48kNeuralBridge()
			d.dropDREDNeuralStateIfDormant()
		}
		return nil
	}

	if n != nil {
		if models.PitchDNN {
			if err := n.dredAnalysis.SetModelPreservingState(blob); err != nil {
				return err
			}
		} else {
			n.dredAnalysis = lpcnetplc.Analysis{}
		}
		if models.PLC {
			if err := n.dredPredictor.SetModelPreservingState(blob); err != nil {
				return err
			}
		} else {
			n.dredPredictor = lpcnetplc.Predictor{}
		}
		if models.FARGAN {
			if err := n.dredFARGAN.SetModelPreservingState(blob); err != nil {
				return err
			}
		} else {
			n.dredFARGAN = lpcnetplc.FARGAN{}
		}
		n.pitchDNNLoaded = models.PitchDNN && n.dredAnalysis.Loaded()
		n.plcModelLoaded = models.PLC && n.dredPredictor.Loaded()
		n.farganModelLoaded = models.FARGAN && n.dredFARGAN.Loaded()
	}
	return nil
}

// setDREDDecoderBlob mirrors the standalone libopus OpusDREDDecoder
// OPUS_SET_DNN_BLOB path.
func (d *Decoder) setDREDDecoderBlob(blob *dnnblob.Blob) {
	if d == nil {
		return
	}

	p := d.dredPayloadState()
	if blob == nil {
		if p != nil {
			p.dredDNNBlob = nil
			p.dredModel = nil
			p.dredModelLoaded = false
			p.dredProcess = rdovae.Processor{}
			p.dredData = nil
			d.clearDREDPayloadState()
			d.resetDRED48kNeuralBridge()
			d.dropDREDPayloadStateIfDormant()
			d.dropDREDRecoveryStateIfDormant()
		}
		return
	}

	p = d.ensureDREDPayloadState()
	p.dredDNNBlob = blob
	p.dredModel = nil
	p.dredModelLoaded = false
	if blob.SupportsDREDDecoder() {
		if model, err := rdovae.LoadDecoder(blob); err == nil {
			p.dredModel = model
			p.dredModelLoaded = true
		}
	}
	if !p.dredModelLoaded {
		d.clearDREDPayloadState()
		p.dredData = nil
		p.dredProcess = rdovae.Processor{}
		d.resetDRED48kNeuralBridge()
		d.dropDREDPayloadStateIfDormant()
		d.dropDREDRecoveryStateIfDormant()
	}
}

func (d *Decoder) ensureDREDPayloadBuffer() {
	p := d.dredPayloadState()
	if p == nil || len(p.dredData) >= internaldred.MaxDataSize {
		return
	}
	p.dredData = make([]byte, internaldred.MaxDataSize)
}

func (d *Decoder) clearDREDPayloadState() {
	p := d.dredPayloadState()
	r := d.dredRecoveryState()
	if p == nil && r == nil {
		return
	}
	if p != nil {
		p.dredCache.Clear()
		p.dredDecoded.Clear()
	}
	if r != nil {
		r.dredPLC.FECClear()
		r.dredBlend = r.dredPLC.Blend()
		r.dredRecovery = 0
	}
	d.resetDRED48kNeuralBridge()
}

func (d *Decoder) invalidateDREDPayloadState() {
	p := d.dredPayloadState()
	r := d.dredRecoveryState()
	if p == nil && r == nil {
		return
	}
	if p != nil {
		p.dredCache.Invalidate()
		p.dredDecoded.Invalidate()
	}
	if r != nil {
		r.dredBlend = max(r.dredBlend, r.dredPLC.Blend())
		r.dredRecovery = 0
	}
}

func (d *Decoder) resetDRED48kNeuralBridge() {
	b := d.dred48kBridgeState()
	if b == nil {
		return
	}
	b.dredPLCFill = 0
	b.dredPLCPreemphMem = 0
	b.dredLastNeural = false
}

func (d *Decoder) dredSidecarActive() bool {
	p := d.dredPayloadState()
	r := d.dredRecoveryState()
	b := d.dred48kBridgeState()
	return (p != nil && p.dredModelLoaded) || r != nil || b != nil
}

func (d *Decoder) dredPayloadScannerActive() bool {
	p := d.dredPayloadState()
	return p != nil && p.dredModelLoaded && !d.ignoreExtensions
}

func (d *Decoder) dredCachedPayloadActive() bool {
	p := d.dredPayloadState()
	return p != nil && p.dredModelLoaded && !d.ignoreExtensions && !p.dredCache.Empty()
}

func (d *Decoder) dredGoodPacketMarkerActive() bool {
	return d.dredRecoveryState() != nil || d.dred48kBridgeState() != nil
}

func (d *Decoder) dredNeuralConcealmentReady() bool {
	return d.ensureDREDNeuralConcealmentRuntime()
}

func (d *Decoder) dredNeuralConcealmentAvailable() bool {
	if !d.dredNeuralConfigEligible() {
		return false
	}
	return d.pitchDNNLoaded && d.plcModelLoaded && d.farganModelLoaded
}

func (d *Decoder) ensureDREDNeuralConcealmentRuntime() bool {
	if !d.dredNeuralConcealmentAvailable() {
		return false
	}
	if !d.ensureDREDNeuralRuntimeLoaded() {
		return false
	}
	r := d.ensureDREDRecoveryState()
	if r == nil {
		return false
	}
	if d.ensureDRED48kBridgeState() == nil {
		return false
	}
	return true
}

func (d *Decoder) maybeCacheDREDPayload(packet []byte) {
	p := d.dredPayloadState()
	if p == nil || !p.dredModelLoaded || d.ignoreExtensions || len(packet) == 0 {
		return
	}
	payload, frameOffset, ok, err := findDREDPayload(packet)
	if err != nil || !ok {
		return
	}
	d.ensureDREDPayloadBuffer()
	if len(payload) > len(p.dredData) {
		return
	}
	r := d.ensureDREDRecoveryState()
	if r != nil {
		r.dredBlend = max(r.dredBlend, r.dredPLC.Blend())
	}
	if err := p.dredCache.Store(p.dredData, payload, frameOffset); err != nil {
		return
	}
	minFeatureFrames := 2 * internaldred.NumRedundancyFrames
	if _, err := p.dredDecoded.Decode(payload, frameOffset, minFeatureFrames); err != nil {
		d.invalidateDREDPayloadState()
		return
	}
	p.dredModel.DecodeAllWithProcessor(&p.dredProcess, p.dredDecoded.Features[:], p.dredDecoded.State[:], p.dredDecoded.Latents[:], p.dredDecoded.NbLatents)
}

func (d *Decoder) cachedDREDMaxAvailableSamples(maxDredSamples int) int {
	return d.cachedDREDResult(maxDredSamples).MaxAvailableSamples()
}

func (d *Decoder) cachedDREDResult(maxDredSamples int) internaldred.Result {
	p := d.dredPayloadState()
	if p == nil || p.dredCache.Empty() || !p.dredModelLoaded || d.ignoreExtensions {
		return internaldred.Result{}
	}
	sampleRate := d.dredRuntimeSampleRate()
	if sampleRate <= 0 {
		return internaldred.Result{}
	}
	return p.dredCache.Result(internaldred.Request{
		MaxDREDSamples: maxDredSamples,
		SampleRate:     sampleRate,
	})
}

func (d *Decoder) maxCachedDREDSamples() int {
	if d == nil {
		return 0
	}
	maxDredSamples := d.maxPacketSamples
	if maxDredSamples <= 0 {
		maxDredSamples = defaultMaxPacketSamples
	}
	sampleRate := int(d.sampleRate)
	if sampleRate > 0 && sampleRate != 48000 && maxDredSamples == defaultMaxPacketSamples {
		if scaled := maxDredSamples * sampleRate / 48000; scaled > 0 {
			return scaled
		}
	}
	return maxDredSamples
}

func (d *Decoder) cachedDREDFeatureWindow(maxDredSamples, decodeOffsetSamples, frameSizeSamples, initFrames int) internaldred.FeatureWindow {
	p := d.dredPayloadState()
	if p == nil {
		return internaldred.FeatureWindow{}
	}
	result := d.cachedDREDResult(maxDredSamples)
	return internaldred.ProcessedFeatureWindow(result, &p.dredDecoded, decodeOffsetSamples, frameSizeSamples, initFrames)
}

func (d *Decoder) cachedDREDRecoveryWindow(maxDredSamples, decodeOffsetSamples, frameSizeSamples int) internaldred.FeatureWindow {
	r := d.dredRecoveryState()
	if r == nil {
		return internaldred.FeatureWindow{}
	}
	initFrames := 0
	if r.dredBlend == 0 {
		initFrames = 2
	}
	return d.cachedDREDFeatureWindow(maxDredSamples, decodeOffsetSamples, frameSizeSamples, initFrames)
}

func (d *Decoder) queueCachedDREDRecovery(maxDredSamples, decodeOffsetSamples, frameSizeSamples int) internaldred.FeatureWindow {
	p := d.dredPayloadState()
	r := d.dredRecoveryState()
	if p == nil || r == nil {
		return internaldred.FeatureWindow{}
	}
	initFrames := 0
	if r.dredBlend == 0 {
		initFrames = 2
	}
	return internaldred.QueueProcessedFeaturesWithInitFrames(&r.dredPLC, d.cachedDREDResult(maxDredSamples), &p.dredDecoded, decodeOffsetSamples, frameSizeSamples, initFrames)
}

func (d *Decoder) finishActiveDREDRecovery(frameSizeSamples int) {
	r := d.dredRecoveryState()
	if r == nil || frameSizeSamples <= 0 {
		return
	}
	r.dredBlend = max(r.dredBlend, r.dredPLC.Blend())
	r.dredRecovery += frameSizeSamples
}

func (d *Decoder) hybridDREDLowbandEligible() bool {
	return d != nil && d.silkDecoder != nil && d.channels >= 1 && d.channels <= 2 && d.dredNeuralConfigEligible()
}

func (d *Decoder) silkDREDLowbandHookEligible() bool {
	return d != nil && d.silkDecoder != nil && d.channels >= 1 && d.channels <= 2 && d.dredNeuralConfigEligible()
}

func (d *Decoder) hybridDREDLowbandSamples(frameSizeSamples int) (int, bool) {
	if !d.hybridDREDLowbandEligible() || frameSizeSamples <= 0 {
		return 0, false
	}
	nativeSamples := frameSizeSamples * 16000
	sampleRate := int(d.sampleRate)
	if sampleRate <= 0 || nativeSamples%sampleRate != 0 {
		return 0, false
	}
	return nativeSamples / sampleRate, true
}

func (d *Decoder) beginHybridDREDLowbandHook() (cleanup func(), used func() bool) {
	if !d.ensureDREDNeuralConcealmentRuntime() {
		return func() {}, func() bool { return false }
	}
	r := d.dredRecoveryState()
	n := d.dredNeuralState()
	if !d.silkDREDLowbandHookEligible() || r == nil || n == nil {
		return func() {}, func() bool { return false }
	}
	directUsed := false
	d.silkDecoder.SetDeepPLCLossMonoHook(func(concealed []float32) (bool, int) {
		if len(concealed) < lpcnetplc.FrameSize || len(concealed)%lpcnetplc.FrameSize != 0 {
			return false, 0
		}
		if !d.generateDREDNeuralFrames16k(concealed, len(concealed)) {
			return false, 0
		}
		directUsed = true
		return true, 0
	})
	return func() {
			d.silkDecoder.SetDeepPLCLossMonoHook(nil)
		}, func() bool {
			return directUsed
		}
}

func (d *Decoder) markDREDConcealed() {
	r := d.dredRecoveryState()
	if r == nil || !d.dredSidecarActive() {
		return
	}
	d.resetDRED48kNeuralBridge()
	r.dredPLC.MarkConcealed()
	r.dredBlend = max(r.dredBlend, r.dredPLC.Blend())
}

func (d *Decoder) updateDREDPCMHistory(frames []float32) {
	if !d.ensureDREDNeuralConcealmentRuntime() {
		return
	}
	r := d.dredRecoveryState()
	if r == nil || len(frames) < lpcnetplc.FrameSize {
		return
	}
	for offset := 0; offset+lpcnetplc.FrameSize <= len(frames); offset += lpcnetplc.FrameSize {
		r.dredPLC.MarkUpdatedFrameFloat(frames[offset : offset+lpcnetplc.FrameSize])
	}
}

func (d *Decoder) updateDREDPCMHistoryInt16(frames []int16) {
	if !d.ensureDREDNeuralConcealmentRuntime() {
		return
	}
	r := d.dredRecoveryState()
	n := d.dredNeuralState()
	if r == nil || n == nil || len(frames) < lpcnetplc.FrameSize {
		return
	}
	for offset := 0; offset+lpcnetplc.FrameSize <= len(frames); offset += lpcnetplc.FrameSize {
		r.dredPLC.MarkUpdatedFrameInt16(frames[offset : offset+lpcnetplc.FrameSize])
		n.dredRawHistoryUpdated = true
	}
}

func (d *Decoder) primeDREDPCMHistoryInt16(frames []int16) {
	if !d.ensureDREDNeuralConcealmentRuntime() {
		return
	}
	r := d.dredRecoveryState()
	n := d.dredNeuralState()
	if r == nil || n == nil || len(frames) < lpcnetplc.FrameSize {
		return
	}
	for offset := 0; offset+lpcnetplc.FrameSize <= len(frames); offset += lpcnetplc.FrameSize {
		r.dredPLC.MarkUpdatedFrameInt16(frames[offset : offset+lpcnetplc.FrameSize])
		n.dredRawHistoryUpdated = true
	}
}

func (d *Decoder) recordDREDRawMonoGoodFrame(samples []int16) {
	if d == nil || len(samples) < lpcnetplc.FrameSize {
		return
	}
	d.primeDREDPCMHistoryInt16(samples)
}

func (d *Decoder) beginDREDRawMonoGoodFrameCapture(mode Mode) func() {
	if d == nil || d.silkDecoder == nil || d.channels < 1 || d.channels > 2 {
		return nil
	}
	if mode != ModeHybrid && mode != ModeSILK {
		return nil
	}
	p := d.dredPayloadState()
	if d.dredRecoveryState() == nil && (p == nil || !p.dredModelLoaded) {
		return func() {}
	}
	if !d.ensureDREDNeuralConcealmentRuntime() {
		return nil
	}
	if n := d.dredNeuralState(); n != nil {
		n.dredRawHistoryUpdated = false
	}
	d.silkDecoder.SetRawMonoFrameHook(d.recordDREDRawMonoGoodFrame)
	return func() {
		d.silkDecoder.SetRawMonoFrameHook(nil)
	}
}

func (d *Decoder) refreshDREDHistoryFromHybridDecoder(samplesPerChannel int) bool {
	if !d.ensureDREDNeuralConcealmentRuntime() {
		return false
	}
	nativeSamples, ok := d.hybridDREDLowbandSamples(samplesPerChannel)
	if !ok {
		return false
	}
	n := d.dredNeuralState()
	if n == nil {
		return false
	}
	if nativeSamples < lpcnetplc.FrameSize || nativeSamples > len(n.dredPLCUpdate) || nativeSamples%lpcnetplc.FrameSize != 0 {
		return false
	}
	if got := d.silkDecoder.FillMonoOutBufTailFloat(n.dredPLCUpdate[:nativeSamples], nativeSamples); got != nativeSamples {
		return false
	}
	d.updateDREDPCMHistory(n.dredPLCUpdate[:nativeSamples])
	n.dredRawHistoryUpdated = true
	return true
}

// refreshDREDHistoryFromSILKDecoder seeds the LPCNet/FARGAN entry history from
// the SILK-only native int16 lowband produced by the most recent SILK decode.
// Mirrors refreshDREDHistoryFromHybridDecoder but pulls the full native mono
// output via silk.Decoder.LatestNativeMono() instead of the Hybrid lowband
// snapshot. The DRED neural concealment runs at 16 kHz, so we require the
// native rate to be 16 kHz (SILK WB).
//
// For stereo decoders, the LPCNet/FARGAN entry history remains mono. libopus
// passes the single lpcnet_state only to SILK channel 0 (`n == 0 ? lpcnet :
// NULL` in dec_API.c), so use the native internal mid/channel-0 samples rather
// than post-MS->LR left/right output.
func (d *Decoder) refreshDREDHistoryFromSILKDecoder() bool {
	if !d.ensureDREDNeuralConcealmentRuntime() {
		return false
	}
	if d == nil || d.silkDecoder == nil {
		return false
	}
	n := d.dredNeuralState()
	if n == nil {
		return false
	}
	var (
		nativeLen int
		fsKHz     int
		native    []int16
	)
	if d.channels == 2 {
		var kHz int
		native, kHz = d.silkDecoder.LatestNativeMid()
		if native == nil || kHz != 16 {
			native, kHz = d.silkDecoder.LatestNativeMono()
		}
		if native == nil || kHz != 16 {
			return false
		}
		nativeLen = len(native)
		fsKHz = kHz
	} else {
		var kHz int
		native, kHz = d.silkDecoder.LatestNativeMono()
		if native == nil || kHz != 16 {
			return false
		}
		nativeLen = len(native)
		fsKHz = kHz
	}
	if fsKHz != 16 || nativeLen <= 0 {
		return false
	}
	// DRED runs at 16 kHz; clamp to whole lpcnetplc 10 ms frames and to the
	// retained dredPLCUpdate scratch capacity.
	usable := nativeLen - (nativeLen % lpcnetplc.FrameSize)
	if usable > len(n.dredPLCUpdate) {
		usable = len(n.dredPLCUpdate) - (len(n.dredPLCUpdate) % lpcnetplc.FrameSize)
	}
	if usable < lpcnetplc.FrameSize {
		return false
	}
	start := nativeLen - usable
	d.updateDREDPCMHistoryInt16(native[start : start+usable])
	n.dredRawHistoryUpdated = true
	return true
}

func (d *Decoder) primeDREDCELTEntryHistory(mode Mode, primeAnalysis bool) int {
	if !d.ensureDREDNeuralConcealmentRuntime() {
		return 0
	}
	r := d.dredRecoveryState()
	neural := d.dredNeuralState()
	if r == nil || neural == nil || r.dredPLC.Blend() != 0 || d.celtDecoder == nil {
		return 0
	}
	if mode != ModeCELT && mode != ModeHybrid {
		return 0
	}
	b := d.ensureDRED48kBridgeState()
	var preemphMem float32
	samples, preemphMem := d.celtDecoder.FillPLCUpdate16kMonoWithPreemphasisMem(neural.dredPLCUpdate[:])
	if b != nil {
		b.dredPLCPreemphMem = preemphMem
	}
	if samples == 0 {
		return 0
	}
	total := 0
	for offset := 0; offset+lpcnetplc.FrameSize <= samples; offset += lpcnetplc.FrameSize {
		total += r.dredPLC.MarkUpdatedFrameFloat(neural.dredPLCUpdate[offset : offset+lpcnetplc.FrameSize])
	}
	if primeAnalysis && total > 0 {
		neural.dredAnalysis.Reset()
		if got := neural.dredAnalysis.PrimeHistoryFramesFloat(neural.dredPLCUpdate[:total]); got != total {
			return 0
		}
	}
	return total
}

func (d *Decoder) generateDREDNeuralFrames16k(dst []float32, samplesPerChannel int) bool {
	if !d.ensureDREDNeuralConcealmentRuntime() {
		return false
	}
	r := d.dredRecoveryState()
	n := d.dredNeuralState()
	if r == nil || n == nil || samplesPerChannel < lpcnetplc.FrameSize || samplesPerChannel%lpcnetplc.FrameSize != 0 {
		return false
	}
	if dst == nil {
		if samplesPerChannel > len(n.dredPLCUpdate) {
			return false
		}
		dst = n.dredPLCUpdate[:samplesPerChannel]
	} else if len(dst) < samplesPerChannel {
		return false
	}
	for offset := 0; offset+lpcnetplc.FrameSize <= samplesPerChannel; offset += lpcnetplc.FrameSize {
		frame := dst[offset : offset+lpcnetplc.FrameSize]
		if r.dredPLC.Blend() == 0 {
			if !r.dredPLC.GenerateConcealedFrameFloatWithAnalysis(&n.dredAnalysis, &n.dredPredictor, &n.dredFARGAN, frame) {
				return false
			}
		} else {
			if !r.dredPLC.GenerateConcealedFrameFloat(&n.dredPredictor, &n.dredFARGAN, frame) {
				return false
			}
		}
	}
	return true
}

func (d *Decoder) advanceHybridDREDLowbandState(frameSizeSamples int, lowbandSnapshot *silk.DeepPLCLowbandSnapshot) bool {
	if !d.ensureDREDNeuralConcealmentRuntime() {
		return false
	}
	r := d.dredRecoveryState()
	n := d.dredNeuralState()
	nativeSamples, ok := d.hybridDREDLowbandSamples(frameSizeSamples)
	if r == nil || n == nil || !ok {
		return false
	}
	if nativeSamples < lpcnetplc.FrameSize || nativeSamples > len(n.dredPLCUpdate) || nativeSamples%lpcnetplc.FrameSize != 0 {
		return false
	}
	if !d.generateDREDNeuralFrames16k(n.dredPLCUpdate[:nativeSamples], nativeSamples) {
		return false
	}
	if lowbandSnapshot != nil {
		d.silkDecoder.RestoreDeepPLCLowbandMono(lowbandSnapshot)
	}
	rendered := n.dredPLCRender[:nativeSamples]
	if d.silkDecoder.ApplyDeepPLCLossMono(n.dredPLCUpdate[:nativeSamples], rendered, n.dredFARGAN.LastPeriod()) != nativeSamples {
		return false
	}
	if lowbandSnapshot != nil {
		d.silkDecoder.AdvanceDeepPLCLowbandMono(rendered)
	}
	r.dredBlend = max(r.dredBlend, r.dredPLC.Blend())
	r.dredRecovery += frameSizeSamples
	return true
}

func (d *Decoder) markDREDUpdatedPCM(pcm []float32, samplesPerChannel int, mode Mode) {
	if !d.dredSidecarActive() {
		return
	}
	if b := d.dred48kBridgeState(); b != nil {
		b.dredLastNeural = false
	}
	r := d.dredRecoveryState()
	if !d.dredNeuralConcealmentAvailable() {
		if r != nil {
			r.dredPLC.ClearBlend()
		}
		return
	}
	if r == nil {
		return
	}
	r.dredPLC.ClearBlend()
	if mode == ModeSILK {
		n := d.dredNeuralState()
		if n != nil && n.dredRawHistoryUpdated {
			return
		}
		if d.refreshDREDHistoryFromSILKDecoder() {
			return
		}
		if d.sampleRate != 16000 || samplesPerChannel < lpcnetplc.FrameSize || samplesPerChannel%lpcnetplc.FrameSize != 0 {
			return
		}
		switch {
		case d.channels == 1 && len(pcm) >= samplesPerChannel:
			d.updateDREDPCMHistory(pcm[:samplesPerChannel])
		case d.channels == 2 && len(pcm) >= 2*samplesPerChannel:
			if n == nil || samplesPerChannel > len(n.dredPLCUpdate) {
				return
			}
			mono := n.dredPLCUpdate[:samplesPerChannel]
			for i := 0; i < samplesPerChannel; i++ {
				mono[i] = 0.5 * (pcm[2*i] + pcm[2*i+1])
			}
			d.updateDREDPCMHistory(mono)
		}
	}
}
