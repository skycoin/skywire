//go:build gopus_dred || gopus_osce

package celt

import (
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/internal/plc"
)

const (
	dred48kSincOrder = 48
)

// Matches libopus celt_decoder.c deep PLC/DRED sinc_filter.
var dred48kSincFilter = [...]float32{
	4.2931e-05, -0.000190293, -0.000816132, -0.000637162, 0.00141662, 0.00354764, 0.00184368, -0.00428274,
	-0.00856105, -0.0034003, 0.00930201, 0.0159616, 0.00489785, -0.0169649, -0.0259484, -0.00596856,
	0.0286551, 0.0405872, 0.00649994, -0.0509284, -0.0716655, -0.00665212, 0.134336, 0.278927,
	0.339995, 0.278927, 0.134336, -0.00665212, -0.0716655, -0.0509284, 0.00649994, 0.0405872,
	0.0286551, -0.00596856, -0.0259484, -0.0169649, 0.00489785, 0.0159616, 0.00930201, -0.0034003,
	-0.00856105, -0.00428274, 0.00184368, 0.00354764, 0.00141662, -0.000637162, -0.000816132, -0.000190293,
	4.2931e-05,
}

func quantizePLCPCM16kFrame(frame []float32) {
	for i := range frame {
		frame[i] = float32(quantizePLCPCM16kSample(frame[i])) * (1.0 / 32768.0)
	}
}

func quantizePLCPCM16kSample(sample float32) int16 {
	v := sample * 32768
	if v < -32767 {
		v = -32767
	}
	if v > 32767 {
		v = 32767
	}
	return int16(opusmath.FloorHalfPlusF32ToInt32(v))
}

func quantizedFARGANPCM16GridSample(sample float32) float32 {
	return float32(quantizePLCPCM16kSample(sample)) * (1.0 / 32768.0)
}

// ConcealPLCNeural48kMonoToFloat32 mirrors the libopus FRAME_PLC_NEURAL
// lost-frame path for the current mono 48 kHz seam. The caller owns the
// queued 16 kHz neural history and provides a frame generator that fills one
// 10 ms concealed frame in normalized float32 units whenever more queued
// samples are needed.
func (d *Decoder) ConcealPLCNeural48kMonoToFloat32(
	out []float32,
	frameSize int,
	lastNeural *bool,
	plcPCM []int16,
	plcFill *int,
	plcPreemphMem *float32,
	generate func([]float32) bool,
) bool {
	if d == nil || d.channels != 1 || frameSize <= 0 || len(out) < frameSize || lastNeural == nil || plcFill == nil || plcPreemphMem == nil || generate == nil {
		return false
	}
	if d.chooseLostFrameType(0, true, false) != framePLCNeural {
		return false
	}
	return d.concealNeural48kMono(out[:frameSize], frameSize, 1, lastNeural, plcPCM, plcFill, plcPreemphMem, generate, true, framePLCNeural)
}

// ConcealDRED48kMonoToFloat32 mirrors the libopus FRAME_DRED lost-frame path
// for the current mono 48 kHz seam. The caller owns the queued 16 kHz neural
// history and provides a frame generator that fills one 10 ms concealed frame
// in normalized float32 units whenever more queued samples are needed.
func (d *Decoder) ConcealDRED48kMonoToFloat32(
	out []float32,
	frameSize int,
	lastNeural *bool,
	plcPCM []int16,
	plcFill *int,
	plcPreemphMem *float32,
	generate func([]float32) bool,
) bool {
	if d == nil || d.channels != 1 || frameSize <= 0 || len(out) < frameSize || lastNeural == nil || plcFill == nil || plcPreemphMem == nil || generate == nil {
		return false
	}
	if d.chooseLostFrameType(0, true, true) != frameDRED {
		return false
	}
	return d.concealNeural48kMono(out[:frameSize], frameSize, 1, lastNeural, plcPCM, plcFill, plcPreemphMem, generate, true, frameDRED)
}

