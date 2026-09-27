package gopus

import (
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

var celtSilenceFrame2B = [...]byte{0xFF, 0xFF}

// smoothFade applies a libopus-style crossfade using the CELT window.
func smoothFade(in1, in2, out []float32, overlap, channels, sampleRate int) {
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
			w = smoothFadeMul32(w, w)
			idx := i*channels + c
			if idx >= len(out) || idx >= len(in1) || idx >= len(in2) {
				break
			}
			oneMinusW := smoothFadeSub32(float32(1), w)
			out[idx] = smoothFadeFMA32(w, in2[idx], smoothFadeMul32(oneMinusW, in1[idx]))
		}
	}
}

func smoothFadeFMA32(a, b, c float32) float32 {
	return a*b + c
}

//go:noinline
func smoothFadeMul32(a, b float32) float32 {
	return a * b
}

//go:noinline
func smoothFadeSub32(a, b float32) float32 {
	return a - b
}

func copyFloat32(dst []float32, src []float32) {
	n := min(len(src), len(dst))
	copy(dst, src[:n])
	if n < len(dst) {
		clear(dst[n:])
	}
}

func (d *Decoder) frameSize48FromAPI(frameSize int) int {
	if d.sampleRate <= 0 || d.sampleRate == 48000 {
		return frameSize
	}
	return frameSize * 48000 / int(d.sampleRate)
}

func (d *Decoder) downsampleFrame48ToAPI(dst, src []float32, frameSize int) {
	channels := int(d.channels)
	if d.sampleRate == 48000 {
		copyFloat32(dst[:frameSize*channels], src[:frameSize*channels])
		return
	}
	factor := 48000 / int(d.sampleRate)
	if factor <= 1 {
		copyFloat32(dst[:frameSize*channels], src[:frameSize*channels])
		return
	}
	for i := range frameSize {
		srcBase := i * factor * channels
		dstBase := i * channels
		for c := range channels {
			dst[dstBase+c] = src[srcBase+c]
		}
	}
}

