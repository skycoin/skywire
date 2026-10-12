package encoder

import (
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/types"
)

// hybridRateTable is the rate_table of compute_silk_rate_for_hybrid()
// (src/opus_encoder.c): the per-channel total rate and the SILK rate at 10 and
// 20 ms, without and with FEC.
var hybridRateTable = [...][5]int32{
	{0, 0, 0, 0, 0},
	{12000, 10000, 10000, 11000, 11000},
	{16000, 13500, 13500, 15000, 15000},
	{20000, 16000, 16000, 18000, 18000},
	{24000, 18000, 18000, 21000, 21000},
	{32000, 22000, 22000, 28000, 28000},
	{64000, 38000, 38000, 50000, 50000},
}

// computeSilkRateForHybrid ports compute_silk_rate_for_hybrid()
// (src/opus_encoder.c): the SILK share of a hybrid frame coded at rate.
func computeSilkRateForHybrid(rate int32, bw types.Bandwidth, frame20ms, vbr, fec bool, channels int32) int32 {
	rate /= channels
	entry := 1
	if frame20ms {
		entry++
	}
	if fec {
		entry += 2
	}
	i := 1
	for ; i < len(hybridRateTable); i++ {
		if hybridRateTable[i][0] > rate {
			break
		}
	}
	var silkRate int32
	if i == len(hybridRateTable) {
		silkRate = hybridRateTable[i-1][entry]
		// Half of the extra bits go to SILK.
		silkRate += (rate - hybridRateTable[i-1][0]) / 2
	} else {
		lo, hi := hybridRateTable[i-1][entry], hybridRateTable[i][entry]
		x0, x1 := hybridRateTable[i-1][0], hybridRateTable[i][0]
		silkRate = (lo*(x1-rate) + hi*(rate-x0)) / (x1 - x0)
	}
	if !vbr {
		// Tiny boost to SILK for CBR.
		silkRate += 100
	}
	if bw == types.BandwidthSuperwideband {
		silkRate += 300
	}
	silkRate *= channels
	// Small adjustment for stereo.
	if channels == 2 && rate >= 12000 {
		silkRate -= 1000
	}
	return silkRate
}

// computeRedundancyBytes ports compute_redundancy_bytes() (src/opus_encoder.c):
// the size of the redundant CELT frame of a mode switch, 0 when it cannot get
// enough bits to be worth coding.
func computeRedundancyBytes(maxDataBytes, bitrateBps, frameRate, channels int32) int32 {
	baseBits := 40*channels + 20
	// Equivalent rate for 5 ms frames, raised by half for VBR.
	redundancyRate := bitrateBps + baseBits*(200-frameRate)
	redundancyRate = 3 * redundancyRate / 2
	redundancyBytes := redundancyRate / 1600

	// The max rate CBR or VBR with a cap allows.
	availableBits := maxDataBytes*8 - 2*baseBits
	redundancyBytesCap := (availableBits*240/(240+48000/frameRate) + baseBits) / 8
	redundancyBytes = min(redundancyBytes, redundancyBytesCap)
	if redundancyBytes > 4+8*channels {
		return min(257, redundancyBytes)
	}
	return 0
}

// clampRedundancyBytesAfterSilk follows opus_encode_frame_native after the
// redundancy direction bit. maxDataBytes includes the TOC; tellBits includes
// the direction bit. Hybrid reserves its length byte and three CELT bits.
func clampRedundancyBytesAfterSilk(maxDataBytes, tellBits, redundancyBytes int32, hybrid bool) int32 {
	reserveBits := tellBits
	if hybrid {
		reserveBits += 8 + 3
	}
	available := maxDataBytes - 1 - (reserveBits+7)/8
	return min(257, max(2, min(available, redundancyBytes)))
}

// frameRequest carries the arguments of one opus_encode_frame_native call.
// The frame codes at e.bitrate (st->bitrate_bps, after any DRED reservation).
type frameRequest struct {
	mode         Mode
	frameSize    int
	maxDataBytes int   // orig_max_data_bytes
	dredBitrate  int   // dred_bitrate_bps
	equivRate    int32 // the packet's equiv_rate
	// prevMode is st->prev_mode as this frame sees it: a CELT or hybrid frame
	// that follows another mode resets and prefills CELT.
	prevMode   Mode
	redundancy bool
	celtToSILK bool
}