// ConcealDRED48kToFloat32 mirrors ConcealDRED48kMonoToFloat32 for both mono
// and stereo decoders. libopus implements DRED concealment as fundamentally
// mono (a single LPCNetPLCState, see opus_decoder.c) and, for stereo output,
// runs the mono pipeline against channel-0 CELT state then duplicates that
// state to channel-1 (celt_decoder.c:1066-1067:
// `if (C==2) OPUS_COPY(decode_mem[1], decode_mem[0], ...)`). This wrapper
// follows the same shape: it runs concealNeural48kMono against channel-0
// slices of the CELT state buffers, mirrors channel-0 to channel-1 after
// the call, and writes mono PCM duplicated across both interleaved channels.
func (d *Decoder) ConcealDRED48kToFloat32(
	out []float32,
	frameSize int,
	lastNeural *bool,
	plcPCM []int16,
	plcFill *int,
	plcPreemphMem *float32,
	generate func([]float32) bool,
) bool {
	return d.ConcealDRED48kDownsampleToFloat32(out, frameSize, 1, lastNeural, plcPCM, plcFill, plcPreemphMem, generate)
}

// ConcealDRED48kDownsampleToFloat32 is ConcealDRED48kToFloat32 with an explicit
// downsample factor: it conceals the lost frame at the 48 kHz neural rate and
// emits frameSize/downsample output samples per channel. It returns false when
// DRED concealment is not the chosen frame type or the arguments are invalid.
func (d *Decoder) ConcealDRED48kDownsampleToFloat32(
	out []float32,
	frameSize int,
	downsample int,
	lastNeural *bool,
	plcPCM []int16,
	plcFill *int,
	plcPreemphMem *float32,
	generate func([]float32) bool,
) bool {
	if d == nil || frameSize <= 0 {
		return false
	}
	if downsample <= 0 || frameSize%downsample != 0 {
		return false
	}
	outputFrameSize := frameSize / downsample
	if d.channels == 1 {
		if len(out) < outputFrameSize {
			return false
		}
		if d.chooseLostFrameType(0, true, true) != frameDRED {
			return false
		}
		return d.concealNeural48kMono(out[:outputFrameSize], frameSize, downsample, lastNeural, plcPCM, plcFill, plcPreemphMem, generate, true, frameDRED)
	}
	if d.channels != 2 {
		return false
	}
	channels := int(d.channels)
	if len(out) < outputFrameSize*channels {
		return false
	}
	if d.chooseLostFrameType(0, true, true) != frameDRED {
		return false
	}
	return d.runStereoDREDConceal(out[:outputFrameSize*channels], frameSize, downsample, lastNeural, plcPCM, plcFill, plcPreemphMem, generate, true, frameDRED)
}

// ConcealPLCNeural48kToFloat32 mirrors ConcealPLCNeural48kMonoToFloat32 for
// both mono and stereo decoders. See ConcealDRED48kToFloat32 for the
// mono-downmix-in / mono-duplicate-out stereo rationale.
func (d *Decoder) ConcealPLCNeural48kToFloat32(
	out []float32,
	frameSize int,
	lastNeural *bool,
	plcPCM []int16,
	plcFill *int,
	plcPreemphMem *float32,
	generate func([]float32) bool,
) bool {
	return d.ConcealPLCNeural48kDownsampleToFloat32(out, frameSize, 1, lastNeural, plcPCM, plcFill, plcPreemphMem, generate)
}

