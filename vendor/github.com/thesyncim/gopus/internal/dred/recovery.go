package dred

import "github.com/thesyncim/gopus/internal/lpcnetplc"

// ProcessedFeatureWindow mirrors the feature scheduling window that
// opus_decode_native() uses after opus_dred_process(), where the processed
// DRED object contributes its retained latent count.
func ProcessedFeatureWindow(result Result, decoded *Decoded, decodeOffsetSamples, frameSizeSamples, initFrames int) FeatureWindow {
	if decoded == nil {
		return result.FeatureWindow(decodeOffsetSamples, frameSizeSamples, initFrames)
	}
	window := result.FeatureWindow(decodeOffsetSamples, frameSizeSamples, initFrames)
	window.MaxFeatureIndex = 4*decoded.NbLatents - 1
	window.RecoverableFeatureFrames = 0
	window.MissingPositiveFrames = 0
	for i := 0; i < window.NeededFeatureFrames; i++ {
		featureOffset := window.FeatureOffsetBase - i
		if featureOffset < 0 {
			continue
		}
		if featureOffset <= window.MaxFeatureIndex {
			window.RecoverableFeatureFrames++
		} else {
			window.MissingPositiveFrames++
		}
	}
	return window
}

// QueueProcessedFeatures mirrors the standalone DRED scheduling loop inside
// opus_decode_native(). It clears the PLC FEC queue, derives the libopus
// recovery window from the retained blend state, and enqueues either concrete
// feature vectors or skipped-positive placeholders.
func QueueProcessedFeatures(plc *lpcnetplc.State, result Result, decoded *Decoded, decodeOffsetSamples, frameSizeSamples int) FeatureWindow {
	if plc == nil {
		return FeatureWindow{}
	}
	initFrames := 0
	if plc.Blend() == 0 {
		initFrames = 2
	}
	return QueueProcessedFeaturesWithInitFrames(plc, result, decoded, decodeOffsetSamples, frameSizeSamples, initFrames)
}

// QueueProcessedFeaturesWithInitFrames mirrors the DRED prefill loop in
// opus_decode_native() when the caller already knows which init-frame policy
// should apply for the packet being scheduled.
func QueueProcessedFeaturesWithInitFrames(plc *lpcnetplc.State, result Result, decoded *Decoded, decodeOffsetSamples, frameSizeSamples, initFrames int) FeatureWindow {
	if plc == nil || decoded == nil {
		return FeatureWindow{}
	}
	plc.FECClear()
	window := ProcessedFeatureWindow(result, decoded, decodeOffsetSamples, frameSizeSamples, initFrames)
	for i := 0; i < window.NeededFeatureFrames; i++ {
		featureOffset := window.FeatureOffsetBase - i
		if featureOffset < 0 {
			continue
		}
		if featureOffset <= window.MaxFeatureIndex {
			start := featureOffset * NumFeatures
			if start >= 0 && start+NumFeatures <= len(decoded.Features) {
				plc.FECAdd(decoded.Features[start : start+NumFeatures])
			}
			continue
		}
		plc.FECAdd(nil)
	}
	return window
}