// codedFrame is the result of one opus_encode_frame_native call.
type codedFrame struct {
	// data is the frame after the TOC byte: the range-coded payload followed
	// by any redundant CELT frame, or a single zero byte (PLC frame) when SILK
	// busted the budget.
	data []byte
	// bw is curr_bandwidth, the bandwidth the TOC signals.
	bw types.Bandwidth
	// dtx reports that silk_Encode returned no payload: the frame is TOC-only
	// and ends before the delay buffer, the high-band gain and the stereo width
	// advance (src/opus_encoder.c:2242-2248).
	dtx bool
}

// encodeFrameNative ports opus_encode_frame_native (src/opus_encoder.c) up to
// the TOC: SILK codes first, then the redundancy signalling, the CELT
// controls, the mode-transition CELT prefill and CELT on the same range coder,
// with the redundant CELT frames of a mode switch around it. pcm is the
// frame's high-pass filtered input. The caller runs the DTX decision, builds
// the TOC and pads the packet. e.frameFinalRange receives st->rangeFinal.
func (e *Encoder) encodeFrameNative(pcm []opusRes, req frameRequest) (codedFrame, error) {
	mode := req.mode
	frameSize := req.frameSize
	fs := e.sampleRate
	channels := int(e.channels)
	streamChannels := e.streamChannels
	maxDataBytes := int32(min(req.maxDataBytes, libopusMaxDataBytesCap))
	frameRate := fs / int32(frameSize)
	hybrid := mode == ModeHybrid
	e.frameFinalRange = 0
	e.clearFixedCELTUsed()
	if mode == ModeSILK {
		// A SILK frame does not consume the CELT transition prefill staged by
		// the SILK onset ramp. Drop it so a later SILK-to-Hybrid transition
		// captures the current delay-buffer window instead of reusing it.
		e.consumeFixedCELTPrefill()
	}
	currBW := e.effectiveBandwidth()

	redundancy, celtToSILK := req.redundancy, req.celtToSILK
	prefill := 0
	if e.silkPrefillPending {
		prefill = 1
	}
	// For the first frame at a new SILK bandwidth: a CELT->SILK redundant
	// frame and a prefill that keeps the sampling rate control.
	if e.silkBWSwitch {
		redundancy = true
		celtToSILK = true
		e.silkBWSwitch = false
		prefill = 2
	}
	// A CELT frame never carries redundancy.
	if mode == ModeCELT {
		redundancy = false
	}
	var redundancyBytes int32
	if redundancy {
		redundancyBytes = computeRedundancyBytes(maxDataBytes, e.bitrate, frameRate, streamChannels)
		redundancy = redundancyBytes != 0
	}
	if e.restrictedSilkApp {
		redundancy = false
		redundancyBytes = 0
	}
	bitsTarget := min(8*(maxDataBytes-redundancyBytes), int32(bitrateToBitsFs(int(e.bitrate), int(fs), frameSize))) - 8

	payload := e.ensureFramePayload(req.maxDataBytes - 1)
	re := &e.frameRangeEncoder
	re.Init(payload)

	celtPCM := e.pcmBuf(pcm, frameSize)
	e.prepareFixedCELTPCM(frameSize)
	if mode != ModeSILK && mode != req.prevMode && isConcreteMode(req.prevMode) {
		e.captureFixedCELTTransitionPrefill()
	}

	hbGain := opusVal16(1)
	fixedHBGainQ15 := int16(1<<15 - 1)
	var silkBitRate int32
	if mode != ModeCELT {
		e.ensureSILKEncoder()
		// Distribute the bits between SILK and CELT.
		totalBitRate := int32(bitsToBitrateFs(int(bitsTarget), int(fs), frameSize))
		frame20ms := fs == 50*int32(frameSize)
		vbr := e.bitrateMode != ModeCBR
		if hybrid {
			silkBitRate = computeSilkRateForHybrid(totalBitRate, currBW, frame20ms, vbr, e.lbrrCoded, streamChannels)
			if len(e.celtEnergyMask) == 0 {
				celtRate := totalBitRate - silkBitRate
				hbGain = hybridHBGain(celtRate)
				fixedHBGainQ15 = e.fixedHighBandGainQ15(celtRate)
			}
		} else {
			silkBitRate = totalBitRate
		}
		if len(e.celtEnergyMask) > 0 && vbr && !e.lfe {
			silkBitRate += e.silkSurroundRateOffset(silkBitRate)
		}

		// Max bits for SILK, counting the TOC, the redundant frame and 1 bit
		// for its position, plus 20 bits for its flag and size in hybrid.
		maxBits := (maxDataBytes - 1) * 8
		if redundancy && redundancyBytes >= 2 {
			maxBits -= redundancyBytes*8 + 1
			if hybrid {
				maxBits -= 20
			}
		}
		useCBR := !vbr
		if useCBR {
			// CBR with non-SILK data codes SILK as VBR with a cap: SILK may
			// take up to a quarter of the remaining bits, CELT and DRED absorb
			// the variation.
			if hybrid || req.dredBitrate > 0 {
				otherBits := int32(int16(max(0, maxBits-silkBitRate*int32(frameSize)/fs)))
				maxBits = max(0, maxBits-otherBits*3/4)
				useCBR = false
			}
		} else if hybrid {
			// Constrained VBR: the SILK rate the max total bits allow.
			maxBitRate := computeSilkRateForHybrid(maxBits*fs/int32(frameSize), currBW, frame20ms, vbr, e.lbrrCoded, streamChannels)
			maxBits = int32(bitrateToBitsFs(int(maxBitRate), int(fs), frameSize))
		}
		e.configureSILKMode(mode, frameSize, int(maxDataBytes), int(silkBitRate), int(maxBits), useCBR)
		activity := e.silkActivity()
		if err := e.runPendingSILKPrefill(prefill, activity); err != nil {
			return codedFrame{}, err
		}
		nBytes, err := e.encodeSILKFrame(pcm, frameSize, re, activity)
		if hybrid {
			recordHybridCoderBoundary(re, hybridCoderBoundarySILKExit)
		}
		if err != nil {
			return codedFrame{}, err
		}
		// A SILK-only TOC signals the SILK internal bandwidth.
		if mode == ModeSILK {
			currBW = silkInternalBandwidth(e.silkMode.InternalSampleRate)
		}
		e.silkMode.OpusCanSwitch = e.silkMode.SwitchReady && !e.nonfinalFrame
		if nBytes == 0 {
			return codedFrame{bw: currBW, dtx: true}, nil
		}
		if e.silkMode.OpusCanSwitch {
			if !e.restrictedSilkApp {
				redundancyBytes = computeRedundancyBytes(maxDataBytes, e.bitrate, frameRate, streamChannels)
				redundancy = redundancyBytes != 0
			}
			celtToSILK = false
			e.silkBWSwitch = true
		}
	}

	// CELT controls of every frame.
	if !e.restrictedSilkApp {
		e.ensureCELTEncoder()
		if mode != ModeCELT {
			e.setCELTQEXTEnabled(false)
		}
		e.celtEncoder.SetBandwidth(celtBandwidthFromTypes(currBW))
		e.celtEncoder.SetStreamChannels(int(streamChannels))
		e.celtEncoder.SetBitrate(celt.BitrateMax)
		e.celtEncoder.SetLSBDepth(int(e.lsbDepth))
		if mode != ModeSILK {
			e.celtEncoder.SetPrediction(e.celtPredictionMode())
		}
	}

	// The delay buffer takes the frame; the high-band gain and stereo width
	// fades then run on pcm_buf so that they do not affect SILK.
	e.updateDelayBuffer(pcm, frameSize)
	e.updateFixedDelayBuffer(frameSize)
	e.fadeHighBand(celtPCM, hbGain)
	e.fadeFixedHighBand(fixedHBGainQ15)
	prevFixedWidth := e.hybridStereoWidthQ14
	e.applyStereoWidth(mode, celtPCM, req.equivRate)
	e.applyFixedStereoWidth(prevFixedWidth)

	// The redundancy flag, position and size.
	signalBits := 17
	if hybrid {
		signalBits += 20
	}
	if mode != ModeCELT && re.Tell()+signalBits <= 8*int(maxDataBytes-1) {
		// SILK-only frames infer the redundancy from the length.
		if hybrid {
			re.EncodeBit(boolToInt(redundancy), 12)
		}
		if redundancy {
			re.EncodeBit(boolToInt(celtToSILK), 1)
			redundancyBytes = clampRedundancyBytesAfterSilk(maxDataBytes, int32(re.Tell()), redundancyBytes, hybrid)
			if hybrid {
				re.EncodeUniform(uint32(redundancyBytes-2), 256)
			}
		}
	} else {
		redundancy = false
	}
	if !redundancy {
		e.silkBWSwitch = false
		redundancyBytes = 0
	}

	var nbComprBytes int
	if mode == ModeSILK {
		nbComprBytes = (re.Tell() + 7) >> 3
		e.frameFinalRange = re.Range()
		re.Done()
	} else {
		nbComprBytes = int(maxDataBytes-1) - int(redundancyBytes)
		if extsupport.QEXT && mode == ModeCELT && e.qextActive() {
			nbComprBytes = req.maxDataBytes - 1
		}
		if req.dredBitrate > 0 {
			dredBytes := bitrateToBitsFs(req.dredBitrate, int(fs), frameSize) / 8
			// CELT may take up to 25% of the bits left to DRED, but keeps at
			// least 5 bytes so that the redundancy signalling stays in sync.
			maxCELTBytes := max((re.Tell()+7)/8+5, nbComprBytes-dredBytes*3/4)
			nbComprBytes = min(nbComprBytes, maxCELTBytes)
		}
		re.Shrink(uint32(nbComprBytes))
	}

	if redundancy || mode != ModeSILK {
		e.syncCELTAnalysisToCELT()
	}
	if hybrid {
		e.celtEncoder.SetSilkInfo(int(e.silkMode.SignalType), int(e.silkMode.Offset))
	}

	// The 5 ms redundant frame that starts a switch from CELT.
	var redundant []byte
	var redundantRng uint32
	if redundancy && celtToSILK {
		e.celtEncoder.SetHybrid(false)
		e.celtEncoder.SetVBR(false)
		e.celtEncoder.SetBitrate(celt.BitrateMax)
		n2 := int(fs) / 200
		var err error
		redundant, redundantRng, err = e.encodeRedundantCELTFrame(celtPCM[:n2*channels],
			e.fixedFrameSliceQ8(0, n2), n2, int(redundancyBytes), hybrid, true)
		if err != nil {
			return codedFrame{}, err
		}
		e.celtEncoder.Reset()
		e.resetFixedCELTState()
	}

	if !e.restrictedSilkApp {
		e.celtEncoder.SetHybrid(mode != ModeCELT)
	}

	var frame []byte
	if mode != ModeSILK {
		e.configureCELTRate(mode, silkBitRate)
		prefillPrediction := int32(e.celtEncoder.Prediction())
		prefilled := e.prefillCELTOnModeSwitch(mode, req.prevMode)
		fixedPrefilled := !prefilled
		if prefilled {
			prefillFrameSize := int(fs) / 400
			startBand := 0
			if hybrid {
				startBand = 17
			}
			if prefill := e.fixedCELTTransitionPrefillInputQ8(prefillFrameSize); prefill != nil {
				fixedPrefilled = e.prefillCELTFrameFixed(prefill, prefillFrameSize, startBand,
					e.celtEncoder.Bitrate(), 2, prefillPrediction)
			}
			e.consumeFixedCELTPrefill()
		}
		// A frame whose SILK part busted the budget becomes a PLC frame
		// without CELT.
		if re.Tell() <= 8*nbComprBytes {
			var err error
			if hybrid {
				recordHybridCoderBoundary(re, hybridCoderBoundaryCELTEntry)
			}
			if hybrid {
				if fixedPCM := e.fixedHybridCELTPCMQ8(frameSize); fixedPCM != nil && fixedPrefilled {
					var ok bool
					frame, ok, err = e.encodeHybridCELTFrameFixed(fixedPCM, frameSize, e.celtEncoder.Bitrate(), nbComprBytes, re, prefilled)
					if !ok && err == nil {
						frame, err = e.celtEncoder.EncodeWithEC(celtPCM, frameSize, nbComprBytes, re)
					}
				} else {
					frame, err = e.celtEncoder.EncodeWithEC(celtPCM, frameSize, nbComprBytes, re)
				}
			} else {
				frame, err = e.encodeCELTOnlyFrame(celtPCM, frameSize, nbComprBytes,
					prefilled, fixedPrefilled)
			}
			if hybrid {
				recordHybridCoderBoundary(re, hybridCoderBoundaryCELTExit)
			}
			if err != nil {
				return codedFrame{}, err
			}
		}
		e.frameFinalRange = e.celtEncoder.FinalRange()
		if r, ok := e.fixedCELTFinalRange(); ok && (mode == ModeCELT || hybrid) {
			e.frameFinalRange = r
		}
	}

	// The 5 ms redundant frame that ends a switch to CELT.
	if redundancy && !celtToSILK {
		e.celtEncoder.Reset()
		e.celtEncoder.SetHybrid(false)
		e.celtEncoder.SetPrediction(0)
		e.celtEncoder.SetVBR(false)
		e.celtEncoder.SetBitrate(celt.BitrateMax)
		n2 := int(fs) / 200
		n4 := int(fs) / 400
		start := (frameSize - n2 - n4) * channels
		e.celtEncoder.SetMaxPayloadBytes(2)
		_, _ = e.celtEncoder.EncodeFrame(celtPCM[start:start+n4*channels], n4)
		e.celtEncoder.SetMaxPayloadBytes(0)
		if prefill := e.fixedFrameSliceQ8(frameSize-n2-n4, n4); prefill != nil {
			e.prefillCELTFrameFixed(prefill, n4, 0, e.celtEncoder.Bitrate(), 2, 0)
		}
		start = (frameSize - n2) * channels
		var err error
		redundant, redundantRng, err = e.encodeRedundantCELTFrame(celtPCM[start:start+n2*channels],
			e.fixedFrameSliceQ8(frameSize-n2, n2), n2, int(redundancyBytes), false, false)
		if err != nil {
			return codedFrame{}, err
		}
	}
	e.frameFinalRange ^= redundantRng

	// SILK busted its target: a single zero byte makes the decoder run the
	// PLC.
	if re.Tell() > int(maxDataBytes-1)*8 {
		e.frameFinalRange = 0
		payload[0] = 0
		return codedFrame{data: payload[:1], bw: currBW}, nil
	}
	if mode == ModeSILK {
		frame = payload[:nbComprBytes]
		if !redundancy {
			// A SILK-only frame drops trailing zero bytes, which the range
			// decoder fills in.
			frame = trimSilkTrailingZeros(frame)
		}
	}
	if redundancy {
		n := len(frame)
		frame = payload[:n+len(redundant)]
		copy(frame[n:], redundant)
	}
	return codedFrame{data: frame, bw: currBW}, nil
}