// ConcealPLCNeural48kDownsampleToFloat32 is ConcealPLCNeural48kToFloat32 with an
// explicit downsample factor: it runs neural PLC concealment at the 48 kHz rate
// and emits frameSize/downsample output samples per channel.
func (d *Decoder) ConcealPLCNeural48kDownsampleToFloat32(
	out []float32,
	frameSize int,
	downsample int,
	lastNeural *bool,
	plcPCM []int16,
	plcFill *int,
	plcPreemphMem *float32,
	generate func([]float32) bool,
) bool {
	if d == nil || frameSize <= 0 {
		return false
	}
	if downsample <= 0 || frameSize%downsample != 0 {
		return false
	}
	outputFrameSize := frameSize / downsample
	if d.channels == 1 {
		if len(out) < outputFrameSize {
			return false
		}
		if d.chooseLostFrameType(0, true, false) != framePLCNeural {
			return false
		}
		return d.concealNeural48kMono(out[:outputFrameSize], frameSize, downsample, lastNeural, plcPCM, plcFill, plcPreemphMem, generate, true, framePLCNeural)
	}
	if d.channels != 2 {
		return false
	}
	channels := int(d.channels)
	if len(out) < outputFrameSize*channels {
		return false
	}
	if d.chooseLostFrameType(0, true, false) != framePLCNeural {
		return false
	}
	return d.runStereoDREDConceal(out[:outputFrameSize*channels], frameSize, downsample, lastNeural, plcPCM, plcFill, plcPreemphMem, generate, true, framePLCNeural)
}

// runStereoDREDConceal mirrors the libopus stereo neural PLC path: compute the
// periodic PLC baseline for both channels, generate mono neural concealment,
// copy channel-0 neural state to channel-1, then crossfade each channel with
// its own periodic baseline for the overlap.
func (d *Decoder) runStereoDREDConceal(
	out []float32,
	frameSize int,
	downsample int,
	lastNeural *bool,
	plcPCM []int16,
	plcFill *int,
	plcPreemphMem *float32,
	generate func([]float32) bool,
	recordLoss bool,
	frameType int,
) bool {
	if d == nil || d.channels != 2 || frameSize <= 0 || downsample <= 0 || frameSize%downsample != 0 {
		return false
	}
	if lastNeural == nil || plcFill == nil || plcPreemphMem == nil || generate == nil {
		return false
	}
	// Either a real output buffer of size >= frameSize*2 or nil for state-only.
	outputSamples := (frameSize / downsample) * 2
	if out != nil && len(out) < outputSamples {
		return false
	}

	d.materializePLCDecodeHistory()
	d.materializePostfilterHistoryFromPLC()

	if d.plcState == nil {
		d.plcState = plc.NewState()
	}
	lossCount := d.plcState.LostCount()
	if recordLoss {
		_ = d.plcState.RecordLoss()
		lossCount = d.plcState.LostCount()
	}
	*lastNeural = d.lastPLCFrameWasNeural()

	totalSamples := frameSize + Overlap
	stereoSamples := totalSamples * 2
	d.scratchPLC = ensureFloat32Slice(&d.scratchPLC, stereoSamples)
	if !d.concealPeriodicPLC(d.scratchPLC[:stereoSamples], frameSize, lossCount, d.lastPLCFrameWasPeriodic() || *lastNeural, false) {
		return false
	}

	baseline := ensureSigSlice(&d.scratchPLCDREDBase, Overlap*2)
	copyFloat32ToSig(baseline[:Overlap*2], d.scratchPLC[:Overlap*2])

	samplesNeeded16k := (frameSize + dred48kSincOrder + Overlap) / 3
	if !*lastNeural {
		*plcFill = 0
	}
	for *plcFill < samplesNeeded16k {
		if *plcFill+160 > len(plcPCM) {
			return false
		}
		frame := ensureFloat32Slice(&d.scratchPLCDREDFrame, 160)
		if !generate(frame) {
			return false
		}
		for i, sample := range frame[:160] {
			plcPCM[*plcFill+i] = quantizePLCPCM16kSample(sample)
		}
		*plcFill += 160
	}

	neural := ensureFloat32Slice(&d.scratchPLCDREDNeural, totalSamples)
	for i := 0; i < totalSamples/3; i++ {
		var sum float32
		for j := 0; j < 17; j++ {
			sum += 3 * float32(plcPCM[i+j]) * dred48kSincFilter[3*j]
		}
		neural[3*i] = sum
		sum = 0
		for j := 0; j < 16; j++ {
			sum += 3 * float32(plcPCM[i+j+1]) * dred48kSincFilter[3*j+2]
		}
		neural[3*i+1] = sum
		sum = 0
		for j := 0; j < 16; j++ {
			sum += 3 * float32(plcPCM[i+j+1]) * dred48kSincFilter[3*j+1]
		}
		neural[3*i+2] = sum
	}

	consumed16k := frameSize / 3
	copy(plcPCM[:], plcPCM[consumed16k:*plcFill])
	*plcFill -= consumed16k

	preemph := deepPLCPreemphCoef
	preemphMem := *plcPreemphMem * 32768
	for i := 0; i < frameSize; i++ {
		tmp := neural[i]
		v := (tmp - preemph*preemphMem)
		preemphMem = tmp
		d.scratchPLC[2*i] = v
		d.scratchPLC[2*i+1] = v
	}
	*plcPreemphMem = preemphMem * (1.0 / 32768.0)
	overlapMem := preemphMem
	for i := 0; i < Overlap; i++ {
		idx := frameSize + i
		tmp := neural[idx]
		v := (tmp - preemph*overlapMem)
		overlapMem = tmp
		dst := 2 * idx
		d.scratchPLC[dst] = v
		d.scratchPLC[dst+1] = v
	}

	if !*lastNeural {
		window := GetWindowBufferF32(Overlap)
		blend := min(Overlap, frameSize)
		for i := 0; i < blend; i++ {
			w := window[i]
			idx := 2 * i
			d.scratchPLC[idx] = (1-w)*float32(baseline[idx]) + w*d.scratchPLC[idx]
			d.scratchPLC[idx+1] = (1-w)*float32(baseline[idx+1]) + w*d.scratchPLC[idx+1]
		}
	}

	d.updateStereoDREDNeuralHistories(d.scratchPLC[:stereoSamples], frameSize)
	d.updatePLCOverlapBuffer(d.scratchPLC[:stereoSamples], frameSize)
	if out != nil {
		if downsample > 1 {
			d.applyDeemphasisAndScaleDownsampleToFloat32(out[:outputSamples], d.scratchPLC[:frameSize*2], downsample, 1.0/32768.0)
		} else {
			d.applyDeemphasisAndScaleToFloat32(out[:outputSamples], d.scratchPLC[:frameSize*2], 1.0/32768.0)
		}
	} else {
		d.advanceDeemphasisStateStereo(d.scratchPLC[:frameSize*2])
	}

	if recordLoss {
		d.finishLostFrame(frameType, frameSize)
	} else {
		switch frameType {
		case frameDRED:
			d.plcDuration = 0
		case framePLCNeural:
			d.accumulatePLCLossDuration(frameSize)
		}
		d.plcSkip = false
		d.plcLastFrameType = int32(frameType)
		d.plcPrevLossWasPeriodic = false
	}
	d.plcPrefilterAndFoldPending = true
	*lastNeural = true
	return true
}

