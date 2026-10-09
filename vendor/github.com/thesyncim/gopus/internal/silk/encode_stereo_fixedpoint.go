//go:build gopus_fixed_point

package silk

// encode_stereo_fixedpoint.go resets the integer analysis history on an
// internal rate change and when stereo side coding resumes after mid-only
// frames.

// resetFixedAnalysisHistory clears the integer encode state silk_setup_fs
// (silk/control_codec.c) resets on an internal rate change and silk_Encode
// (silk/enc_API.c lines 453-463) resets on the side channel when stereo side
// coding resumes after mid-only frames: sShape (harm/tilt smoothers and
// LastGainIndex), sNSQ, prev_NLSFq_Q15, prevLag, sNSQ.lagPrev, prevSignalType,
// sNSQ.prev_gain_Q16 and first_frame_after_reset. frameCounter and the VAD
// state are preserved, as in libopus.
func (e *Encoder) resetFixedAnalysisHistory() {
	st := e.fixed
	st.nsq = NSQState{}
	st.nsq.prevGainQ16 = 1 << 16
	st.nsq.lagPrev = 100
	st.prevNLSFqQ15 = [maxLPCOrder]int16{}
	st.prevLag = 100
	st.lastGainIndex = 10
	st.prevSignalType = typeNoVoiceActivity
	st.harmShapeGainSmthQ16 = 0
	st.tiltSmthQ16 = 0
	st.firstFrameAfterReset = true
}