// pcmBuf returns pcm_buf of opus_encode_frame_native: the delay history
// ahead of the frame followed by the start of the frame, or the frame alone
// when the application codes without delay compensation (restricted low
// delay). It leaves the delay buffer as it is.
func (e *Encoder) pcmBuf(pcm []opusRes, frameSize int) []opusRes {
	if !e.lowDelay {
		return e.delayCompensatedPCM(pcm, frameSize)
	}
	out := e.ensureDelayedPCM(frameSize * int(e.channels))
	copy(out, pcm)
	return out
}

// hybridHBGain returns the gain that increasingly attenuates the high band of
// a hybrid frame as it gets fewer bits: 1 - 2^(-celtRate/1024)
// (src/opus_encoder.c:2056-2061).
func hybridHBGain(celtRate int32) opusVal16 {
	return 1 - opusmath.CeltExp2(-float32(celtRate)*(1.0/1024))
}

// fadeHighBand runs gain_fade() on pcm_buf from the previous frame's
// high-band gain to hbGain when either is below one, and records hbGain as
// prev_HB_gain (src/opus_encoder.c:2311-2316).
func (e *Encoder) fadeHighBand(pcm []opusRes, hbGain opusVal16) {
	if !e.restrictedSilkApp && (e.prevHBGain < 1 || hbGain < 1) {
		e.applyGainFade(pcm, e.prevHBGain, hbGain)
	}
	e.prevHBGain = hbGain
}