func (d *Decoder) updateStereoDREDNeuralHistories(samples []float32, frameSize int) {
	d.updateStereoDREDNeuralHistory(d.postfilterMem, frameSize, combFilterHistory, samples)
	d.updateStereoDREDNeuralHistory(d.plcDecodeMem, frameSize, plcDecodeBufferSize, samples)
	d.postfilterMemFromPLC = false
	d.postfilterMemPLCBacked = false
	d.plcDecodeMemRingActive = false
	d.plcDecodeMemRingStart = 0
}

func (d *Decoder) updateStereoDREDNeuralHistory(hist []celtSig, frameSize, history int, samples []float32) {
	if d == nil || frameSize <= 0 || history <= 0 || len(hist) < history*2 || len(samples) < frameSize*2 {
		return
	}
	histL := hist[:history]
	histR := hist[history : 2*history]
	if frameSize >= history {
		src := (frameSize - history) * 2
		for i := 0; i < history; i++ {
			histL[i] = celtSig(samples[src])
			histR[i] = celtSig(samples[src+1])
			src += 2
		}
		return
	}
	copy(histL, histL[frameSize:])
	dst := history - frameSize
	// libopus copies channel-0 decode memory over channel 1 before the neural crossfade.
	copy(histR[:dst], histL[:dst])
	src := 0
	for i := 0; i < frameSize; i++ {
		histL[dst+i] = celtSig(samples[src])
		histR[dst+i] = celtSig(samples[src+1])
		src += 2
	}
}

