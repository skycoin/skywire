package silk

// silkCNGReset mirrors libopus silk_CNG_Reset().
func silkCNGReset(st *decoderState) {
	if st == nil {
		return
	}
	order := int(st.lpcOrder)
	if order <= 0 || order > maxLPCOrder {
		order = maxLPCOrder
	}
	stepQ15 := silkDiv32_16(int32(32767), int32(order+1))
	accQ15 := int32(0)
	for i := 0; i < order; i++ {
		accQ15 += stepQ15
		st.cng.smthNLSFQ15[i] = int16(accQ15)
	}
	for i := order; i < maxLPCOrder; i++ {
		st.cng.smthNLSFQ15[i] = 0
		st.cng.synthStateQ14[i] = 0
	}
	for i := range st.cng.excBufQ14 {
		st.cng.excBufQ14[i] = 0
	}
	st.cng.smthGainQ16 = 0
	st.cng.randSeed = 3176576
}

// silkCNGExc fills a comfort-noise excitation buffer (Q14) by randomly sampling
// the stored excitation history with the LCG seed. Mirrors libopus silk/CNG.c
// silk_CNG_exc.
func silkCNGExc(dstQ14 []int32, excBufQ14 []int32, length int, randSeed *int32) {
	if length <= 0 || randSeed == nil {
		return
	}
	excMask := cngBufMaskMax
	for excMask > length {
		excMask >>= 1
	}
	seed := *randSeed
	for i := 0; i < length && i < len(dstQ14); i++ {
		seed = silkRand(seed)
		idx := max(int((seed>>24)&int32(excMask)), 0)
		if idx >= len(excBufQ14) {
			idx = len(excBufQ14) - 1
		}
		dstQ14[i] = excBufQ14[idx]
	}
	*randSeed = seed
}

func silkSMULTT(a32, b32 int32) int32 {
	return (a32 >> 16) * (b32 >> 16)
}

func silkSubLShift32(a, b int32, shift int) int32 {
	return a - (b << shift)
}

func silkAddSat16(a, b int16) int16 {
	sum := int32(a) + int32(b)
	return silkSAT16(sum)
}

func cngLPCOrder(st *decoderState) int {
	order := int(st.lpcOrder)
	if order <= 0 || order > maxLPCOrder {
		return maxLPCOrder
	}
	return order
}

func syncCNGSampleRate(st *decoderState) {
	if st.fsKHz == st.cng.fsKHz {
		return
	}
	silkCNGReset(st)
	st.cng.fsKHz = st.fsKHz
}

func shouldUpdateCNGHistory(st *decoderState, ctrl *decoderControl) bool {
	return st.lossCnt == 0 && st.prevSignalType == typeNoVoiceActivity && ctrl != nil
}

// updateCNGHistory refreshes the comfort-noise model from the most recent
// no-voice-activity frame: it smooths the stored NLSFs toward the frame's
// NLSFs, copies the highest-gain subframe's excitation into the CNG excitation
// history, and smooths the CNG gain. Mirrors the history-update block of libopus
// silk/CNG.c silk_CNG.
func updateCNGHistory(st *decoderState, ctrl *decoderControl) {
	order := cngLPCOrder(st)
	for i := range order {
		smthQ15 := int32(st.cng.smthNLSFQ15[i])
		delta := int32(st.prevNLSFQ15[i]) - smthQ15
		st.cng.smthNLSFQ15[i] = int16(smthQ15 + silkSMULWB(delta, cngNLSFSMthQ16))
	}

	maxGainQ16 := int32(0)
	subfr := 0
	nbSubfr := int(st.nbSubfr)
	for i := range nbSubfr {
		if ctrl.GainsQ16[i] > maxGainQ16 {
			maxGainQ16 = ctrl.GainsQ16[i]
			subfr = i
		}
	}

	subfrLength := int(st.subfrLength)
	if subfrLength > 0 && nbSubfr > 0 {
		moveLen := (nbSubfr - 1) * subfrLength
		if moveLen > 0 && subfrLength+moveLen <= len(st.cng.excBufQ14) {
			copy(st.cng.excBufQ14[subfrLength:subfrLength+moveLen], st.cng.excBufQ14[:moveLen])
		}
		srcStart := subfr * subfrLength
		srcEnd := srcStart + subfrLength
		if srcStart >= 0 && srcEnd <= len(st.excQ14) && subfrLength <= len(st.cng.excBufQ14) {
			copy(st.cng.excBufQ14[:subfrLength], st.excQ14[srcStart:srcEnd])
		}
	}

	for i := range nbSubfr {
		st.cng.smthGainQ16 += silkSMULWB(ctrl.GainsQ16[i]-st.cng.smthGainQ16, cngGainSmthQ16)
		if silkSMULWW(st.cng.smthGainQ16, cngGainSmthThresholdQ16) > ctrl.GainsQ16[i] {
			st.cng.smthGainQ16 = ctrl.GainsQ16[i]
		}
	}
}