// prefillCELTOnModeSwitch resets CELT and prefills it with the Fs/400
// samples of delay history ahead of the frame (tmp_prefill) when a CELT-only
// or hybrid frame follows another mode; the frame then codes without
// prediction (src/opus_encoder.c:2477-2486). It runs with the frame's CELT
// controls and reports whether it ran.
func (e *Encoder) prefillCELTOnModeSwitch(mode, prevMode Mode) bool {
	defer func() { e.hasCELTPrefill = false }()
	if mode == prevMode || !isConcreteMode(prevMode) || e.lowDelay {
		return false
	}
	e.setCELTQEXTEnabled(false)
	e.celtEncoder.Reset()
	n4 := int(e.sampleRate) / 400
	if src := e.celtTransitionPrefillSource(n4 * int(e.channels)); src != nil {
		e.celtEncoder.SetMaxPayloadBytes(2)
		_, _ = e.celtEncoder.EncodeFrame(src, n4)
		e.celtEncoder.SetMaxPayloadBytes(0)
	}
	e.celtEncoder.SetPrediction(0)
	return true
}

// encodeRedundantCELTFrame codes a 5 ms redundant CELT frame of n samples at
// the current CELT controls into its own range coder, redundancyBytes long,
// and returns a copy that outlives the next CELT encode.
func (e *Encoder) encodeRedundantCELTFrame(pcm []opusRes, pcmQ8 []int32, n, redundancyBytes int, hybrid, analysis bool) ([]byte, uint32, error) {
	e.setCELTQEXTEnabled(false)
	e.celtEncoder.SetMaxPayloadBytes(redundancyBytes)
	if len(pcmQ8) == n*int(e.channels) {
		if data, finalRange, ok, err := e.encodeRedundantCELTFrameFixed(pcmQ8, n,
			e.celtEncoder.Bitrate(), redundancyBytes, hybrid, analysis); ok || err != nil {
			e.celtEncoder.SetMaxPayloadBytes(0)
			if err != nil {
				return nil, 0, err
			}
			return append(e.redundancyScratch[:0], data...), finalRange, nil
		}
	}
	data, err := e.celtEncoder.EncodeFrame(pcm, n)
	e.celtEncoder.SetMaxPayloadBytes(0)
	if err != nil {
		return nil, 0, err
	}
	return append(e.redundancyScratch[:0], data...), e.celtEncoder.FinalRange(), nil
}

