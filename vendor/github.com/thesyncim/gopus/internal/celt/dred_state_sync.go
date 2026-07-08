//go:build gopus_dred || gopus_osce

package celt

import "github.com/thesyncim/gopus/internal/plc"

// CommitDRED48kMonoConcealment mirrors the retained CELT-side state updates
// libopus carries forward after a 48 kHz mono deep-PLC/DRED concealment step.
// The frame samples are caller-visible PCM for the concealed packet, while
// overlap carries the retained tail for the next CELT synthesis boundary.
func (d *Decoder) CommitDRED48kMonoConcealment(frame, overlap []float32) {
	if d == nil || d.channels != 1 {
		return
	}
	frameSize := len(frame)
	if frameSize <= 0 {
		return
	}

	d.updatePostfilterHistoryMonoFromFloat32(frame, frameSize, combFilterHistory)
	d.updatePLCDecodeHistoryMonoFromFloat32(frame, frameSize, plcDecodeBufferSize)
	if len(overlap) >= Overlap {
		copy(d.overlapBuffer[:Overlap], overlap[:Overlap])
	}

	last := float32(frame[frameSize-1])
	d.preemphState[0] = celtSig(float32(PreemphCoef) * (last * 32768))
	d.plcLossDuration = 0
	d.plcDuration = 0
	d.plcLastFrameType = frameDRED
	d.plcSkip = false
	d.plcPrevLossWasPeriodic = false
	d.plcPrefilterAndFoldPending = false
	if d.plcState == nil {
		d.plcState = plc.NewState()
	}
	d.plcState.Reset()
	d.plcState.SetLastFrameParams(plc.ModeCELT, frameSize, int(d.channels))
}

// SyncAfterDREDLoss aligns the retained CELT lost-frame cadence with libopus's
// FRAME_DRED bookkeeping after a caller has already produced the audible loss
// frame by another path.
func (d *Decoder) SyncAfterDREDLoss() {
	if d == nil {
		return
	}
	d.plcDuration = 0
	d.plcLastFrameType = frameDRED
	d.plcSkip = false
	d.plcPrevLossWasPeriodic = false
	d.plcPrefilterAndFoldPending = true
}
