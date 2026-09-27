package celt

import (
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/plc"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// NewDecoder creates a new CELT decoder with the given number of channels.
// Valid channel counts are 1 (mono) or 2 (stereo).
// The decoder is ready to process CELT frames after creation.
func NewDecoder(channels int) *Decoder {
	if channels < 1 {
		channels = 1
	}
	if channels > 2 {
		channels = 2
	}

	d := &Decoder{
		channels:   int32(channels),
		sampleRate: 48000, // CELT always operates at 48kHz internally
		downsample: 1,

		// Energy-prediction history is sized to two channels regardless of the
		// decoder channel count, matching libopus, which always allocates
		// oldBandE/oldLogE/oldLogE2/backgroundLogE as 2*nbEBands. A mono stream
		// keeps the right-channel slot as a shadow copy of the left (libopus
		// `if (C==1) OPUS_COPY(&oldBandE[nbEBands], oldBandE, nbEBands)`), which the
		// loss-recovery prediction folds back in after a concealed gap.
		prevEnergy:       make([]celtGLog, MaxBands*2),
		prevEnergy2:      make([]celtGLog, MaxBands*2),
		prevLogE:         make([]celtGLog, MaxBands*2),
		prevLogE2:        make([]celtGLog, MaxBands*2),
		backgroundEnergy: make([]celtGLog, MaxBands*2),

		// Overlap buffer for CELT (full overlap per channel)
		overlapBuffer: make([]celtSig, Overlap*channels),

		// De-emphasis filter state, one per channel
		preemphState: make([]celtSig, channels),

		// Postfilter history buffer for comb filter
		postfilterMem: make([]celtSig, combFilterHistory*channels),
		// PLC decode history sized to libopus DEC_PITCH_BUF_SIZE.
		plcDecodeMem: make([]celtSig, plcDecodeBufferSize*channels),
		plcLPC:       make([]float32, celtPLCLPCOrder*channels),

		// RNG state (libopus initializes to zero)
		rng: 0,

		bandwidth:              CELTFullband,
		phaseInversionDisabled: channels == 1,
		plcState:               plc.NewState(),
	}

	// Match libopus init/reset defaults (oldLogE/oldLogE2 = -28, buffers cleared).
	d.Reset()

	return d
}

// SetPhaseInversionDisabled toggles stereo phase inversion during CELT decoding.
func (d *Decoder) SetPhaseInversionDisabled(disabled bool) {
	d.phaseInversionDisabled = disabled
}

// PhaseInversionDisabled reports whether stereo phase inversion is disabled.
func (d *Decoder) PhaseInversionDisabled() bool {
	return d.phaseInversionDisabled
}

// Reset clears decoder state for a new stream.
// Call this when starting to decode a new audio stream.
func (d *Decoder) Reset() {
	channels := int(d.channels)
	if channels < 1 {
		channels = 1
	} else if channels > 2 {
		channels = 2
	}
	sampleRate := int(d.sampleRate)
	if sampleRate == 0 {
		sampleRate = 48000
	}
	downsample := int(d.downsample)
	if downsample <= 0 {
		downsample = 1
	}
	phaseInversionDisabled := d.phaseInversionDisabled
	complexity := d.complexity

	// Energy-prediction history is always two channels wide (libopus 2*nbEBands),
	// even for mono, so the right-channel shadow survives a concealed loss gap.
	prevEnergy := ensureGLogSlice(&d.prevEnergy, MaxBands*2)
	prevEnergy2 := ensureGLogSlice(&d.prevEnergy2, MaxBands*2)
	prevLogE := ensureGLogSlice(&d.prevLogE, MaxBands*2)
	prevLogE2 := ensureGLogSlice(&d.prevLogE2, MaxBands*2)
	backgroundEnergy := ensureGLogSlice(&d.backgroundEnergy, MaxBands*2)
	overlapBuffer := ensureSigSlice(&d.overlapBuffer, Overlap*channels)
	preemphState := ensureSigSlice(&d.preemphState, channels)
	postfilterMem := ensureSigSlice(&d.postfilterMem, combFilterHistory*channels)
	plcDecodeMem := ensureSigSlice(&d.plcDecodeMem, plcDecodeBufferSize*channels)
	plcLPC := ensureFloat32Slice(&d.plcLPC, celtPLCLPCOrder*channels)
	clear(prevEnergy)
	clear(prevEnergy2)
	clear(prevLogE)
	clear(prevLogE2)
	clear(backgroundEnergy)
	clear(overlapBuffer)
	clear(preemphState)
	clear(postfilterMem)
	clear(plcDecodeMem)
	clear(plcLPC)
	plcState := d.plcState
	if plcState == nil {
		plcState = plc.NewState()
	} else {
		plcState.Reset()
		plcState.SetLastFrameParams(plc.ModeSILK, 960, 1)
	}
	d.clearDecoderScratchForReset()

	d.prevEnergy = prevEnergy
	d.prevEnergy2 = prevEnergy2
	d.prevLogE = prevLogE
	d.prevLogE2 = prevLogE2
	d.backgroundEnergy = backgroundEnergy
	d.overlapBuffer = overlapBuffer
	d.preemphState = preemphState
	d.postfilterMem = postfilterMem
	d.plcDecodeMem = plcDecodeMem
	d.plcLPC = plcLPC

	d.channels = int32(channels)
	d.sampleRate = int32(sampleRate)
	d.downsample = int32(downsample)
	d.bandwidth = CELTFullband
	d.phaseInversionDisabled = phaseInversionDisabled
	d.complexity = complexity
	d.plcState = plcState
	d.plcSkip = true
	d.plcLastFrameType = frameNone
	d.rangeDecoder = nil
	d.rangeDecoderScratch = rangecoding.Decoder{}
	d.directOutPCM = nil
	d.decoderQEXTFields = decoderQEXTFields{}
	d.decoderDREDState = decoderDREDState{}
	d.rng = 0
	d.prevStreamChannels = 0
	d.postfilterMemFromPLC = false
	d.postfilterMemPLCBacked = false
	d.postfilterPeriod = 0
	d.postfilterGain = 0
	d.postfilterTapset = 0
	d.postfilterPeriodOld = 0
	d.postfilterGainOld = 0
	d.postfilterTapsetOld = 0
	d.plcDecodeMemRingActive = false
	d.plcDecodeMemRingStart = 0
	d.plcLastPitchPeriod = 0
	d.plcPrevLossWasPeriodic = false
	d.plcPrefilterAndFoldPending = false
	d.plcLossDuration = 0
	d.plcDuration = 0
	d.collapseMask = 0
	d.redundancyActive = false
	d.redundancyBytes = nil
	d.redundancyRange = 0
	d.redundancyFrameSize = 0

	for i := range d.prevLogE {
		d.prevLogE[i] = -28.0
		d.prevLogE2[i] = -28.0
	}
	if extsupport.QEXT {
		d.clearQEXTState()
	}
}

func (d *Decoder) clearDecoderScratchForReset() {
	clearGLogCap(d.scratchPrevEnergy)
	clearGLogCap(d.scratchPrevEnergyGLog)
	clearGLogCap(d.scratchEnergies)
	clearInt32Cap(d.scratchTFRes)
	clearInt32Cap(d.scratchOffsets)
	clearInt32Cap(d.scratchPulses)
	clearInt32Cap(d.scratchFineQuant)
	clearInt32Cap(d.scratchFinePriority)
	clearFloat32Cap(d.scratchPrevBandEnergy)
	clearGLogCap(d.scratchSilenceE)
	clearInt32Cap(d.scratchCaps)
	clearInt32Cap(d.scratchAllocWork)
	d.scratchBands.clearForReset()
	d.scratchIMDCTF32.clearForReset()
	d.scratchIMDCTF32R.clearForReset()
	clearFloat32Cap(d.scratchSynthF32)
	clearFloat32Cap(d.scratchSynthRF32)
	clearFloat32Cap(d.scratchSpecRF32)
	clearFloat32Cap(d.scratchStereoF32)
	clearFloat32Cap(d.scratchShortCoeffsF32)
	clearFloat32Cap(d.scratchMonoToStereoRF32)
	clearFloat32Cap(d.scratchMonoMixF32)
	clearFloat32Cap(d.postfilterScratchF32)
	clearFloat32Cap(d.postfilterWindowSqF32)
	clearFloat32Cap(d.scratchPLC)
	clearFloat32Cap(d.scratchPLCF32)
	clearFloat32Cap(d.scratchPLCPitchLP)
	d.scratchPLCPitchSearch.clearForReset()
	clearSigCap(d.scratchPLCFIRTmp)
	clearSigCap(d.scratchPLCWindowed)
	clearFloat32Cap(d.scratchPLCIIRY)
	clearSigCap(d.scratchPLCBuf)
	clearSigCap(d.scratchPLCExc)
	clearSigCap(d.scratchPLCFoldSrc)
	clearSigCap(d.scratchPLCFoldDst)
	clearNormCap(d.scratchPLCHybridNormL)
	clearNormCap(d.scratchPLCHybridNormR)
}

func (s *bandDecodeScratch) clearForReset() {
	clearNormCap(s.left)
	clearNormCap(s.right)
	clearByteCap(s.collapse)
	clearNormCap(s.norm)
	clearNormCap(s.lowband)
	clearNormCap(s.coeffs)
	clear(s.bandVectors)
	clear(s.bandVectorsL)
	clear(s.bandVectorsR)
	for i := range s.bandStorage {
		clearNormCap(s.bandStorage[i])
		clearNormCap(s.bandStorageL[i])
		clearNormCap(s.bandStorageR[i])
	}
	clearInt32Cap(s.pvqPulses)
	clearNormCap(s.pvqNorm)
	clearNormCap(s.foldResult)
	clearUint32Cap(s.cwrsU)
	clearNormCap(s.hadamardTmpNorm)
	clearNormCap(s.quantWork)
}

func (s *imdctScratchF32) clearForReset() {
	clearComplex64Cap(s.fftIn)
	clearKissCpxCap(s.fftTmp)
	clearFloat32Cap(s.buf)
	clearFloat32Cap(s.out)
}

func (s *plcPitchSearchScratch) clearForReset() {
	clearFloat32Cap(s.xLP4)
	clearFloat32Cap(s.yLP4)
	clearFloat32Cap(s.xcorr)
}

func clearFloat32Cap(s []float32) {
	clear(s[:cap(s)])
}

func clearGLogCap(s []celtGLog) {
	clear(s[:cap(s)])
}

func clearSigCap(s []celtSig) {
	clear(s[:cap(s)])
}

func clearNormCap(s []celtNorm) {
	clear(s[:cap(s)])
}

func clearInt32Cap(s []int32) {
	clear(s[:cap(s)])
}

func clearByteCap(s []byte) {
	clear(s[:cap(s)])
}

func clearUint32Cap(s []uint32) {
	clear(s[:cap(s)])
}

func clearComplex64Cap(s []complex64) {
	clear(s[:cap(s)])
}

func clearKissCpxCap(s []kissCpx) {
	clear(s[:cap(s)])
}
