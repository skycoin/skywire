package silk

// computeLTPScaleIndex selects the LTP scale index using libopus logic.
// For zero packet loss this defaults to index 0 (no scaling).
func (e *Encoder) computeLTPScaleIndex(ltpPredGainQ7 int32, condCoding int) int {
	if condCoding != codeIndependently || ltpPredGainQ7 <= 0 {
		return 0
	}

	roundLoss := max(e.packetLossPercent*e.nFramesPerPacket, 0)
	if e.lbrrLTPRoundLoss {
		roundLoss = 2 + (roundLoss*roundLoss)/100
	}

	// Match libopus silk_LTP_scale_ctrl_FLP.c logic
	// psEnc->sCmn.indices.LTP_scaleIndex = silk_SMULBB( psEncCtrl->LTPredCodGain, round_loss ) > silk_log2lin( 2900 - psEnc->sCmn.SNR_dB_Q7 );
	//
	// In libopus, LTPredCodGain is dB (float). silk_SMULBB truncates it to int16.
	// In gopus, ltpPredGainQ7 is dB * 128 (Q7).
	// To match libopus truncation, we use (ltpPredGainQ7 / 128).
	val := (ltpPredGainQ7 / 128) * int32(roundLoss)

	threshold1 := silkLog2Lin(2900 - int32(e.snrDBQ7))
	threshold2 := silkLog2Lin(3900 - int32(e.snrDBQ7))

	idx := 0
	if val > threshold1 {
		idx++
	}
	if val > threshold2 {
		idx++
	}
	if idx < 0 {
		return 0
	}
	if idx > 2 {
		return 2
	}
	return idx
}
