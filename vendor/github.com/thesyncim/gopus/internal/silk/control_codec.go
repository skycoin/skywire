package silk

// control is silk_control_encoder (silk/control_codec.c), run on each channel
// at the start of every silk_Encode call. It takes the packet's controls and,
// unless the channel is part way through a packet outside a prefill, picks the
// internal sampling rate (silk_control_audio_bandwidth), carries the resampler
// and the analysis buffer over to it, and sets the packet size, complexity,
// packet loss and LBRR. allowBWSwitch is the packet encoder's
// allowBandwidthSwitch of the previous packet; a non-zero forceFsKHz overrides
// the rate, which keeps the side channel at the rate of the mid channel.
func (e *Encoder) control(ctl *EncControl, allowBWSwitch bool, forceFsKHz int32) {
	e.useDTX = ctl.UseDTX
	e.useCBR = ctl.UseCBR
	e.apiFsHz = ctl.APISampleRate
	e.maxInternalFsHz = ctl.MaxInternalSampleRate
	e.minInternalFsHz = ctl.MinInternalSampleRate
	e.desiredInternalFsHz = ctl.DesiredInternalSampleRate
	e.allowBandwidthSwitch = allowBWSwitch

	if e.controlledSinceLastPayload && !e.prefillFlag {
		if e.apiFsHz != e.prevAPIFsHz && e.fsKHz > 0 {
			// Change in API sampling rate in the middle of encoding a packet.
			e.setupResamplers(e.fsKHz)
		}
		return
	}

	// No previously coded frames are in the payload buffer from here on.
	fsKHz := e.controlAudioBandwidth(ctl)
	if forceFsKHz != 0 {
		fsKHz = forceFsKHz
	}
	e.setupResamplers(fsKHz)
	e.setupFs(fsKHz, ctl.PayloadSizeMs)
	e.setupComplexity(ctl.Complexity)
	e.packetLossPercent = ctl.PacketLossPercentage
	e.setupLBRR(ctl.LBRRCoded)
	e.controlledSinceLastPayload = true
}

// controlAudioBandwidth is silk_control_audio_bandwidth
// (silk/control_audio_bandwidth.c): it returns the internal sampling rate in
// kHz for the next packet. A new encoder starts at the desired rate and a rate
// outside the API rate or the min/max limits moves straight inside them.
// Otherwise the rate moves one step at a time towards the desired rate, only
// while switching is allowed: a switch down first fades the band out with the
// variable LP filter, a switch up waits until the Opus encoder can switch, and
// the variable LP filter then fades the band in. When the encoder is ready to
// switch but the Opus encoder has not allowed it yet, it sets switchReady and
// lowers maxBits to make room for the redundant CELT frame of the switch.
func (e *Encoder) controlAudioBandwidth(ctl *EncControl) int32 {
	origKHz := e.fsKHz
	// Handle a bandwidth-switching reset where we need to be aware what the
	// last sampling rate was.
	if origKHz == 0 {
		origKHz = e.lpState.SavedFsKHz
	}
	fsKHz := origKHz
	fsHz := fsKHz * 1000
	switch {
	case fsHz == 0:
		// Encoder has just been initialized.
		fsHz = min(e.desiredInternalFsHz, e.apiFsHz)
		fsKHz = fsHz / 1000
	case fsHz > e.apiFsHz || fsHz > e.maxInternalFsHz || fsHz < e.minInternalFsHz:
		// Make sure the internal rate is not higher than the external rate or
		// the maximum allowed, or lower than the minimum allowed.
		fsHz = max(min(e.apiFsHz, e.maxInternalFsHz), e.minInternalFsHz)
		fsKHz = fsHz / 1000
	default:
		// State machine for the internal sampling rate switching.
		lp := &e.lpState
		if lp.TransitionFrameNo >= transitionFrames {
			// Stop transition phase.
			lp.Mode = 0
		}
		if e.allowBandwidthSwitch || ctl.OpusCanSwitch {
			fsKHz = e.switchInternalRate(ctl, origKHz)
		}
	}
	return fsKHz
}

