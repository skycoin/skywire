package dred

import (
	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/dred/rdovae"
	"github.com/thesyncim/gopus/internal/lpcnetplc"
)

// LatentGenerator mirrors the libopus DRED encoder-side d-frame pipeline for
// already-prepared 16 kHz mono PCM. It retains LPCNet analysis and RDOVAE
// recurrent state so repeated use stays allocation-free.
type LatentGenerator struct {
	buffer        EncoderBuffer
	analysis      lpcnetplc.Analysis
	processor     rdovae.EncoderProcessor
	featureFrames [2 * lpcnetplc.NumTotalFeatures]float32
	input         [2 * NumFeatures]float32
	latents       [rdovae.LatentDim]float32
	initialState  [rdovae.StateDim]float32
}

// SetDNNBlob binds the shared pitch model family needed by the encoder-side
// LPCNet analysis front end and resets retained state.
func (g *LatentGenerator) SetDNNBlob(blob *dnnblob.Blob) error {
	if err := g.analysis.SetModel(blob); err != nil {
		g.Reset()
		return err
	}
	g.analysis.SetDREDEncoderMode(true)
	g.Reset()
	return nil
}

// SetDNNBlobPreservingState replaces the shared pitch model family without
// clearing retained DRED encoder state.
func (g *LatentGenerator) SetDNNBlobPreservingState(blob *dnnblob.Blob) error {
	if err := g.analysis.SetModelPreservingState(blob); err != nil {
		return err
	}
	g.analysis.SetDREDEncoderMode(true)
	return nil
}

// Loaded reports whether the latent generator has the pitch-analysis model it
// needs to mirror libopus DRED feature extraction.
func (g *LatentGenerator) Loaded() bool {
	return g != nil && g.analysis.Loaded()
}

// Reset clears retained feature-extraction and RDOVAE recurrent state while
// preserving the bound pitch model.
func (g *LatentGenerator) Reset() {
	if g == nil {
		return
	}
	g.buffer.Reset()
	g.analysis.Reset()
	g.processor.Reset()
	g.featureFrames = [2 * lpcnetplc.NumTotalFeatures]float32{}
	g.input = [2 * NumFeatures]float32{}
	g.latents = [rdovae.LatentDim]float32{}
	g.initialState = [rdovae.StateDim]float32{}
}

// DREDOffset reports the current libopus-shaped DRED offset in 2.5 ms units.
func (g *LatentGenerator) DREDOffset() int32 {
	if g == nil {
		return 0
	}
	return g.buffer.DREDOffset()
}

// LatentOffset reports the current libopus-shaped latent offset.
func (g *LatentGenerator) LatentOffset() int32 {
	if g == nil {
		return 0
	}
	return g.buffer.LatentOffset()
}

// Process16k mirrors the libopus encoder-side dred_compute_latents() inner
// 16 kHz mono d-frame path. The callback, when non-nil, is invoked once per
// emitted latent/state pair with slices that alias internal storage and remain
// valid only until the callback returns.
func (g *LatentGenerator) Process16k(model *rdovae.EncoderModel, pcm []float32, extraDelay int32, emit func(latents, initialState []float32)) int {
	if g == nil || model == nil || !g.Loaded() {
		return 0
	}

	return g.buffer.Append16k(pcm, extraDelay, func(frame []float32) {
		first := g.featureFrames[:lpcnetplc.NumTotalFeatures]
		second := g.featureFrames[lpcnetplc.NumTotalFeatures:]
		if n := g.analysis.ComputeSingleFrameFeaturesFloat(first, frame[:FrameSize]); n != lpcnetplc.NumTotalFeatures {
			return
		}
		if n := g.analysis.ComputeSingleFrameFeaturesFloat(second, frame[FrameSize:DFrameSize]); n != lpcnetplc.NumTotalFeatures {
			return
		}
		copy(g.input[:NumFeatures], first[:NumFeatures])
		copy(g.input[NumFeatures:2*NumFeatures], second[:NumFeatures])
		if !model.EncodeDFrameWithProcessor(&g.processor, g.latents[:], g.initialState[:], g.input[:]) {
			return
		}
		if emit != nil {
			emit(g.latents[:], g.initialState[:])
		}
	})
}
