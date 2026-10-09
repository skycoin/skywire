//go:build gopus_fixed_point

package encoder

func (e *Encoder) reserveFixedShortScratch(frameSize, maxDataBytes int) {
	if !e.celtFixedFrameSizeInScope(frameSize) {
		return
	}
	st := e.ensureFixedCELT(int(e.channels))
	st.enc.ReserveFrameScratch(frameSize, maxDataBytes)
	if cap(st.rng.Buffer()) < maxDataBytes {
		st.rng.Init(make([]byte, maxDataBytes))
	}
	if cap(e.fixedCELTOut) < maxDataBytes {
		buf := make([]byte, len(e.fixedCELTOut), maxDataBytes)
		copy(buf, e.fixedCELTOut)
		e.fixedCELTOut = buf
	}
}
