//go:build gopus_fixed_point

package encoder

import (
	"math/bits"

	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/fixedpoint"
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

type encoderFixedOuterQ8Fields struct {
	fixedPrevHBGainQ15 int16
	fixedSilkPrefillQ8 []int32
	fixedCELTPrefillQ8 []int32
	fixedSilkPrefillOK bool
	fixedCELTPrefillOK bool
}

func newFixedOuterQ8Fields() encoderFixedOuterQ8Fields {
	return encoderFixedOuterQ8Fields{fixedPrevHBGainQ15: 1<<15 - 1}
}

func (e *Encoder) resetFixedOuterQ8() {
	e.fixedPrevHBGainQ15 = 1<<15 - 1
	e.fixedSilkPrefillOK = false
	e.fixedCELTPrefillOK = false
	clear(e.fixedSilkPrefillQ8)
	clear(e.fixedCELTPrefillQ8)
}

func (e *Encoder) fixedHighBandGainQ15(celtRate int32) int16 {
	// celt_exp2 receives opus_val16 in fixed builds, so the C assignment
	// narrows -celtRate to int16 before evaluating its Q16 result. This matches
	// opus_encoder.c:2061 and celt/mathops.h; keep the wrap rather than clamping.
	return int16((1<<15 - 1) - (fixedpoint.CeltExp2(int16(-celtRate)) >> 1))
}

func (e *Encoder) fadeFixedHighBand(gainQ15 int16) {
	if !e.restrictedSilkApp && e.fixedFrameReady &&
		(e.fixedPrevHBGainQ15 < 1<<15-1 || gainQ15 < 1<<15-1) {
		applyFixedGainFade(e.fixedDelayed, int(e.channels), e.fixedPrevHBGainQ15, gainQ15, int(e.sampleRate))
	}
	e.fixedPrevHBGainQ15 = gainQ15
}

// fixedDCRejectRes ports src/opus_encoder.c:dc_reject for the selected
// FIXED_POINT+ENABLE_RES24 build. Input and output are opus_res int32 Q8; hpMem
// carries the C opus_val32 state, including its unused second word per channel.
func fixedDCRejectRes(input, output []int32, hpMem *[4]int32, fs, channels, cutoff int) {
	shift := bits.Len32(uint32(fs/(cutoff*4))) - 1
	const saturation = int32((1 << 24) - 1)
	for c := 0; c < channels; c++ {
		for i := c; i < len(input); i += channels {
			x := input[i]
			if x > saturation {
				x = saturation
			} else if x < -saturation {
				x = -saturation
			}
			x <<= 6 // SHL32(x,14-RES_SHIFT), RES_SHIFT=8.
			y := x - hpMem[2*c]
			hpMem[2*c] += (x - hpMem[2*c] + (1 << (shift - 1))) >> shift
			output[i] = (y + (1 << 5)) >> 6
		}
	}
}

// fixedFloatToRes ports FLOAT2RES/FLOAT2INT24 in celt/arch.h and
// celt/float_cast.h for the selected FIXED_POINT+ENABLE_RES24 build.
func fixedFloatToRes(sample float32) int32 {
	if sample > 2 {
		return 1 << 24
	}
	if sample < -2 {
		return -(1 << 24)
	}
	return opusmath.Float32ToInt24(sample)
}

// prepareFixedInputRes retains the integer coding input independently of the
// float32 policy/analysis path. Signed16 short input remains exact after the
// public lattice conversion, while float and signed24 APIs retain Q8 values.
func (e *Encoder) prepareFixedInputRes(pcm []float32) {
	if cap(e.fixedRawRes) < len(pcm) {
		e.fixedRawRes = make([]int32, len(pcm))
	}
	e.fixedRawRes = e.fixedRawRes[:len(pcm)]
	for i, sample := range pcm {
		e.fixedRawRes[i] = fixedFloatToRes(sample)
	}
	e.fixedInputActive = true
	e.fixedFrameReady = false
	e.fixedFrameCursor = 0
}

func (e *Encoder) clearFixedInputRes() {
	e.fixedInputActive = false
	e.fixedFrameReady = false
}

func (e *Encoder) preprocessFixedInputRes(frameSize int) {
	channels := int(e.channels)
	frameSamples := frameSize * channels
	if !e.fixedInputActive || frameSize <= 0 ||
		e.fixedFrameCursor < 0 || e.fixedFrameCursor+frameSamples > len(e.fixedRawRes) {
		return
	}
	if cap(e.fixedFiltered) < len(e.fixedRawRes) {
		e.fixedFiltered = make([]int32, len(e.fixedRawRes))
	}
	e.fixedFiltered = e.fixedFiltered[:len(e.fixedRawRes)]
	rawFrame := e.fixedRawRes[e.fixedFrameCursor : e.fixedFrameCursor+frameSamples]
	filteredFrame := e.fixedFiltered[e.fixedFrameCursor : e.fixedFrameCursor+frameSamples]
	if e.voipApp {
		// hp_cutoff is called once per native frame, after the Opus-level
		// variable-HP smoother has advanced for that frame.
		cutoffHz := silk.VariableHPCutoffHz(e.variableHPSmth2Q15)
		silk.HPCutoffRes24(rawFrame, filteredFrame, &e.fixedHPMem,
			e.sampleRate, e.channels, cutoffHz)
	} else if extsupport.QEXT && e.qextActive() {
		// opus_encoder.c:2005-2008 copies the QEXT input directly in non-VoIP
		// mode, preserving hp_mem instead of advancing dc_reject state.
		copy(filteredFrame, rawFrame)
	} else {
		fixedDCRejectRes(rawFrame, filteredFrame, &e.fixedHPMem, int(e.sampleRate), channels, 3)
	}
}

func (e *Encoder) prepareFixedCELTPCM(frameSize int) {
	e.fixedFrameReady = false
	channels := int(e.channels)
	frameSamples := frameSize * channels
	if !e.fixedInputActive || e.fixedFrameCursor+frameSamples > len(e.fixedFiltered) {
		return
	}
	e.fixedFrameSource = e.fixedFiltered[e.fixedFrameCursor : e.fixedFrameCursor+frameSamples]
	if cap(e.fixedDelayed) < frameSamples {
		e.fixedDelayed = make([]int32, frameSamples)
	}
	e.fixedDelayed = e.fixedDelayed[:frameSamples]
	if e.lowDelay {
		copy(e.fixedDelayed, e.fixedFrameSource)
		e.fixedFrameReady = true
		return
	}
	fs := int(e.sampleRate)
	delaySamples := (fs / 250) * channels
	bufferSamples := (fs / 100) * channels
	if len(e.fixedDelayBuffer) != bufferSamples {
		e.fixedDelayBuffer = make([]int32, bufferSamples)
	}
	tail := bufferSamples - delaySamples
	if frameSamples <= delaySamples {
		copy(e.fixedDelayed, e.fixedDelayBuffer[tail:tail+frameSamples])
	} else {
		copy(e.fixedDelayed, e.fixedDelayBuffer[tail:])
		copy(e.fixedDelayed[delaySamples:], e.fixedFrameSource[:frameSamples-delaySamples])
	}
	e.fixedFrameReady = true
}

// fixedSILKInputQ8 returns the selected FIXED_POINT+ENABLE_RES24 input frame
// after the outer encoder's Q8 high-pass. SILK applies RES2INT16 while loading
// API-rate samples in silk/enc_API.c.
func (e *Encoder) fixedSILKInputQ8(frameSize int) []int32 {
	if !e.fixedFrameReady || len(e.fixedFrameSource) != frameSize*int(e.channels) {
		return nil
	}
	return e.fixedFrameSource
}

func (e *Encoder) fixedHybridCELTPCMQ8(frameSize int) []int32 {
	if !e.fixedFrameReady || len(e.fixedDelayed) != frameSize*int(e.channels) {
		return nil
	}
	return e.fixedDelayed
}

func (e *Encoder) fixedFrameSliceQ8(offset, frameSize int) []int32 {
	start := offset * int(e.channels)
	end := start + frameSize*int(e.channels)
	if !e.fixedFrameReady || start < 0 || end > len(e.fixedDelayed) {
		return nil
	}
	return e.fixedDelayed[start:end]
}

func (e *Encoder) stageFixedSILKPrefill(captureCELTPrefill bool) {
	if !e.fixedInputActive {
		return
	}
	channels := int(e.channels)
	frameSize := int(e.sampleRate) / 100
	frameSamples := frameSize * channels
	if cap(e.fixedSilkPrefillQ8) < frameSamples {
		e.fixedSilkPrefillQ8 = make([]int32, frameSamples)
	}
	e.fixedSilkPrefillQ8 = e.fixedSilkPrefillQ8[:frameSamples]
	clear(e.fixedSilkPrefillQ8)
	if len(e.fixedDelayBuffer) >= frameSamples {
		copy(e.fixedSilkPrefillQ8, e.fixedDelayBuffer[:frameSamples])
	} else if len(e.fixedDelayBuffer) > 0 {
		copy(e.fixedSilkPrefillQ8[frameSamples-len(e.fixedDelayBuffer):], e.fixedDelayBuffer)
	}

	prefillSamples := int(e.sampleRate) / 400
	delayComp := int(e.sampleRate) / 250
	rampStart := frameSize - delayComp - prefillSamples
	rampStart = min(max(rampStart, 0), frameSize)
	clear(e.fixedSilkPrefillQ8[:rampStart*channels])
	if prefillSamples > 0 && rampStart < frameSize {
		end := min(frameSize, rampStart+prefillSamples)
		applyFixedGainFade(e.fixedSilkPrefillQ8[rampStart*channels:end*channels], channels, 0, 1<<15-1, int(e.sampleRate))
	}
	e.fixedSilkPrefillOK = true
	e.fixedCELTPrefillOK = false
	if !captureCELTPrefill || prefillSamples <= 0 || rampStart+prefillSamples > frameSize {
		return
	}
	if cap(e.fixedCELTPrefillQ8) < prefillSamples*channels {
		e.fixedCELTPrefillQ8 = make([]int32, prefillSamples*channels)
	}
	e.fixedCELTPrefillQ8 = e.fixedCELTPrefillQ8[:prefillSamples*channels]
	copy(e.fixedCELTPrefillQ8, e.fixedSilkPrefillQ8[rampStart*channels:(rampStart+prefillSamples)*channels])
	e.fixedCELTPrefillOK = true
}

func (e *Encoder) captureFixedCELTTransitionPrefill() {
	if e.fixedCELTPrefillOK || !e.fixedInputActive || e.lowDelay {
		return
	}
	channels := int(e.channels)
	prefillSamples := int(e.sampleRate) / 400
	start := int(e.sampleRate)/100 - int(e.sampleRate)/250 - prefillSamples
	if prefillSamples <= 0 || start < 0 {
		return
	}
	count := prefillSamples * channels
	if cap(e.fixedCELTPrefillQ8) < count {
		e.fixedCELTPrefillQ8 = make([]int32, count)
	}
	e.fixedCELTPrefillQ8 = e.fixedCELTPrefillQ8[:count]
	clear(e.fixedCELTPrefillQ8)
	first := start * channels
	last := first + count
	if last <= len(e.fixedDelayBuffer) {
		copy(e.fixedCELTPrefillQ8, e.fixedDelayBuffer[first:last])
	}
	e.fixedCELTPrefillOK = true
}

func (e *Encoder) fixedSILKPrefillInputQ8(frameSize int) []int32 {
	if !e.fixedSilkPrefillOK || len(e.fixedSilkPrefillQ8) != frameSize*int(e.channels) {
		return nil
	}
	return e.fixedSilkPrefillQ8
}

func (e *Encoder) fixedCELTTransitionPrefillInputQ8(frameSize int) []int32 {
	if !e.fixedCELTPrefillOK || len(e.fixedCELTPrefillQ8) != frameSize*int(e.channels) {
		return nil
	}
	return e.fixedCELTPrefillQ8
}

func (e *Encoder) consumeFixedCELTPrefill() { e.fixedCELTPrefillOK = false }

func (e *Encoder) consumeFixedSILKPrefill() { e.fixedSilkPrefillOK = false }

func (e *Encoder) encodeSILKPrefill(prefill, activity int) error {
	if fixedPCM := e.fixedSILKPrefillInputQ8(int(e.sampleRate) / 100); fixedPCM != nil {
		_, err := e.silk.EncodeResQ8(&e.silkMode, fixedPCM, int(e.sampleRate)/100, nil, prefill, activity)
		e.consumeFixedSILKPrefill()
		return err
	}
	_, err := e.silk.Encode(&e.silkMode, e.scratchSilkPrefill, int(e.sampleRate)/100, nil, prefill, activity)
	return err
}

func (e *Encoder) encodeSILKFrame(pcm []opusRes, frameSize int, re *rangecoding.Encoder, activity int) (int32, error) {
	if fixedPCM := e.fixedSILKInputQ8(frameSize); fixedPCM != nil {
		return e.silk.EncodeResQ8(&e.silkMode, fixedPCM, frameSize, re, 0, activity)
	}
	return e.silk.Encode(&e.silkMode, pcm, frameSize, re, 0, activity)
}

func (e *Encoder) advanceFixedInputCursor(frameSize int) {
	frameSamples := frameSize * int(e.channels)
	if !e.fixedFrameReady || len(e.fixedFrameSource) != frameSamples {
		return
	}
	e.fixedFrameCursor += frameSamples
	e.fixedFrameReady = false
}

func (e *Encoder) updateFixedDelayBuffer(frameSize int) {
	frameSamples := frameSize * int(e.channels)
	if !e.fixedFrameReady || len(e.fixedFrameSource) != frameSamples {
		return
	}
	bufferSamples := (int(e.sampleRate) / 100) * int(e.channels)
	if len(e.fixedDelayBuffer) != bufferSamples {
		e.fixedDelayBuffer = make([]int32, bufferSamples)
	}
	if frameSamples >= bufferSamples {
		copy(e.fixedDelayBuffer, e.fixedFrameSource[frameSamples-bufferSamples:])
		e.fixedFrameCursor += frameSamples
		return
	}
	keep := bufferSamples - frameSamples
	copy(e.fixedDelayBuffer[:keep], e.fixedDelayBuffer[frameSamples:])
	copy(e.fixedDelayBuffer[keep:], e.fixedFrameSource)
	e.fixedFrameCursor += frameSamples
}

func (e *Encoder) applyFixedStereoWidth(prevWidthQ14 int16) {
	if !e.fixedFrameReady || e.channels != 2 || len(e.celtEnergyMask) != 0 || e.restrictedSilkApp {
		return
	}
	widthQ14 := e.hybridStereoWidthQ14
	if prevWidthQ14 < 1<<14 || widthQ14 < 1<<14 {
		applyFixedStereoFade(e.fixedDelayed, prevWidthQ14, widthQ14, int(e.sampleRate))
	}
}
