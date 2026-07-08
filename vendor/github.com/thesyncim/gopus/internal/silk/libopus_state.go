package silk

func resetDecoderState(st *decoderState) {
	*st = decoderState{}
	st.firstFrameAfterReset = true
	st.prevGainQ16 = 1 << 16
}