func (d *Decoder) decodeCELTFrameToAPIScratch(data []byte, frameSize int, packetStereo bool) ([]float32, error) {
	needed := frameSize * int(d.channels)
	if len(d.scratchRedundant) < needed {
		return nil, ErrBufferTooSmall
	}
	out := d.scratchRedundant[:needed]
	if err := d.celtDecoder.DecodeFrameWithPacketStereoToFloat32AtAPIRate(data, frameSize, packetStereo, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (d *Decoder) prepareStereoTransition(packetStereo bool, bandwidth silk.Bandwidth) {
	if !packetStereo || d.channels != 2 || d.prevPacketStereo {
		return
	}

	d.silkDecoder.ResetSideChannel()
	leftResampler := d.silkDecoder.GetResampler(bandwidth)
	rightResampler := d.silkDecoder.GetResamplerRightChannel(bandwidth)
	if rightResampler != nil && leftResampler != nil {
		rightResampler.CopyFrom(leftResampler)
	}
}

func addFloat32ToFloat32(dst []float32, src []float32) {
	n := min(len(src), len(dst))
	for i := range n {
		dst[i] += src[i]
	}
}

// decodeOpusFrameInto mirrors libopus opus_decode_frame behavior for a single frame.
// out must have room for frameSize * channels samples.
func (d *Decoder) decodeOpusFrameInto(
	out []float32,
	data []byte,
	frameSize int,
	packetFrameSize int,
	packetMode Mode,
	packetBandwidth Bandwidth,
	packetStereo bool,
) (int, error) {
	return d.decodeOpusFrameIntoWithQEXT(
		out,
		data,
		frameSize,
		packetFrameSize,
		packetMode,
		packetBandwidth,
		packetStereo,
		nil,
	)
}

func (d *Decoder) decodeOpusFrameIntoWithQEXT(
	out []float32,
	data []byte,
	frameSize int,
	packetFrameSize int,
	packetMode Mode,
	packetBandwidth Bandwidth,
	packetStereo bool,
	qextPayload []byte,
) (int, error) {
	return d.decodeOpusFrameIntoWithStatePolicyAndQEXT(
		out,
		data,
		frameSize,
		packetFrameSize,
		packetMode,
		packetBandwidth,
		packetStereo,
		true,
		qextPayload,
	)
}

// decodeOpusFrameIntoWithStatePolicy mirrors libopus opus_decode_frame behavior
// for a single frame and allows callers to control whether nil-data PLC decode
// should source mode/bandwidth/stereo from decoder state.
// out must have room for frameSize * channels samples.
func (d *Decoder) decodeOpusFrameIntoWithStatePolicy(
	out []float32,
	data []byte,
	frameSize int,
	packetFrameSize int,
	packetMode Mode,
	packetBandwidth Bandwidth,
	packetStereo bool,
	useDecoderPLCState bool,
) (int, error) {
	return d.decodeOpusFrameIntoWithStatePolicyAndQEXT(
		out,
		data,
		frameSize,
		packetFrameSize,
		packetMode,
		packetBandwidth,
		packetStereo,
		useDecoderPLCState,
		nil,
	)
}

func (d *Decoder) decodeOpusFrameIntoWithStatePolicyAndQEXT(
	out []float32,
	data []byte,
	frameSize int,
	packetFrameSize int,
	packetMode Mode,
	packetBandwidth Bandwidth,
	packetStereo bool,
	useDecoderPLCState bool,
	qextPayload []byte,
) (int, error) {
	channels := int(d.channels)
	fs := 48000
	if packetMode == ModeSILK || packetMode == ModeCELT || packetMode == ModeHybrid {
		fs = int(d.sampleRate)
	}
	F20 := fs / 50
	F10 := F20 >> 1
	F5 := F10 >> 1
	F2_5 := F5 >> 1

	if frameSize < F2_5 {
		return 0, ErrBufferTooSmall
	}

	maxFrame := fs / 25 * 3
	frameSize = min(frameSize, maxFrame)

	// libopus opus_decode_frame: a frame whose coded length is <= 1 (0 or just a
	// ToC byte) triggers PLC/DTX, and its final range is forced to 0 (the
	// `if (len <= 1) st->rangeFinal = 0` at the end of opus_decode_frame). This
	// is a PER-FRAME condition on the frame's own size, so for a multi-frame
	// packet whose last frame is DTX the packet's OPUS_GET_FINAL_RANGE is 0 even
	// though the whole packet length is > 1.
	frameLenLE1 := len(data) <= 1
	if frameLenLE1 {
		data = nil
		if packetFrameSize > 0 {
			frameSize = min(frameSize, packetFrameSize)
		}
	}

	audiosize := frameSize
	mode := packetMode
	bandwidth := packetBandwidth
	packetStereoLocal := packetStereo

	if data == nil {
		audiosize = frameSize
		if !d.haveDecoded {
			needed := audiosize * channels
			if len(out) < needed {
				return 0, ErrBufferTooSmall
			}
			clear(out[:needed])
			return audiosize, nil
		}

		if useDecoderPLCState {
			if d.prevRedundancy {
				mode = ModeCELT
			} else {
				mode = d.prevMode
			}
			bandwidth = d.lastBandwidth
			packetStereoLocal = d.prevPacketStereo
		}

		if audiosize > F20 {
			return d.decodePLCChunksInto(out, audiosize, plcDecodeState{
				packetFrameSize:    packetFrameSize,
				mode:               mode,
				bandwidth:          bandwidth,
				packetStereo:       packetStereoLocal,
				useDecoderPLCState: useDecoderPLCState,
			})
		} else if audiosize < F20 {
			if audiosize > F10 {
				audiosize = F10
			} else if mode != ModeSILK && audiosize > F5 && audiosize < F10 {
				audiosize = F5
			}
		}
	} else {
		audiosize = packetFrameSize
	}

	if audiosize > frameSize {
		return 0, ErrBufferTooSmall
	}
	frameSize = audiosize

	needed := frameSize * channels
	if len(out) < needed {
		return 0, ErrBufferTooSmall
	}
	out = out[:needed]

	transition := false
	var pcmTransition []float32
	if data != nil && d.haveDecoded && ((mode == ModeCELT && d.prevMode != ModeCELT &&
		!d.prevRedundancy) ||
		(mode != ModeCELT && d.prevMode == ModeCELT)) {
		transition = true
		if mode == ModeCELT {
			transSize := min(F5, audiosize)
			if len(d.scratchTransition) < transSize*channels {
				return 0, ErrBufferTooSmall
			}
			// libopus opus_decoder.c:387-390 decodes a 5 ms transition frame via
			// opus_decode_frame(NULL) using the previous (SILK/Hybrid) mode. When
			// deep PLC / DRED is enabled, that PLC frame runs silk_PLC_conceal,
			// which advances the LPCNet PLC state via lpcnet_plc_conceal()
			// (silk/PLC.c:400-405, run_deep_plc = enable_deep_plc). Route the
			// transition PLC through the DRED neural concealment hook so the
			// LPCNet PCM history / continuity state is advanced by the same one
			// concealed frame before DRED recovery begins.
			cleanupHook := func() {}
			if d.prevMode != ModeCELT && d.dredNeuralConcealmentAvailable() {
				cleanupHook, _ = d.beginHybridDREDLowbandHook()
			}
			n, err := d.decodeOpusFrameIntoWithStatePolicy(
				d.scratchTransition,
				nil,
				transSize,
				packetFrameSize,
				d.prevMode,
				d.lastBandwidth,
				packetStereoLocal,
				useDecoderPLCState,
			)
			cleanupHook()
			if err != nil {
				return 0, err
			}
			pcmTransition = d.scratchTransition[:n*channels]
		}
	}

	rd := &d.scratchRangeDecoder
	if data != nil {
		rd.Init(data)
	}

	celtBW := celt.CELTFullband
	if data != nil {
		celtBW = celt.BandwidthFromOpusConfig(int(bandwidth))
		d.celtDecoder.SetBandwidth(celtBW)
	}

	redundancy := false
	celtToSilk := false
	redundancyBytes := 0
	mainLen := len(data)
	// fixedHybridFrame records that this frame was decoded by the integer Hybrid
	// path (gopus_fixed_point), so the redundancy / transition post-processing runs
	// its opus_res-domain equivalent and keeps the int16/int24 output bit-exact.
	fixedHybridFrame := false
	var redundantAudio []float32
	var redundantRng uint32 // Captured final range from redundancy decoding

	needCeltReset := d.haveDecoded && mode != d.prevMode && !d.prevRedundancy

	decodeRedundantCELT := func(redundancyData []byte) ([]float32, error) {
		samples, err := d.decodeCELTFrameToAPIScratch(redundancyData, F5, packetStereoLocal)
		if err != nil {
			return nil, err
		}
		// Capture the final range from decoding the redundancy frame
		redundantRng = d.celtDecoder.FinalRange()
		return samples, nil
	}

	switch mode {
	case ModeHybrid:
		if data != nil && d.haveDecoded && d.prevMode == ModeCELT {
			d.silkDecoder.Reset()
		}
		if data == nil {
			// A lost hybrid frame conceals via the float PLC (SILK PLC + float CELT
			// PLC). Under -tags gopus_fixed_point with an active integer-output packet,
			// also advance the integer CELT (highband) cross-frame state through the
			// loss and accumulate the concealed highband onto the integer SILK
			// lowband, mirroring opus_decode_frame's celt_decode_with_ec_dred(NULL,
			// celt_accum=1) for a lost hybrid frame -- so the integer output of this
			// frame AND the post-loss recovery frame stay bit-exact. When the integer
			// path is declined (no active packet, integer CELT not yet primed, or a
			// rate below 48 kHz) the int16/int24 wrappers use the float conversion.
			fixedHybridPLCArmed := false
			if !extsupport.QEXT {
				fixedHybridPLCArmed = d.armFixedHybridLost(frameSize, packetStereoLocal)
			}
			if !fixedHybridPLCArmed {
				d.markFixedUnhandled()
			}
			samples, err := d.hybridDecoder.DecodeToFloat32WithPacketStereo(nil, frameSize, packetStereoLocal)
			if err != nil {
				if fixedHybridPLCArmed {
					d.silkDecoder.ArmPLCLowbandCapture(nil)
				}
				return 0, err
			}
			copyFloat32(out, samples)
			// Capture FinalRange for PLC
			d.mainDecodeRng = d.hybridDecoder.FinalRange()
			if fixedHybridPLCArmed {
				if !d.finishFixedHybridLost(frameSize) {
					d.markFixedUnhandled()
				}
			}
		} else {
			// Under -tags gopus_fixed_point with an active integer-output packet,
			// arm the integer CELT highband hook so the hybrid decode also produces
			// libopus-exact int16/int24 output (start band 17, celt_accum onto the
			// SILK opus_res lowband). prepareFixedHybrid is a no-op in the default
			// build and on the float Decode path, where the float conversion is used.
			fixedHybridArmed := false
			if !extsupport.QEXT {
				fixedHybridArmed = d.prepareFixedHybrid(data, celtBW, needCeltReset)
			}
			fixedHybridFrame = fixedHybridArmed
			if !fixedHybridArmed {
				d.markFixedUnhandled()
			}
			d.hybridDecoder.SetPrevPacketStereo(d.prevPacketStereo)
			afterSilk := func(rd *rangecoding.Decoder) error {
				if rd == nil {
					return nil
				}
				if rd.Tell()+17+20 <= 8*len(data) {
					redundancy = rd.DecodeBit(12) == 1
					if redundancy {
						celtToSilk = rd.DecodeBit(1) == 1
						redundancyBytes = int(rd.DecodeUniformSmall(256)) + 2
						mainLen = len(data) - redundancyBytes
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

				if redundancy && celtToSilk && redundancyBytes > 0 && mainLen >= 0 && mainLen+redundancyBytes <= len(data) {
					redundantData := data[mainLen : mainLen+redundancyBytes]
					// Mirror the reference: the integer CELT->SILK redundancy
					// frame is decoded (start band 0, no reset) on the same
					// integer CELT decoder before the main hybrid accum, so the
					// shared decode_mem / energy state stays bit-identical.
					d.fixedDecodeRedundantCELT(redundantData, celtBW, false)
					decoded, err := decodeRedundantCELT(redundantData)
					if err != nil {
						return err
					}
					redundantAudio = decoded
				}

				if transition && !redundancy && len(pcmTransition) == 0 {
					transSize := min(F5, audiosize)
					// Mirror the reference transition decode on the integer CELT
					// decoder (5 ms CELT PLC, start band 0) before the main hybrid
					// accum; the accum's OPUS_RESET_STATE then discards its
					// decode_mem, exactly as in opus_decode_frame.
					d.fixedDecodeTransitionPLC(transSize)
					// The recursive float PLC decode declines the integer
					// accumulation; preserve the main hybrid frame's integer
					// status across it, since the integer transition crossfade
					// recovers the frame bit-exact. Suppress the integer
					// celt_decode_lost hook for the recursive data==nil decode:
					// fixedDecodeTransitionPLC already advanced the integer CELT
					// PLC state, so the recursive decode must only fill the float
					// pcmTransition buffer.
					handled := d.fixedSnapshotHandled()
					suppressed := d.fixedSuppressCELTPLC(true)
					n, err := d.decodeOpusFrameInto(d.scratchTransition, nil, transSize, packetFrameSize, d.prevMode, d.lastBandwidth, packetStereoLocal)
					d.fixedSuppressCELTPLC(suppressed)
					d.fixedRestoreHandled(handled)
					if err != nil {
						return err
					}
					pcmTransition = d.scratchTransition[:n*channels]
				}

				if needCeltReset {
					d.celtDecoder.Reset()
					d.celtDecoder.SetBandwidth(celtBW)
				}
				if extsupport.QEXT {
					d.setCELTQEXTPayload(qextPayload)
				}
				return nil
			}

			if err := d.hybridDecoder.DecodeWithDecoderHookToFloat32(rd, frameSize, packetStereoLocal, afterSilk, out); err != nil {
				if fixedHybridArmed {
					_ = d.finishFixedHybrid()
				}
				return 0, err
			}
			if fixedHybridArmed {
				if ferr := d.finishFixedHybrid(); ferr != nil {
					return 0, ferr
				}
			}
			// Capture the main decode's FinalRange before any redundancy post-processing
			d.mainDecodeRng = d.hybridDecoder.FinalRange()
		}

	case ModeSILK:
		// SILK output is not produced by the integer CELT path; the int16/int24
		// wrappers must use the float conversion for this packet.
		d.markFixedUnhandled()
		if d.haveDecoded && d.prevMode == ModeCELT {
			d.silkDecoder.Reset()
		}

		silkBW, ok := silk.BandwidthFromOpus(int(bandwidth))
		if !ok {
			silkBW = silk.BandwidthWideband
		}
		if extsupport.OSCERuntime && data != nil {
			restoreOSCELACEHook := d.installOSCELACESilkPostfilterHook(mode, silkBW, packetStereoLocal)
			defer restoreOSCELACEHook()
		}

		silkDecodeSize := max(frameSize, F10)

		var silkSamples int
		var err error
		if data != nil {
			d.prepareStereoTransition(packetStereoLocal, silkBW)
			switch {
			case packetStereoLocal && channels == 2:
				silkSamples, err = d.silkDecoder.DecodeStereoWithDecoderInto(rd, silkBW, silkDecodeSize, true, out)
				if err == nil {
					if frameSize < silkDecodeSize {
						silkSamples = frameSize
					}
				}
			case packetStereoLocal && channels == 1:
				var silkOut []float32
				silkOut, err = d.silkDecoder.DecodeStereoToMonoWithDecoder(rd, silkBW, silkDecodeSize, true)
				if err == nil {
					silkSamples = len(silkOut) / channels
					if frameSize < silkDecodeSize {
						silkSamples = frameSize
					}
					copyFloat32(out, silkOut[:silkSamples*channels])
				}
			case !packetStereoLocal && channels == 2:
				silkSamples, err = d.silkDecoder.DecodeMonoToStereoWithDecoderInto(rd, silkBW, silkDecodeSize, true, d.prevPacketStereo, out)
				if err == nil {
					if frameSize < silkDecodeSize {
						silkSamples = frameSize
					}
				}
			default:
				// Zero-allocation path for mono SILK decode
				silkSamples, err = d.silkDecoder.DecodeWithDecoderInto(rd, silkBW, silkDecodeSize, true, out)
				if err == nil && frameSize < silkDecodeSize {
					silkSamples = frameSize
				}
			}
		} else {
			d.prepareStereoTransition(packetStereoLocal, silkBW)
			switch {
			case packetStereoLocal && channels == 2:
				// Zero-allocation stereo SILK PLC: conceal interleaved L/R into
				// decoder-owned scratch (sized once at NewDecoder) and copy the
				// requested frame into out.
				if cap(d.scratchSilkPLC) < silkDecodeSize*channels {
					d.scratchSilkPLC = make([]float32, silkDecodeSize*channels)
				}
				plcBuf := d.scratchSilkPLC[:silkDecodeSize*channels]
				var n int
				n, err = d.silkDecoder.DecodePLCStereoInto(silkBW, silkDecodeSize, plcBuf)
				if err == nil {
					silkSamples = n / channels
					if frameSize < silkDecodeSize {
						silkSamples = frameSize
					}
					copyFloat32(out, plcBuf[:silkSamples*channels])
				}
			case packetStereoLocal && channels == 1:
				var silkOut []float32
				silkOut, err = d.silkDecoder.DecodeStereoToMono(nil, silkBW, silkDecodeSize, true)
				if err == nil {
					silkSamples = len(silkOut) / channels
					if frameSize < silkDecodeSize {
						silkSamples = frameSize
					}
					copyFloat32(out, silkOut[:silkSamples*channels])
				}
			case !packetStereoLocal && channels == 2:
				var silkOut []float32
				silkOut, err = d.silkDecoder.DecodeMonoToStereo(nil, silkBW, silkDecodeSize, true, d.prevPacketStereo)
				if err == nil {
					silkSamples = len(silkOut) / channels
					if frameSize < silkDecodeSize {
						silkSamples = frameSize
					}
					copyFloat32(out, silkOut[:silkSamples*channels])
				}
			default:
				// Zero-allocation mono SILK PLC: conceal into decoder-owned scratch
				// (sized once at NewDecoder) and copy the requested frame into out.
				if cap(d.scratchSilkPLC) < silkDecodeSize {
					d.scratchSilkPLC = make([]float32, silkDecodeSize)
				}
				plcBuf := d.scratchSilkPLC[:silkDecodeSize]
				var n int
				n, err = d.silkDecoder.DecodePLCInto(silkBW, silkDecodeSize, plcBuf)
				if err == nil {
					silkSamples = n / channels
					if frameSize < silkDecodeSize {
						silkSamples = frameSize
					}
					copyFloat32(out, plcBuf[:silkSamples*channels])
				}
			}
		}
		if err != nil {
			return 0, err
		}
		_ = silkSamples // Used for tracking decode size

		// Optional libopus OSCE BWE forward pass for SILK-only mode at 48 kHz
		// API with WB internal sample rate. Mirrors libopus
		// OSCE_MODE_SILK_BBWE which replaces the silk_resampler upsampling
		// with an ML-based 16 kHz -> 48 kHz bandwidth extension.
		//
		// The helper is a no-op outside of `gopus_osce` and
		// short-circuits when the BWE control is disabled / no BWE model is
		// loaded, so the standard silk_resampler output is retained for
		// every existing decode path.
		if extsupport.OSCERuntime && data != nil {
			d.maybeApplyOSCEBWEPostSilk(out, frameSize, mode, silkBW, packetStereoLocal)
		}

		if data != nil && rd.Tell()+17 <= 8*len(data) {
			redundancy = true
			celtToSilk = rd.DecodeBit(1) == 1
			redundancyBytes = len(data) - ((rd.Tell() + 7) >> 3)
			mainLen = len(data) - redundancyBytes
			if mainLen*8 < rd.Tell() {
				mainLen = 0
				redundancyBytes = 0
				redundancy = false
				celtToSilk = false
			} else {
				rd.ShrinkStorage(redundancyBytes)
			}
		}

		// Capture the main decode's FinalRange AFTER redundancy flag reads but BEFORE any CELT redundancy decode.
		// For SILK-only mode, the final range includes all bits read from the range decoder.
		d.mainDecodeRng = rd.Range()

		if transition && !redundancy && len(pcmTransition) == 0 {
			transSize := min(F5, audiosize)
			n, err := d.decodeOpusFrameIntoWithStatePolicy(
				d.scratchTransition,
				nil,
				transSize,
				packetFrameSize,
				d.prevMode,
				d.lastBandwidth,
				packetStereoLocal,
				useDecoderPLCState,
			)
			if err != nil {
				return 0, err
			}
			pcmTransition = d.scratchTransition[:n*channels]
		}

	case ModeCELT:
		if needCeltReset {
			d.celtDecoder.Reset()
			d.resetFixedCELT()
			if data != nil {
				d.celtDecoder.SetBandwidth(celtBW)
			}
		}
		if extsupport.QEXT {
			d.setCELTQEXTPayload(qextPayload)
		}
		// The float CELT decoder always runs: it fills the float out buffer
		// (the exact float Decode path) and advances the float cross-frame state
		// that a subsequent PLC frame depends on.
		if err := d.celtDecoder.DecodeFrameWithPacketStereoToFloat32AtAPIRate(data, min(F20, frameSize), packetStereoLocal, out); err != nil {
			return 0, err
		}
		// Capture the main decode's FinalRange (no redundancy post-processing for CELT-only)
		d.mainDecodeRng = d.celtDecoder.FinalRange()

		// Under -tags gopus_fixed_point, an active integer-output packet
		// (DecodeInt16 / DecodeInt24) additionally runs the integer FIXED_POINT
		// CELT decoder to accumulate libopus-exact int16/int24 output. The
		// dispatch is a no-op in the default build and on the float Decode path.
		if data != nil && !extsupport.QEXT {
			handled, fixedErr := d.celtDecodeFixedAPIRate(data, min(F20, frameSize), packetStereoLocal, celtBW, out)
			if fixedErr != nil {
				return 0, fixedErr
			}
			if !handled {
				d.markFixedUnhandled()
			}
		} else if data == nil && !extsupport.QEXT && !d.fixedCELTPLCHookSuppressed() {
			// CELT-only packet loss: run the integer FIXED_POINT celt_decode_lost
			// so the int16/int24 PLC output is bit-exact with opus_decode(NULL).
			// The integer decoder's cross-frame state was primed by the prior
			// received CELT frames; if it was not (no integer CELT history yet)
			// the helper declines and the float conversion is used.
			//
			// When this CELT frame is the recursive scratch decode of a Hybrid
			// transition (fixedCELTPLCHookSuppressed), the integer 5 ms CELT PLC
			// frame was already produced by fixedDecodeTransitionPLC; here we only
			// fill the float pcmTransition buffer and must not advance the integer
			// CELT PLC state again, so the hook is skipped and the frame declines.
			if !d.celtDecodeLostFixedAPIRate(min(F20, frameSize)) {
				d.markFixedUnhandled()
			}
		} else {
			d.markFixedUnhandled()
		}
	}

	if redundancy {
		// Redundancy post-processing rewrites the output after the main decode.
		// For a Hybrid frame handled by the integer path the equivalent
		// opus_res-domain redundancy decode + smooth_fade below keeps the
		// int16/int24 output bit-exact; otherwise the int16/int24 wrappers must
		// use the float conversion for this packet.
		if !fixedHybridFrame {
			d.markFixedUnhandled()
		}
		transition = false
		pcmTransition = nil
	}

	if redundancy && celtToSilk && len(redundantAudio) == 0 && data != nil && redundancyBytes > 0 && mainLen >= 0 && mainLen+redundancyBytes <= len(data) {
		redundantData := data[mainLen : mainLen+redundancyBytes]
		decoded, err := decodeRedundantCELT(redundantData)
		if err != nil {
			return 0, err
		}
		redundantAudio = decoded
	}

	if mode != ModeSILK && data == nil {
		// No extra work for PLC in CELT/Hybrid modes.
	} else if d.haveDecoded && mode == ModeSILK && d.prevMode == ModeHybrid && !(redundancy && celtToSilk && d.prevRedundancy) {
		samples, err := d.decodeCELTFrameToAPIScratch(celtSilenceFrame2B[:], F2_5, packetStereoLocal)
		if err != nil {
			return 0, err
		}
		addFloat32ToFloat32(out, samples)
	}

	if redundancy && !celtToSilk && data != nil && redundancyBytes > 0 && mainLen >= 0 && mainLen+redundancyBytes <= len(data) {
		d.celtDecoder.Reset()
		d.celtDecoder.SetBandwidth(celtBW)
		redundantData := data[mainLen : mainLen+redundancyBytes]
		// Mirror the reference on the integer CELT decoder: OPUS_RESET_STATE,
		// start band 0, decode the SILK->CELT redundancy frame, then the integer
		// opus_res smooth_fade onto the in-flight Hybrid frame.
		d.fixedDecodeRedundantCELT(redundantData, celtBW, true)
		decoded, err := decodeRedundantCELT(redundantData)
		if err != nil {
			return 0, err
		}
		redundantAudio = decoded
		start := (frameSize - F2_5) * channels
		if start >= 0 && start < len(out) && len(redundantAudio) >= F5*channels {
			smoothFade(out[start:], redundantAudio[F2_5*channels:], out[start:], F2_5, channels, fs)
		}
		if fixedHybridFrame {
			d.fixedApplyRedundancySilkToCelt(frameSize, fs)
		}
	}

	if redundancy && celtToSilk && (d.prevMode != ModeSILK || d.prevRedundancy) && len(redundantAudio) >= F5*channels {
		copy(out[:F2_5*channels], redundantAudio[:F2_5*channels])
		smoothFade(redundantAudio[F2_5*channels:], out[F2_5*channels:], out[F2_5*channels:], F2_5, channels, fs)
		if fixedHybridFrame {
			d.fixedApplyRedundancyCeltToSilk(frameSize, fs)
		}
	}

	if transition && len(pcmTransition) > 0 {
		if fixedHybridFrame {
			d.fixedApplyTransition(frameSize, audiosize, fs)
		} else {
			// The transition crossfade rewrites the float out buffer after the
			// main decode, so the integer-exact accumulation no longer matches.
			d.markFixedUnhandled()
		}
		if audiosize >= F5 {
			copy(out[:F2_5*channels], pcmTransition[:F2_5*channels])
			smoothFade(pcmTransition[F2_5*channels:], out[F2_5*channels:], out[F2_5*channels:], F2_5, channels, fs)
		} else {
			smoothFade(pcmTransition, out, out, F2_5, channels, fs)
		}
	}

	d.prevMode = mode
	d.prevRedundancy = redundancy && !celtToSilk
	d.haveDecoded = true
	d.redundantRng = redundantRng
	if frameLenLE1 {
		// Mirror opus_decode_frame's `if (len <= 1) st->rangeFinal = 0`: a PLC/DTX
		// frame contributes a zero final range regardless of any stale range-coder
		// state left over from the previous frame (the PLC path never re-inits the
		// range decoder). Zeroing both components keeps FinalRange()'s
		// mainDecodeRng ^ redundantRng at 0 for this frame, which is what
		// OPUS_GET_FINAL_RANGE reports for the last frame of the packet.
		d.mainDecodeRng = 0
		d.redundantRng = 0
	}
	// Note: d.lastDataLen is set at packet level in Decode(), not here

	if data != nil {
		d.fixedClearHybridFrame()
	}

	return audiosize, nil
}