func (d *Decoder) advanceDeemphasisStateStereo(samples []float32) {
	if d == nil || d.channels != 2 || len(samples) == 0 || len(d.preemphState) < 2 {
		return
	}
	const verySmall float32 = 1e-30
	const coef float32 = float32(PreemphCoef)
	stateL := d.preemphState[0]
	stateR := d.preemphState[1]
	for i := 0; i+1 < len(samples); i += 2 {
		tmpL := samples[i] + verySmall + stateL
		stateL = coef * tmpL
		tmpR := samples[i+1] + verySmall + stateR
		stateR = coef * tmpR
	}
	d.preemphState[0] = stateL
	d.preemphState[1] = stateR
}

// ConcealPLCNeural48kMonoStateOnly updates retained CELT 48 kHz mono neural
// PLC state without producing caller-visible PCM or recording another loss
// event.
func (d *Decoder) ConcealPLCNeural48kMonoStateOnly(
	frameSize int,
	lastNeural *bool,
	plcPCM []int16,
	plcFill *int,
	plcPreemphMem *float32,
	generate func([]float32) bool,
) bool {
	if d == nil || d.channels != 1 || frameSize <= 0 || lastNeural == nil || plcFill == nil || plcPreemphMem == nil || generate == nil {
		return false
	}
	return d.concealNeural48kMono(nil, frameSize, 1, lastNeural, plcPCM, plcFill, plcPreemphMem, generate, false, framePLCNeural)
}

// ConcealDRED48kMonoStateOnly updates retained CELT 48 kHz mono DRED state
// without producing caller-visible PCM or recording another loss event.
// This is used by the hybrid decoder, which already emitted the audible lost
// frame via its SILK+CELT PLC base but still needs libopus-shaped DRED CELT
// waveform state for the next good packet.
func (d *Decoder) ConcealDRED48kMonoStateOnly(
	frameSize int,
	lastNeural *bool,
	plcPCM []int16,
	plcFill *int,
	plcPreemphMem *float32,
	generate func([]float32) bool,
) bool {
	if d == nil || d.channels != 1 || frameSize <= 0 || lastNeural == nil || plcFill == nil || plcPreemphMem == nil || generate == nil {
		return false
	}
	return d.concealNeural48kMono(nil, frameSize, 1, lastNeural, plcPCM, plcFill, plcPreemphMem, generate, false, frameDRED)
}