// switchInternalRate is the switching step of silk_control_audio_bandwidth
// (silk/control_audio_bandwidth.c), run while a bandwidth switch is allowed.
// It returns the internal rate in kHz for the next packet.
func (e *Encoder) switchInternalRate(ctl *EncControl, origKHz int32) int32 {
	lp := &e.lpState
	switch {
	case origKHz*1000 > e.desiredInternalFsHz:
		// Switch down.
		if lp.Mode == 0 {
			// New transition.
			lp.TransitionFrameNo = transitionFrames
			// Reset transition filter state.
			lp.InLPState = [2]int32{}
		}
		if ctl.OpusCanSwitch {
			// Stop transition phase.
			lp.Mode = 0
			// Switch to a lower sample frequency.
			if origKHz == 16 {
				return 12
			}
			return 8
		}
		if lp.TransitionFrameNo <= 0 {
			ctl.SwitchReady = true
			// Make room for redundancy.
			ctl.MaxBits -= ctl.MaxBits * 5 / (ctl.PayloadSizeMs + 5)
		} else {
			// Direction: down (at double speed).
			lp.Mode = -2
		}
	case origKHz*1000 < e.desiredInternalFsHz:
		// Switch up.
		if ctl.OpusCanSwitch {
			// New transition.
			lp.TransitionFrameNo = 0
			// Reset transition filter state.
			lp.InLPState = [2]int32{}
			// Direction: up.
			lp.Mode = 1
			// Switch to a higher sample frequency.
			if origKHz == 8 {
				return 12
			}
			return 16
		}
		if lp.Mode == 0 {
			ctl.SwitchReady = true
			// Make room for redundancy.
			ctl.MaxBits -= ctl.MaxBits * 5 / (ctl.PayloadSizeMs + 5)
		} else {
			// Direction: up.
			lp.Mode = 1
		}
	case lp.Mode < 0:
		lp.Mode = 1
	}
	return origKHz
}

// setupResamplers is silk_setup_resamplers (silk/control_codec.c). When the
// internal rate or the API rate changes, it re-initializes the API-rate to
// internal-rate input resampler. An encoder already running at a rate also
// carries its analysis buffer x_buf over: x_buf is resampled up to the API
// rate and back down through the new input resampler, which leaves x_buf at
// the new rate and the resampler primed with the buffered signal.
func (e *Encoder) setupResamplers(fsKHz int32) {
	if e.fsKHz != fsKHz || e.prevAPIFsHz != e.apiFsHz {
		if e.fsKHz == 0 {
			// Initialize the resampler for silk_Encode preparing resampling
			// from API_fs_Hz to fs_kHz.
			e.resampler.init(int(e.apiFsHz), int(fsKHz)*1000, true)
		} else {
			bufLengthMs := (e.nbSubfr*5)<<1 + laShapeMs
			oldBufSamples := bufLengthMs * e.fsKHz
			newBufSamples := bufLengthMs * fsKHz
			xBuf := ensureInt16Slice(&e.xBufFix, int(max(oldBufSamples, newBufSamples)))
			e.xBufToInt16(xBuf[:oldBufSamples])

			// Temporary resampling of x_buf data to API_fs_Hz.
			e.xBufAPIResample.init(int(e.fsKHz)*1000, int(e.apiFsHz), false)
			apiBufSamples := bufLengthMs * (e.apiFsHz / 1000)
			xBufAPI := ensureInt16Slice(&e.xBufAPI, int(apiBufSamples))
			e.xBufAPIResample.Resample(xBufAPI, xBuf[:oldBufSamples])

			// Initialize the resampler for silk_Encode preparing resampling
			// from API_fs_Hz to fs_kHz.
			e.resampler.init(int(e.apiFsHz), int(fsKHz)*1000, true)

			// Correct the resampler state by resampling the buffered data from
			// API_fs_Hz to fs_kHz.
			e.resampler.Resample(xBuf[:newBufSamples], xBufAPI)
			e.xBufFromInt16(xBuf[:newBufSamples])
		}
	}
	e.prevAPIFsHz = e.apiFsHz
}

// setupFs is silk_setup_fs (silk/control_codec.c): it sets the packet
// geometry for packetSizeMs and, when the internal rate changes, resets the
// analysis history (the buffered input and the frames coded so far are
// dropped) and sets the rate-dependent coding parameters for fsKHz.
func (e *Encoder) setupFs(fsKHz, packetSizeMs int32) {
	// Set packet size.
	if packetSizeMs != e.packetSizeMs {
		if packetSizeMs <= 10 {
			e.nFramesPerPacket = 1
			e.nbSubfr = 1
			if packetSizeMs == 10 {
				e.nbSubfr = 2
			}
			e.frameLength = packetSizeMs * fsKHz
		} else {
			e.nFramesPerPacket = packetSizeMs / maxFrameLengthMs
			e.nbSubfr = maxNbSubfr
			e.frameLength = maxFrameLengthMs * fsKHz
		}
		e.packetSizeMs = packetSizeMs
		// Trigger a new SNR computation.
		e.targetRateBps = 0
	}

	// Set internal sampling frequency.
	if e.fsKHz != fsKHz {
		e.resetAnalysisHistory()
		e.inputBufIx = 0
		e.nFramesEncoded = 0
		// Trigger a new SNR computation.
		e.targetRateBps = 0

		e.fsKHz = fsKHz
		switch fsKHz {
		case 8:
			e.bandwidth = BandwidthNarrowband
		case 12:
			e.bandwidth = BandwidthMediumband
		default:
			e.bandwidth = BandwidthWideband
		}
		e.lpcOrder = maxLPCOrder
		if fsKHz == 8 || fsKHz == 12 {
			e.lpcOrder = minLPCOrder
		}
		e.frameLength = subFrameLengthMs * fsKHz * e.nbSubfr
	}
}