// applyCNGLostFrame synthesizes comfort noise for a lost or inactive frame and
// adds it to frame in place: it derives the CNG gain from the PLC random scale
// and the smoothed CNG gain, generates a CNG excitation, runs it through the LPC
// filter built from the smoothed CNG NLSFs, and accumulates the result. Returns
// false (no CNG applied) when the frame is not a loss frame. Mirrors the
// synthesis block of libopus silk/CNG.c silk_CNG.
func (d *Decoder) applyCNGLostFrame(channel int, st *decoderState, frame []int16) bool {
	if st.lossCnt == 0 {
		return false
	}

	plcState := d.ensureSILKPLCState(channel)
	if plcState == nil {
		return true
	}

	gainQ16 := silkSMULWW(int32(plcState.RandScaleQ14), plcState.PrevGainQ16[1])
	if gainQ16 >= (1<<21) || st.cng.smthGainQ16 > (1<<23) {
		gainQ16 = silkSMULTT(gainQ16, gainQ16)
		gainQ16 = silkSubLShift32(silkSMULTT(st.cng.smthGainQ16, st.cng.smthGainQ16), gainQ16, 5)
		gainQ16 = silkLSHIFT(silkSqrtApproxPLC(gainQ16), 16)
	} else {
		gainQ16 = silkSMULWW(gainQ16, gainQ16)
		gainQ16 = silkSubLShift32(silkSMULWW(st.cng.smthGainQ16, st.cng.smthGainQ16), gainQ16, 5)
		gainQ16 = silkLSHIFT(silkSqrtApproxPLC(gainQ16), 8)
	}
	gainQ10 := silkRSHIFT(gainQ16, 6)

	order := cngLPCOrder(st)

	// libopus silk_CNG works on a single SILK frame (frame_length <= 320), so
	// the common case fits the stack array. Public PLC entry points can request
	// a multi-frame concealment span in one call (frameSizeSamples > one SILK
	// frame); fall back to a heap slice so the synthesis buffer never overflows.
	// The computed CNG values are identical either way for frames that fit.
	var cngSigQ14 [maxFrameLength + maxLPCOrder]int32
	var sig []int32
	if maxLPCOrder+len(frame) <= len(cngSigQ14) {
		sig = cngSigQ14[:maxLPCOrder+len(frame)]
	} else {
		sig = make([]int32, maxLPCOrder+len(frame))
	}
	silkCNGExc(sig[maxLPCOrder:], st.cng.excBufQ14[:], len(frame), &st.cng.randSeed)

	var aQ12 [maxLPCOrder]int16
	var nlsfQ15 [maxLPCOrder]int16
	for i := range order {
		nlsfQ15[i] = st.cng.smthNLSFQ15[i]
	}
	if !silkNLSF2A(aQ12[:order], nlsfQ15[:order], order) {
		lpc := lsfToLPCDirect(nlsfQ15[:order])
		copy(aQ12[:order], lpc[:order])
	}

	copy(sig[:maxLPCOrder], st.cng.synthStateQ14[:])
	for i := range frame {
		base := maxLPCOrder + i
		lpcPredQ10 := int32(order >> 1)
		for j := range order {
			lpcPredQ10 = silkSMLAWB(lpcPredQ10, sig[base-j-1], int32(aQ12[j]))
		}
		sig[base] = silkAddSat32(sig[base], lShiftSAT32By4(lpcPredQ10))
		cngSample := silkSAT16(silkRSHIFT_ROUND(silkSMULWW(sig[base], gainQ10), 8))
		frame[i] = silkAddSat16(frame[i], cngSample)
	}
	copy(st.cng.synthStateQ14[:], sig[len(frame):len(frame)+maxLPCOrder])
	return true
}

func clearCNGSynthesisState(st *decoderState) {
	for i := 0; i < int(st.lpcOrder) && i < maxLPCOrder; i++ {
		st.cng.synthStateQ14[i] = 0
	}
}

// applyCNG mirrors libopus silk_CNG() cadence for a single channel/frame.
func (d *Decoder) applyCNG(channel int, st *decoderState, ctrl *decoderControl, frame []int16) {
	if st == nil || len(frame) == 0 {
		return
	}

	syncCNGSampleRate(st)
	if shouldUpdateCNGHistory(st, ctrl) {
		updateCNGHistory(st, ctrl)
	}
	if d.applyCNGLostFrame(channel, st, frame) {
		return
	}
	clearCNGSynthesisState(st)
}