func (d *Decoder) concealNeural48kMono(
	out []float32,
	frameSize int,
	downsample int,
	lastNeural *bool,
	plcPCM []int16,
	plcFill *int,
	plcPreemphMem *float32,
	generate func([]float32) bool,
	recordLoss bool,
	frameType int,
) bool {
	if d == nil || d.channels != 1 || frameSize <= 0 || downsample <= 0 || frameSize%downsample != 0 || lastNeural == nil || plcFill == nil || plcPreemphMem == nil || generate == nil {
		return false
	}
	outputFrameSize := frameSize / downsample
	if out != nil && len(out) < outputFrameSize {
		return false
	}
	if d.plcState == nil {
		d.plcState = plc.NewState()
	}
	lossCount := d.plcState.LostCount()
	if recordLoss {
		_ = d.plcState.RecordLoss()
		lossCount = d.plcState.LostCount()
	}
	*lastNeural = d.lastPLCFrameWasNeural()

	totalSamples := frameSize + Overlap
	d.scratchPLC = ensureFloat32Slice(&d.scratchPLC, totalSamples)
	if !d.concealPeriodicPLC(d.scratchPLC[:totalSamples], frameSize, lossCount, d.lastPLCFrameWasPeriodic() || *lastNeural, false) {
		return false
	}

	baseline := ensureSigSlice(&d.scratchPLCDREDBase, Overlap)
	copyFloat32ToSig(baseline[:Overlap], d.scratchPLC[:Overlap])

	samplesNeeded16k := (frameSize + dred48kSincOrder + Overlap) / 3
	if !*lastNeural {
		*plcFill = 0
	}
	for *plcFill < samplesNeeded16k {
		if *plcFill+160 > len(plcPCM) {
			return false
		}
		frame := ensureFloat32Slice(&d.scratchPLCDREDFrame, 160)
		if !generate(frame) {
			return false
		}
		for i, sample := range frame[:160] {
			plcPCM[*plcFill+i] = quantizePLCPCM16kSample(sample)
		}
		*plcFill += 160
	}

	neural := ensureFloat32Slice(&d.scratchPLCDREDNeural, totalSamples)
	for i := 0; i < totalSamples/3; i++ {
		var sum float32
		for j := 0; j < 17; j++ {
			sum += 3 * float32(plcPCM[i+j]) * dred48kSincFilter[3*j]
		}
		neural[3*i] = sum
		sum = 0
		for j := 0; j < 16; j++ {
			sum += 3 * float32(plcPCM[i+j+1]) * dred48kSincFilter[3*j+2]
		}
		neural[3*i+1] = sum
		sum = 0
		for j := 0; j < 16; j++ {
			sum += 3 * float32(plcPCM[i+j+1]) * dred48kSincFilter[3*j+1]
		}
		neural[3*i+2] = sum
	}

	consumed16k := frameSize / 3
	copy(plcPCM[:], plcPCM[consumed16k:*plcFill])
	*plcFill -= consumed16k

	preemph := deepPLCPreemphCoef
	preemphMem := *plcPreemphMem * 32768
	for i := 0; i < frameSize; i++ {
		tmp := neural[i]
		d.scratchPLC[i] = (tmp - preemph*preemphMem)
		preemphMem = tmp
	}
	// Match libopus celt_decode_lost(FRAME_DRED): retain plc_preemphasis_mem at
	// the frame boundary and keep the overlap tail in a local only.
	*plcPreemphMem = preemphMem * (1.0 / 32768.0)
	overlapMem := preemphMem
	for i := 0; i < Overlap; i++ {
		idx := frameSize + i
		tmp := neural[idx]
		d.scratchPLC[idx] = (tmp - preemph*overlapMem)
		overlapMem = tmp
	}

	if !*lastNeural {
		window := GetWindowBufferF32(Overlap)
		blend := min(Overlap, frameSize)
		for i := 0; i < blend; i++ {
			d.scratchPLC[i] = (1-window[i])*float32(baseline[i]) + window[i]*d.scratchPLC[i]
		}
	}

	d.updatePostfilterHistory(d.scratchPLC[:frameSize], frameSize, combFilterHistory)
	d.updatePLCDecodeHistory(d.scratchPLC[:frameSize], frameSize, plcDecodeBufferSize)
	d.updatePLCOverlapBuffer(d.scratchPLC[:totalSamples], frameSize)
	if out != nil {
		if downsample > 1 {
			d.applyDeemphasisAndScaleDownsampleToFloat32(out[:outputFrameSize], d.scratchPLC[:frameSize], downsample, 1.0/32768.0)
		} else {
			d.applyDeemphasisAndScaleToFloat32(out[:outputFrameSize], d.scratchPLC[:frameSize], 1.0/32768.0)
		}
	} else {
		d.advanceDeemphasisStateMono(d.scratchPLC[:frameSize])
	}

	if recordLoss {
		d.finishLostFrame(frameType, frameSize)
	} else {
		switch frameType {
		case frameDRED:
			d.plcDuration = 0
		case framePLCNeural:
			d.accumulatePLCLossDuration(frameSize)
		}
		d.plcSkip = false
		d.plcLastFrameType = int32(frameType)
		d.plcPrevLossWasPeriodic = false
	}
	d.plcPrefilterAndFoldPending = true
	*lastNeural = true
	return true
}