// encodeCELTOnlyFrame codes a CELT-only frame to the nbComprBytes budget: the
// integer CELT encoder under the gopus_fixed_point build, the float one
// otherwise.
func (e *Encoder) encodeCELTOnlyFrame(pcm []opusRes, frameSize, nbComprBytes int, prefilled, fixedReady bool) ([]byte, error) {
	e.setCELTQEXTEnabled(extsupport.QEXT && e.qextActive())
	e.celtEncoder.SetMaxPayloadBytes(nbComprBytes)
	defer e.celtEncoder.SetMaxPayloadBytes(0)
	if fixedReady {
		if out, ok, err := e.encodeCELTFrameFixed(pcm, frameSize, e.celtEncoder.Bitrate(), nbComprBytes, prefilled); ok || err != nil {
			return out, err
		}
	}
	return e.celtEncoder.EncodeFrame(pcm, frameSize)
}

// silkSurroundRateOffset ports the surround masking rate change of the SILK
// part (src/opus_encoder.c:2069-2106): the energy mask of the SILK bands,
// halved for a conservative reduction, moves the SILK rate, and a hybrid
// frame gives SILK 3/5 of the change.
func (e *Encoder) silkSurroundRateOffset(silkBitRate int32) int32 {
	if offset, ok := e.fixedSilkSurroundRateOffset(silkBitRate); ok {
		return offset
	}
	end := 17
	srate := float32(16000)
	switch e.bandwidth {
	case types.BandwidthNarrowband:
		end = 13
		srate = 8000
	case types.BandwidthMediumband:
		end = 15
		srate = 12000
	}
	var maskSum float32
	for c := range int(e.channels) {
		for i := range end {
			idx := 21*c + i
			if idx >= len(e.celtEnergyMask) {
				continue
			}
			mask := max(min(e.celtEnergyMask[idx], 0.5), -2.0)
			if mask > 0 {
				mask *= 0.5
			}
			maskSum += mask
		}
	}
	maskingDepth := maskSum / float32(end) * float32(e.channels)
	maskingDepth += 0.2
	rateOffset := int32(srate * maskingDepth)
	rateOffset = max(rateOffset, -2*silkBitRate/3)
	if e.bandwidth == types.BandwidthSuperwideband || e.bandwidth == types.BandwidthFullband {
		return 3 * rateOffset / 5
	}
	return rateOffset
}

