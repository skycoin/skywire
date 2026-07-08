package multistream

import (
	"fmt"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// celtSilenceFrame2B is the all-ones 2-byte CELT frame libopus decodes to let
// the CELT MDCT fade out on a Hybrid->SILK transition (opus_decode_frame).
var celtSilenceFrame2B = [...]byte{0xFF, 0xFF}

// transitionState captures the per-stream mode-transition decision for one
// frame, mirroring the pcm_transition handling in libopus opus_decode_frame
// (src/opus_decoder.c). A transition is a 5 ms crossfade applied whenever the
// per-stream coding mode crosses the CELT_ONLY boundary across consecutive
// frames, smoothing the discontinuity between the SILK/Hybrid and CELT MDCT
// reconstructions.
type transitionState struct {
	active     bool
	pcm        []float32
	prevMode   int
	prevBW     int
	prevStereo bool
	// pendingTransSize is the 5 ms transition span for a non-CELT target whose
	// transition PLC frame is decoded later (after the redundancy flags are read).
	pendingTransSize int
}

// beginModeTransition decides whether this frame is a CELT-boundary mode
// transition and, for a CELT-only target, decodes the 5 ms PLC transition frame
// in the previous mode up front (mirrors opus_decode_frame lines ~374-390:
// pcm_transition_celt decoded before the main CELT decode). For a non-CELT
// target the transition PLC frame is decoded later, after the redundancy flags
// are known, since libopus zeroes the transition when redundancy is present.
func (d *streamState) beginModeTransition(toc streamTOC, transSize int) (transitionState, error) {
	var ts transitionState
	if !d.haveDecoded {
		return ts, nil
	}
	prevMode := int(d.lastMode)
	mode := toc.mode
	celtBoundary := (mode == streamModeCELT && prevMode != streamModeCELT && !d.prevRedundancy) ||
		(mode != streamModeCELT && prevMode == streamModeCELT)
	if !celtBoundary {
		return ts, nil
	}
	ts.active = true
	ts.prevMode = prevMode
	ts.prevBW = int(d.lastBandwidth)
	ts.prevStereo = d.lastPacketStereo
	if mode == streamModeCELT {
		pcm, err := d.transitionPLCToFloat32(transSize, ts.prevMode, ts.prevBW, ts.prevStereo)
		if err != nil {
			return ts, err
		}
		ts.pcm = pcm
	}
	return ts, nil
}

// applyModeTransition crossfades the freshly decoded frame onto the transition
// PLC frame, mirroring opus_decode_frame lines ~660-676. With at least 5 ms of
// audio it copies the first 2.5 ms verbatim and smooth_fades the next 2.5 ms;
// otherwise it fades the whole available window.
func (d *streamState) applyModeTransition(ts *transitionState, out []float32, frameSize int) {
	if !ts.active || len(ts.pcm) == 0 {
		return
	}
	channels := int(d.channels)
	fs := int(d.sampleRate)
	f10 := fs / 50 / 2
	f5 := f10 >> 1
	f2_5 := f5 >> 1
	if frameSize >= f5 {
		copy(out[:f2_5*channels], ts.pcm[:f2_5*channels])
		streamSmoothFade(ts.pcm[f2_5*channels:], out[f2_5*channels:], out[f2_5*channels:], f2_5, channels, fs)
	} else {
		streamSmoothFade(ts.pcm, out, out, f2_5, channels, fs)
	}
}

// decodeSILKModeWithTransition decodes a SILK-only frame with the libopus
// CELT->SILK SILK-state reset, the Hybrid->SILK CELT fade-out, the SILK<->CELT
// redundancy handling, and the pcm_transition crossfade from a previous CELT
// frame (opus_decode_frame). The SILK payload is decoded through a shared range
// decoder so the trailing redundancy flag and the redundant 5 ms CELT frame can
// be read from the same decoder, exactly as opus_decode_frame does.
func (d *streamState) decodeSILKModeWithTransition(frame []byte, frameSize, transSize int, toc streamTOC) ([]float32, error) {
	bw, ok := silk.BandwidthFromOpus(toc.bandwidth)
	if !ok {
		return nil, fmt.Errorf("multistream: invalid SILK bandwidth: %d", toc.bandwidth)
	}

	ts, err := d.beginModeTransition(toc, transSize)
	if err != nil {
		return nil, err
	}

	// libopus discards stale SILK state on a CELT->SILK transition (the SILK
	// decoder has no usable history after a CELT-only run); mirrors the
	// MODE_SILK_ONLY path in opus_decode_frame.
	if d.haveDecoded && int(d.lastMode) == streamModeCELT {
		d.silkDec.Reset()
	}

	channels := int(d.channels)
	celtBW := celt.BandwidthFromOpusConfig(toc.bandwidth)
	fs := int(d.sampleRate)
	f10 := fs / 100
	f5 := f10 >> 1
	f2_5 := f5 >> 1

	// libopus decodes the SILK frame at IMAX(F10, audiosize) and trims to the
	// requested size (opus_decode_frame silk_frame_size); SILK's minimum is 10 ms,
	// so this only matters if a caller ever requests less than F10.
	silkDecodeSize := max(frameSize, f10)

	var rd rangecoding.Decoder
	rd.Init(frame)
	out, err := d.decodeSILKWithDecoder(&rd, silkDecodeSize, toc.stereo, bw)
	if err != nil {
		return nil, err
	}
	if frameSize < silkDecodeSize {
		out = out[:frameSize*channels]
	}

	// After the SILK payload, libopus reads the redundancy flag from the same
	// range decoder: for SILK-only mode redundancy is implied (no logp(12) bit)
	// when at least 17 bits remain, and the redundant frame occupies the trailing
	// bytes (opus_decode_frame, MODE_SILK_ONLY redundancy block).
	redundancy := false
	celtToSilk := false
	redundancyBytes := 0
	mainLen := len(frame)
	var redundantAudio []float32
	if rd.Tell()+17 <= 8*len(frame) {
		redundancy = true
		celtToSilk = rd.DecodeBit(1) == 1
		redundancyBytes = len(frame) - ((rd.Tell() + 7) >> 3)
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

	redundancyValid := redundancy && redundancyBytes > 0 && mainLen >= 0 && mainLen+redundancyBytes <= len(frame)

	// A CELT->SILK redundant frame is decoded BEFORE the Hybrid->SILK fade-out so
	// the fade-out gate sees the redundancy decision (opus_decode_frame ordering).
	// The redundant frame is band-limited to the SILK bandwidth (libopus sets
	// CELT_SET_END_BAND for the bandwidth, then CELT_SET_START_BAND(0), before the
	// celt_decode_with_ec call) and the CELT decoder keeps its prior state (no
	// reset) so the main frame reads the redundancy-updated state.
	if redundancyValid && celtToSilk {
		d.celtDec.SetBandwidth(celtBW)
		redundantData := frame[mainLen : mainLen+redundancyBytes]
		redundantAudio = make([]float32, f5*channels)
		if err := d.celtDec.DecodeFrameWithPacketStereoToFloat32AtAPIRate(redundantData, f5, toc.stereo, redundantAudio); err != nil {
			return nil, err
		}
	}

	// Hybrid->SILK fade-out: decode a 2.5 ms CELT silence frame and add it so the
	// CELT MDCT history rings down cleanly (opus_decode_frame MODE_SILK_ONLY else
	// branch), skipped when a CELT->SILK redundant frame continues a redundancy run.
	if !(redundancy && celtToSilk && d.prevRedundancy) {
		if err := d.addHybridToSilkFadeOut(out); err != nil {
			return nil, err
		}
	}

	// SILK->CELT redundancy: reset CELT, decode the redundant full-band 5 ms frame,
	// then fade its tail onto the end of the main SILK frame.
	if redundancyValid && !celtToSilk {
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
	}

	// CELT->SILK redundancy: copy the redundant head and fade it into the main
	// frame (only on the first redundant frame of a SILK run). On a fresh stream
	// there is no previous mode (libopus prev_mode==0 != MODE_SILK_ONLY), so the
	// crossfade applies; !d.haveDecoded mirrors that.
	if redundancyValid && celtToSilk && (!d.haveDecoded || int(d.lastMode) != streamModeSILK || d.prevRedundancy) && len(redundantAudio) >= f5*channels {
		copy(out[:f2_5*channels], redundantAudio[:f2_5*channels])
		streamSmoothFade(redundantAudio[f2_5*channels:], out[f2_5*channels:], out[f2_5*channels:], f2_5, channels, fs)
	}

	// pcm_transition for a CELT->SILK mode change is zeroed when redundancy is
	// present (opus_decode_frame); otherwise decode the 5 ms PLC frame in the
	// previous CELT mode and crossfade it onto the front of this SILK frame.
	if ts.active && redundancy {
		ts.active = false
	}
	if ts.active && len(ts.pcm) == 0 {
		pcm, perr := d.transitionPLCToFloat32(transSize, ts.prevMode, ts.prevBW, ts.prevStereo)
		if perr != nil {
			return nil, perr
		}
		ts.pcm = pcm
	}

	d.applyModeTransition(&ts, out, frameSize)
	d.prevRedundancy = redundancy && !celtToSilk
	return out, nil
}

// transitionPLCToFloat32 decodes a packet-loss-concealment frame of transSize
// samples in the supplied previous mode/bandwidth/stereo without recording it as
// the stream's decode state. It mirrors the opus_decode_frame(NULL) transition
// decode, which advances the shared SILK/Hybrid/CELT concealment state in the
// previous mode before the main frame is decoded.
func (d *streamState) transitionPLCToFloat32(transSize, prevMode, prevBW int, prevStereo bool) ([]float32, error) {
	channels := int(d.channels)
	switch prevMode {
	case streamModeSILK:
		// SILK concealment cannot produce less than 10 ms; libopus decodes the
		// transition PLC frame at >=F10 and uses only the first transSize samples,
		// advancing the SILK PLC state by a full 10 ms (opus_decode_frame ->
		// silk_frame_size = IMAX(F10, ...)).
		f10 := int(d.sampleRate) / 100
		silkPLCSize := max(transSize, f10)
		pcm, err := d.decodeSILKToFloat32(nil, silkPLCSize, prevStereo, prevBW)
		if err != nil {
			return nil, err
		}
		if silkPLCSize > transSize {
			pcm = pcm[:transSize*channels]
		}
		return pcm, nil
	case streamModeHybrid:
		return d.hybridDec.DecodeToFloat32WithPacketStereo(nil, transSize, prevStereo)
	case streamModeCELT:
		d.celtDec.SetBandwidth(celt.BandwidthFromOpusConfig(prevBW))
		out := make([]float32, transSize*int(d.channels))
		if err := d.celtDec.DecodeFrameWithPacketStereoToFloat32AtAPIRate(nil, transSize, prevStereo, out); err != nil {
			return nil, err
		}
		return out, nil
	default:
		return make([]float32, transSize*int(d.channels)), nil
	}
}

// addHybridToSilkFadeOut handles the libopus Hybrid->SILK fade-out: when the
// previous frame was Hybrid and this frame is SILK-only, libopus decodes a 2.5 ms
// CELT silence frame at start band 0 and adds it so the CELT MDCT history rings
// down cleanly (opus_decode_frame, the MODE_SILK_ONLY else branch). The shared
// CELT decoder matches the single decoder libopus uses, so decoding here advances
// the same state.
func (d *streamState) addHybridToSilkFadeOut(out []float32) error {
	if int(d.lastMode) != streamModeHybrid {
		return nil
	}
	channels := int(d.channels)
	f2_5 := d.sampleRateF2_5()
	scratch := make([]float32, f2_5*channels)
	if err := d.celtDec.DecodeFrameWithPacketStereoToFloat32AtAPIRate(celtSilenceFrame2B[:], f2_5, d.lastPacketStereo, scratch); err != nil {
		return err
	}
	n := min(len(scratch), len(out))
	for i := 0; i < n; i++ {
		out[i] += scratch[i]
	}
	return nil
}

func (d *streamState) sampleRateF2_5() int {
	return int(d.sampleRate) / 50 / 2 / 2 / 2
}
