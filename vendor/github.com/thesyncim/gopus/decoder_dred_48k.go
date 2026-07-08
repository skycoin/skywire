//go:build gopus_dred || gopus_osce

package gopus

import "github.com/thesyncim/gopus/internal/lpcnetplc"

// applyDREDNeuralConcealment48kMono drives one 48 kHz neural CELT
// concealment frame. libopus is fundamentally mono DRED (single
// LPCNetPLCState in opus_decoder.c; mono-downmix on entry, mono-duplicate on
// exit). For stereo decoders we run the same mono pipeline and rely on the
// CELT wrapper to mirror channel-0 state into channel-1 and to interleave the
// mono PCM across both output channels (celt_decoder.c:1066-1067).
func (d *Decoder) applyDREDNeuralConcealment48kMono(pcm []float32, samplesPerChannel int) bool {
	if d == nil || d.channels < 1 || d.channels > 2 || samplesPerChannel <= 0 {
		return false
	}
	channels := int(d.channels)
	sampleRate := int(d.sampleRate)
	if len(pcm) < samplesPerChannel*channels {
		return false
	}
	r := d.dredRecoveryState()
	n := d.dredNeuralState()
	b := d.dred48kBridgeState()
	if r == nil || n == nil || b == nil || d.celtDecoder == nil {
		return false
	}

	generate := func(frame []float32) bool {
		if len(frame) < lpcnetplc.FrameSize {
			return false
		}
		if r.dredPLC.Blend() == 0 {
			return r.dredPLC.GenerateConcealedFrameFloatWithAnalysis(&n.dredAnalysis, &n.dredPredictor, &n.dredFARGAN, frame[:lpcnetplc.FrameSize])
		}
		return r.dredPLC.GenerateConcealedFrameFloat(&n.dredPredictor, &n.dredFARGAN, frame[:lpcnetplc.FrameSize])
	}
	frameSize48 := samplesPerChannel
	downsample := 1
	celtPCM := pcm[:samplesPerChannel*channels]
	if sampleRate != 48000 {
		frameSize48 = d.frameSize48FromAPI(samplesPerChannel)
		downsample = 48000 / sampleRate
	}
	// Mono-downmix-in / mono-duplicate-out: the stereo path runs the mono
	// CELT neural concealment helper against the channel-0 slot of the CELT
	// state and mirrors the channel-0 result into channel-1 for both the
	// retained CELT state and the interleaved output PCM.
	var ok bool
	if r.dredPLC.FECFillPos() > r.dredPLC.FECReadPos() {
		ok = d.celtDecoder.ConcealDRED48kDownsampleToFloat32(
			celtPCM,
			frameSize48,
			downsample,
			&b.dredLastNeural,
			b.dredPLCPCM[:],
			&b.dredPLCFill,
			&b.dredPLCPreemphMem,
			generate,
		)
	} else {
		ok = d.celtDecoder.ConcealPLCNeural48kDownsampleToFloat32(
			celtPCM,
			frameSize48,
			downsample,
			&b.dredLastNeural,
			b.dredPLCPCM[:],
			&b.dredPLCFill,
			&b.dredPLCPreemphMem,
			generate,
		)
	}
	if !ok {
		return false
	}
	return true
}