// ensureFramePayload returns the range-coder buffer of a frame, n bytes long
// (orig_max_data_bytes-1).
func (e *Encoder) ensureFramePayload(n int) []byte {
	n = max(n, 1)
	if cap(e.framePayload) < n {
		e.framePayload = make([]byte, n)
	}
	return e.framePayload[:n]
}

// celtBandwidthFromTypes maps the frame's bandwidth to the CELT bandwidth that
// sets the end band opus_encode_frame_native hands CELT (CELT_SET_END_BAND):
// 13 for narrowband, 17 for mediumband and wideband, 19 for superwideband and
// 21 for fullband.
func celtBandwidthFromTypes(bw types.Bandwidth) celt.CELTBandwidth {
	switch bw {
	case types.BandwidthNarrowband:
		return celt.CELTNarrowband
	case types.BandwidthMediumband, types.BandwidthWideband:
		return celt.CELTWideband
	case types.BandwidthSuperwideband:
		return celt.CELTSuperwideband
	default:
		return celt.CELTFullband
	}
}

// configureCELTRate sets the CELT rate controls of a CELT-only or hybrid
// frame (src/opus_encoder.c:2447-2476): VBR follows the stream; a VBR hybrid
// frame hands CELT the rate SILK leaves, unconstrained, and a VBR CELT-only
// frame the whole rate with the stream's constraint. CBR frames keep
// OPUS_BITRATE_MAX and fill nb_compr_bytes. A CBR stream carrying DRED codes
// its CELT part as unconstrained VBR so the DRED payload absorbs the slack.
func (e *Encoder) configureCELTRate(mode Mode, silkBitRate int32) {
	vbr := e.bitrateMode != ModeCBR
	e.celtEncoder.SetVBR(vbr)
	if vbr {
		if mode == ModeHybrid {
			e.setCELTBitrate(int(e.bitrate - silkBitRate))
			e.celtEncoder.SetConstrainedVBR(false)
		} else {
			e.celtEncoder.SetConstrainedVBR(e.bitrateMode == ModeCVBR)
			e.setCELTBitrate(int(e.bitrate))
		}
	}
	if !vbr && e.dredEncodingActive() {
		celtBitrate := e.bitrate
		if mode == ModeHybrid {
			celtBitrate -= silkBitRate
		}
		e.celtEncoder.SetVBR(true)
		e.celtEncoder.SetConstrainedVBR(false)
		e.setCELTBitrate(int(celtBitrate))
	}
}

