//go:build gopus_fixed_point

package fixedpoint

// ReserveFrameScratch sizes the fixed CELT working buffers used when a child
// Opus encoder changes from SILK to CELT at the same caller frame size. It
// preserves all CELT predictor and range-coder state.
func (e *CELTEncoder) ReserveFrameScratch(frameSize, maxPayloadBytes int) {
	if frameSize <= 0 || maxPayloadBytes <= 0 {
		return
	}
	n := frameSize * max(e.upsample, 1)
	channels := e.channels
	bands := len(e.eBands) - 1
	if bands <= 0 {
		return
	}
	s := e.ensureScratch()
	maxPeriod := e.maxPeriod
	maxPitch := max(maxPeriod-3*combFilterMinPeriod, 1)
	ensureInt32(&s.preStorage, channels*(n+maxPeriod))
	if cap(s.prePeriodic) < channels {
		s.prePeriodic = make([][]int32, channels)
	}
	ensureInt16(&s.pitchBuf, (maxPeriod+n)>>1)
	ensureInt16(&s.pitchXLP4, n>>2)
	ensureInt16(&s.pitchYLP4, (n+maxPitch)>>2)
	ensureInt32(&s.pitchXcor, maxPitch>>1)
	ensureInt32(&s.pitchYY, maxPeriod+1)
	ensureInt16(&s.pitchXX, n+maxPeriod)
	ensureInt(&s.offsets, bands)
	ensureInt(&s.importance, bands)
	ensureInt(&s.spreadWeight, bands)
	ensureInt(&s.tfMetric, bands)
	ensureInt(&s.tfPath0, bands)
	ensureInt(&s.tfPath1, bands)
	ensureInt32(&s.tfTmp, n)
	ensureInt32(&s.tfTmp1, n)
	ensureInt32(&s.qNorm, channels*n)
	ensureInt32(&s.qLowbandScratch, n)
	ensureInt32(&s.qXSave, n)
	ensureInt32(&s.qYSave, n)
	ensureInt32(&s.qXSave2, n)
	ensureInt32(&s.qYSave2, n)
	ensureInt32(&s.qNormSave2, n)
	s.rdoSnapPre.ReserveBufferCapacity(maxPayloadBytes)
	s.rdoSnap2.ReserveBufferCapacity(maxPayloadBytes)
	s.qextRDOSnapPre.ReserveBufferCapacity(maxPayloadBytes)
	s.qextRDOSnap2.ReserveBufferCapacity(maxPayloadBytes)
}
