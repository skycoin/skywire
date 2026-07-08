package multistream

import (
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// decodeHybridModeWithTransition wraps the Hybrid decode with the libopus
// pcm_transition crossfade for a CELT->Hybrid mode change. The transition PLC
// frame (decoded in the previous CELT mode) is produced inside the Hybrid decode
// hook once redundancy is known to be absent, mirroring opus_decode_frame, then
// crossfaded onto the front of the decoded Hybrid frame.
func (d *streamState) decodeHybridModeWithTransition(frame []byte, frameSize, transSize int, toc streamTOC) ([]float32, error) {
	var ts transitionState
	if d.haveDecoded && int(d.lastMode) == streamModeCELT {
		ts.active = true
		ts.prevMode = streamModeCELT
		ts.prevBW = int(d.lastBandwidth)
		ts.prevStereo = d.lastPacketStereo
		ts.pendingTransSize = transSize
	}
	out, err := d.decodeHybridToFloat32(frame, frameSize, toc, &ts)
	if err != nil {
		return nil, err
	}
	d.applyModeTransition(&ts, out, frameSize)
	return out, nil
}

// decodeHybridToFloat32 decodes one Hybrid (SILK+CELT) frame mirroring libopus
// opus_decode_frame: after SILK fills the low band, the shared range decoder is
// advanced past the hybrid redundancy flags before the CELT highband reads bands
// 17..21, then any SILK<->CELT redundancy and the CELT->Hybrid mode-transition
// crossfade (via ts) are applied. Skipping the redundancy-flag read leaves the
// CELT highband reading from the wrong bit position, which is what produced the
// coupled-stereo Hybrid float divergence vs libopus.
//
// Reference: src/opus_decoder.c opus_decode_frame (redundancy handling at
// start_band=17, celt_accum=1; redundant 5 ms CELT frame + smooth_fade).
func (d *streamState) decodeHybridToFloat32(frame []byte, frameSize int, toc streamTOC, ts *transitionState) ([]float32, error) {
	channels := int(d.channels)
	celtBW := celt.BandwidthFromOpusConfig(toc.bandwidth)

	// libopus discards stale SILK state on a CELT->Hybrid transition, and resets
	// the CELT decoder on any mode change that did not come from a redundancy
	// frame (needCeltReset). These mirror opus_decode_frame.
	if d.haveDecoded && d.lastMode == streamModeCELT {
		d.silkDec.Reset()
	}
	needCeltReset := d.haveDecoded && int(d.lastMode) != toc.mode && !d.prevRedundancy
	d.celtDec.SetBandwidth(celtBW)

	const (
		f10  = 480
		f5   = f10 >> 1
		f2_5 = f5 >> 1
	)
	fs := int(d.sampleRate)

	redundancy := false
	celtToSilk := false
	redundancyBytes := 0
	mainLen := len(frame)
	var redundantAudio []float32

	afterSilk := func(rd *rangecoding.Decoder) error {
		if rd == nil {
			return nil
		}
		if rd.Tell()+17+20 <= 8*len(frame) {
			redundancy = rd.DecodeBit(12) == 1
			if redundancy {
				celtToSilk = rd.DecodeBit(1) == 1
				redundancyBytes = int(rd.DecodeUniformSmall(256)) + 2
				mainLen = len(frame) - redundancyBytes
				if mainLen*8 < rd.Tell() {
					mainLen = 0
					redundancyBytes = 0
					redundancy = false
					celtToSilk = false
				} else {
					rd.ShrinkStorage(redundancyBytes)
				}
			}
		}
		// Hand the final redundancy decision to the gopus_fixed_point integer
		// highband hook (which runs next, against a clone of this same decoder
		// positioned at the CELT start band) so it can decline redundant frames it
		// does not reproduce. A no-op in the default build.
		d.setFixedHybridRedundancy(redundancy)
		// pcm_transition for a CELT->Hybrid mode change: decode the 5 ms PLC frame
		// in the previous CELT mode now that redundancy is known to be absent and
		// before the CELT decoder is reset (opus_decode_frame lines ~540-543).
		if ts != nil && ts.active && ts.pendingTransSize > 0 && !redundancy && len(ts.pcm) == 0 {
			pcm, perr := d.transitionPLCToFloat32(ts.pendingTransSize, ts.prevMode, ts.prevBW, ts.prevStereo)
			if perr != nil {
				return perr
			}
			ts.pcm = pcm
		}
		if ts != nil && redundancy {
			ts.active = false
		}
		// 5 ms CELT->SILK redundant frame: decoded on the shared CELT decoder at
		// start band 0 BEFORE the main hybrid highband, so the main decode reads
		// the redundancy-updated CELT state (no reset). Mirrors opus_decode_frame.
		if redundancy && celtToSilk && redundancyBytes > 0 && mainLen >= 0 && mainLen+redundancyBytes <= len(frame) {
			redundantData := frame[mainLen : mainLen+redundancyBytes]
			redundantAudio = make([]float32, f5*channels)
			if rerr := d.celtDec.DecodeFrameWithPacketStereoToFloat32AtAPIRate(redundantData, f5, toc.stereo, redundantAudio); rerr != nil {
				return rerr
			}
		}
		if needCeltReset {
			d.celtDec.Reset()
			d.celtDec.SetBandwidth(celtBW)
		}
		return nil
	}

	var rd rangecoding.Decoder
	rd.Init(frame)
	out, err := d.hybridDec.DecodeWithDecoderHook(&rd, frameSize, toc.stereo, afterSilk)
	if err != nil {
		return nil, err
	}

	if redundancy {
		valid := redundancyBytes > 0 && mainLen >= 0 && mainLen+redundancyBytes <= len(frame)
		if valid && !celtToSilk {
			// 5 ms SILK->CELT redundant frame: reset CELT, decode full-band, then
			// fade the redundant tail onto the end of the main frame.
			d.celtDec.Reset()
			d.celtDec.SetBandwidth(celtBW)
			redundantData := frame[mainLen : mainLen+redundancyBytes]
			redundantAudio = make([]float32, f5*channels)
			if err := d.celtDec.DecodeFrameWithPacketStereoToFloat32AtAPIRate(redundantData, f5, toc.stereo, redundantAudio); err != nil {
				return nil, err
			}
			start := (frameSize - f2_5) * channels
			if start >= 0 && start < len(out) && len(redundantAudio) >= f5*channels {
				streamSmoothFade(out[start:], redundantAudio[f2_5*channels:], out[start:], f2_5, channels, fs)
			}
		} else if valid && celtToSilk && (int(d.lastMode) != streamModeSILK || d.prevRedundancy) && len(redundantAudio) >= f5*channels {
			// 5 ms CELT->SILK: copy the redundant head and fade into the main frame.
			copy(out[:f2_5*channels], redundantAudio[:f2_5*channels])
			streamSmoothFade(redundantAudio[f2_5*channels:], out[f2_5*channels:], out[f2_5*channels:], f2_5, channels, fs)
		}
	}

	d.prevRedundancy = redundancy && !celtToSilk
	return out, nil
}

// streamSmoothFade applies the libopus CELT-window crossfade used by the hybrid
// redundancy/transition handling. It mirrors opus_decoder.c smooth_fade.
func streamSmoothFade(in1, in2, out []float32, overlap, channels, sampleRate int) {
	if overlap <= 0 || channels <= 0 || sampleRate <= 0 {
		return
	}
	inc := 48000 / sampleRate
	if inc <= 0 {
		inc = 1
	}
	win := celt.GetWindowBufferF32(overlap * inc)
	if len(win) == 0 {
		return
	}
	maxSamples := overlap * channels
	if len(out) < maxSamples || len(in1) < maxSamples || len(in2) < maxSamples {
		maxSamples = min(len(out), min(len(in1), len(in2)))
		overlap = maxSamples / channels
	}
	for c := range channels {
		for i := 0; i < overlap; i++ {
			w := win[i*inc]
			w = streamSmoothFadeMul(w, w)
			idx := i*channels + c
			if idx >= len(out) || idx >= len(in1) || idx >= len(in2) {
				break
			}
			oneMinusW := streamSmoothFadeSub(float32(1), w)
			out[idx] = streamSmoothFadeMul(w, in2[idx]) + streamSmoothFadeMul(oneMinusW, in1[idx])
		}
	}
}

//go:noinline
func streamSmoothFadeMul(a, b float32) float32 { return a * b }

//go:noinline
func streamSmoothFadeSub(a, b float32) float32 { return a - b }