// setCELTBitrate applies OPUS_SET_BITRATE to the CELT encoder with the checks
// of celt_encoder_ctl (celt/celt_encoder.c:3001-3009): a rate of 500 b/s or less
// is rejected and leaves the current rate in place, and the rate is capped at
// 750 kb/s per channel.
func (e *Encoder) setCELTBitrate(bitrate int) {
	if bitrate <= 500 && bitrate != celt.BitrateMax {
		return
	}
	e.celtEncoder.SetBitrate(min(bitrate, 750000*int(e.channels)))
}

// applyStereoWidth runs the stereo width block of opus_encode_frame_native
// (src/opus_encoder.c:2320-2348). SILK-only and CELT-only frames, and hybrid
// frames coded as one stream channel, take silk_mode.stereoWidth_Q14 from the
// packet's equiv_rate; stereo hybrid frames keep the smoothed width silk_Encode
// reported. Without an energy mask, when the previously applied width or the
// new one is below full width, stereo_fade() runs on pcm (pcm_buf, modified in
// place) and hybrid_stereo_width_Q14 records the width.
func (e *Encoder) applyStereoWidth(mode Mode, pcm []opusRes, equivRate int32) {
	if mode != ModeHybrid || e.streamChannels == 1 {
		switch {
		case equivRate > 32000:
			e.silkMode.StereoWidthQ14 = 16384
		case equivRate < 16000:
			e.silkMode.StereoWidthQ14 = 0
		default:
			e.silkMode.StereoWidthQ14 = 16384 - 2048*(32000-equivRate)/(equivRate-14000)
		}
	}
	if len(e.celtEnergyMask) > 0 || e.channels != 2 {
		return
	}
	widthQ14 := int16(e.silkMode.StereoWidthQ14)
	if e.hybridStereoWidthQ14 < 1<<14 || widthQ14 < 1<<14 {
		if !e.restrictedSilkApp {
			e.applyStereoFade(pcm, e.hybridStereoWidthQ14, widthQ14)
		}
		e.hybridStereoWidthQ14 = widthQ14
	}
}