// setupComplexity is silk_setup_complexity (silk/control_codec.c): it sets the
// pitch estimator, noise shaping and quantizer tuning for complexity 0-10.
func (e *Encoder) setupComplexity(complexity int32) {
	complexity = min(max(complexity, 0), 10)
	e.complexity = complexity

	fsKHz := e.fsKHz

	switch {
	case complexity < 1:
		e.pitchEstimationComplexity = 0
		e.pitchEstimationThresholdQ16 = 52429
		e.pitchEstimationLPCOrder = 6
		e.shapingLPCOrder = 12
		e.laShape = 3 * fsKHz
		e.nStatesDelayedDecision = 1
		e.warpingQ16 = 0
		e.nlsfSurvivors = 2
	case complexity < 2:
		e.pitchEstimationComplexity = 1
		e.pitchEstimationThresholdQ16 = 49807
		e.pitchEstimationLPCOrder = 8
		e.shapingLPCOrder = 14
		e.laShape = 5 * fsKHz
		e.nStatesDelayedDecision = 1
		e.warpingQ16 = 0
		e.nlsfSurvivors = 3
	case complexity < 3:
		e.pitchEstimationComplexity = 0
		e.pitchEstimationThresholdQ16 = 52429
		e.pitchEstimationLPCOrder = 6
		e.shapingLPCOrder = 12
		e.laShape = 3 * fsKHz
		e.nStatesDelayedDecision = 2
		e.warpingQ16 = 0
		e.nlsfSurvivors = 2
	case complexity < 4:
		e.pitchEstimationComplexity = 1
		e.pitchEstimationThresholdQ16 = 49807
		e.pitchEstimationLPCOrder = 8
		e.shapingLPCOrder = 14
		e.laShape = 5 * fsKHz
		e.nStatesDelayedDecision = 2
		e.warpingQ16 = 0
		e.nlsfSurvivors = 4
	case complexity < 6:
		e.pitchEstimationComplexity = 1
		e.pitchEstimationThresholdQ16 = 48497
		e.pitchEstimationLPCOrder = 10
		e.shapingLPCOrder = 16
		e.laShape = 5 * fsKHz
		e.nStatesDelayedDecision = 2
		e.warpingQ16 = int32(float32(fsKHz) * float32(warpingMultiplier) * 65536.0)
		e.nlsfSurvivors = 6
	case complexity < 8:
		e.pitchEstimationComplexity = 1
		e.pitchEstimationThresholdQ16 = 47186
		e.pitchEstimationLPCOrder = 12
		e.shapingLPCOrder = 20
		e.laShape = 5 * fsKHz
		e.nStatesDelayedDecision = 3
		e.warpingQ16 = int32(float32(fsKHz) * float32(warpingMultiplier) * 65536.0)
		e.nlsfSurvivors = 8
	default:
		e.pitchEstimationComplexity = 2
		e.pitchEstimationThresholdQ16 = 45875
		e.pitchEstimationLPCOrder = 16
		e.shapingLPCOrder = 24
		e.laShape = 5 * fsKHz
		e.nStatesDelayedDecision = maxDelDecStates
		e.warpingQ16 = int32(float32(fsKHz) * float32(warpingMultiplier) * 65536.0)
		e.nlsfSurvivors = 16
	}

	// Do not allow higher pitch estimation LPC order than predict LPC order.
	e.pitchEstimationLPCOrder = min(e.pitchEstimationLPCOrder, e.lpcOrder)
	e.shapeWinLength = subFrameLengthMs*fsKHz + 2*e.laShape
}

// setupLBRR is silk_setup_LBRR (silk/control_codec.c), run once per packet:
// LBRR is coded when the Opus layer asks for it, and the LBRR gain increase is
// smaller after a packet that already carried LBRR (the previous packet was
// coded at a lower rate).
func (e *Encoder) setupLBRR(lbrrCoded bool) {
	lbrrInPreviousPacket := e.lbrrEnabled
	e.lbrrEnabled = lbrrCoded
	if !e.lbrrEnabled {
		return
	}
	if !lbrrInPreviousPacket {
		e.lbrrGainIncreases = 7
		return
	}
	e.lbrrGainIncreases = max(7-silkSMULWB(e.packetLossPercent, int32(silkFixConst(0.2, 16))), 3)
}