// applyGainFade ports gain_fade() (src/opus_encoder.c): across the CELT
// overlap (sampled at window[i*inc] with inc = 48000/Fs) the gain moves from
// g1 to g2 with the squared window, and the rest of the frame takes g2.
func (e *Encoder) applyGainFade(samples []opusRes, g1, g2 opusVal16) {
	channels := int(e.channels)
	frameSize := len(samples) / channels
	inc := max(48000/int(e.sampleRate), 1)
	overlapSize := celt.Overlap
	if e.sampleRate == 96000 {
		overlapSize = 240
	}
	overlap := min(overlapSize/inc, frameSize)
	window := celt.GetWindowBufferF32(overlapSize)
	for i := range overlap {
		w := opusVal16(window[i*inc])
		w = round32(w * w)
		g := fma32(w, g2, round32((1-w)*g1))
		for c := range channels {
			samples[i*channels+c] = g * samples[i*channels+c]
		}
	}
	for i := overlap * channels; i < frameSize*channels; i++ {
		samples[i] = g2 * samples[i]
	}
}

// applyStereoFade ports stereo_fade() (src/opus_encoder.c) for the widths
// g1 = widthQ14Prev/16384 and g2 = widthQ14/16384: the side channel is scaled
// by 1-g, which moves from 1-g1 to 1-g2 across the overlap.
func (e *Encoder) applyStereoFade(samples []opusRes, widthQ14Prev, widthQ14 int16) {
	frameSize := len(samples) / 2
	g1 := 1 - opusVal16(widthQ14Prev)*(1.0/16384)
	g2 := 1 - opusVal16(widthQ14)*(1.0/16384)
	inc := max(48000/int(e.sampleRate), 1)
	overlapSize := celt.Overlap
	if e.sampleRate == 96000 {
		overlapSize = 240
	}
	overlap := min(overlapSize/inc, frameSize)
	window := celt.GetWindowBufferF32(overlapSize)
	for i := range overlap {
		// opus_encoder.c stereo_fade rounds w*w and the first gain product;
		// the second product is added with the target's natural contraction.
		w := opusVal16(window[i*inc])
		w = round32(w * w)
		g := fma32(w, g2, round32((1-w)*g1))
		diff := round32(opusVal32(0.5) * (samples[i*2] - samples[i*2+1]))
		if outerTargetV3FMA {
			// GCC contracts each channel update with g*diff in stereo_fade
			// (src/opus_encoder.c) for AMD64 v3. Keep the shared half-difference
			// rounded before both fused updates.
			samples[i*2] = opusRes(fma32(-g, diff, float32(samples[i*2])))
			samples[i*2+1] = opusRes(fma32(g, diff, float32(samples[i*2+1])))
		} else {
			diff = round32(g * diff)
			samples[i*2] -= diff
			samples[i*2+1] += diff
		}
	}
	for i := overlap; i < frameSize; i++ {
		diff := round32(opusVal32(0.5) * (samples[i*2] - samples[i*2+1]))
		if outerTargetV3FMA && !outerTargetV3SIMDFadeTail {
			// The scalar v3 archive contracts each channel update with g2*diff.
			samples[i*2] = opusRes(fma32(-g2, diff, float32(samples[i*2])))
			samples[i*2+1] = opusRes(fma32(g2, diff, float32(samples[i*2+1])))
		} else {
			// The ordinary SIMD archive rounds the gain product before updating
			// either channel in the vectorized steady-width loop.
			diff = round32(g2 * diff)
			samples[i*2] -= diff
			samples[i*2+1] += diff
		}
	}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
