//go:build gopus_fixed_point && !gopus_qext

package multistream

import (
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/fixedpoint"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

type streamFixedQEXTFields struct{}

func (d *streamState) fixedCELTDownsample() int {
	downsample := 48000 / int(d.sampleRate)
	if downsample <= 0 {
		return 1
	}
	return downsample
}

func (d *streamState) beginFixedCELTTransition(mode int, gainQ8 int32) {
	d.fixedTransitionArmed = d.fixedCELT != nil && d.haveDecoded &&
		((mode != streamModeCELT && d.lastMode == streamModeCELT) ||
			(mode == streamModeCELT && (d.lastMode == streamModeSILK || d.lastMode == streamModeHybrid) && !d.prevRedundancy))
	d.fixedTransitionReady = false
	d.fixedTransitionHasMain = false
	d.fixedTransitionGainQ8 = gainQ8
}

func (d *streamState) canCaptureFixedHybridTransition() bool {
	return d.fixedCELT != nil
}

func (d *streamState) endFixedCELTTransition() {
	d.fixedTransitionArmed = false
	d.fixedTransitionReady = false
	d.fixedTransitionHasMain = false
}

func (d *streamState) captureFixedCELTTransition(main []float32, frameSize, transSize int, active bool) {
	if !d.fixedTransitionArmed {
		return
	}
	d.fixedTransitionArmed = false
	if !active || transSize <= 0 || d.fixedCELT == nil {
		return
	}
	channels := int(d.channels)
	needed := transSize * channels
	if main != nil && len(main) < needed {
		return
	}
	downsample := d.fixedCELTDownsample()
	coreFrameSize := transSize * downsample
	if cap(d.fixedCELTPCM) < needed {
		d.fixedCELTPCM = make([]int16, needed)
	}
	if d.fixedCELT.DecodeWithECChannels(nil, coreFrameSize, fixedCELTCodedChannels(d.lastPacketStereo), d.fixedCELTPCM[:needed]) != transSize {
		return
	}
	transition := d.fixedCELT.LastRes()
	if len(transition) < needed {
		return
	}
	if cap(d.fixedTransitionRes) < needed {
		d.fixedTransitionRes = make([]int32, needed)
	}
	if cap(d.fixedTransitionMain) < needed {
		d.fixedTransitionMain = make([]int32, needed)
	}
	d.fixedTransitionRes = d.fixedTransitionRes[:needed]
	d.fixedTransitionMain = d.fixedTransitionMain[:needed]
	copy(d.fixedTransitionRes, transition[:needed])
	if main != nil {
		floatToRes(d.fixedTransitionMain, main[:needed])
	}
	if d.fixedTransitionGainQ8 != 0 {
		fixedpoint.ApplyDecodeGainRes(d.fixedTransitionRes, fixedpoint.DecodeGainQ16(int(d.fixedTransitionGainQ8)))
	}
	d.fixedTransitionHasMain = main != nil
	d.fixedTransitionReady = true
}

func (d *streamState) captureFixedSILKTransition(transition []float32, transSize int, active bool) {
	if !d.fixedTransitionArmed {
		return
	}
	d.fixedTransitionArmed = false
	if !active || transSize <= 0 || d.fixedCELT == nil {
		return
	}
	channels := int(d.channels)
	needed := transSize * channels
	if len(transition) < needed {
		return
	}
	if cap(d.fixedTransitionRes) < needed {
		d.fixedTransitionRes = make([]int32, needed)
	}
	d.fixedTransitionRes = d.fixedTransitionRes[:needed]
	floatToRes(d.fixedTransitionRes, transition[:needed])
	if d.fixedTransitionGainQ8 != 0 {
		fixedpoint.ApplyDecodeGainRes(d.fixedTransitionRes, fixedpoint.DecodeGainQ16(int(d.fixedTransitionGainQ8)))
	}
	d.fixedTransitionHasMain = false
	d.fixedTransitionReady = true
}

func (d *streamState) applyFixedCELTTransition(res []int32, frameSize int) {
	if !d.fixedTransitionReady {
		return
	}
	d.fixedTransitionReady = false
	channels := int(d.channels)
	needed := len(d.fixedTransitionRes)
	if channels <= 0 || needed > len(res) {
		return
	}
	if d.fixedTransitionHasMain {
		if needed > len(d.fixedTransitionMain) {
			return
		}
		copy(res[:needed], d.fixedTransitionMain[:needed])
	}
	f5 := int(d.sampleRate) / 200
	f2_5 := int(d.sampleRate) / 400
	if frameSize >= f5 {
		first := f2_5 * channels
		if first > needed {
			return
		}
		copy(res[:first], d.fixedTransitionRes[:first])
		fixedpoint.SmoothFadeRes(d.fixedTransitionRes[first:needed], res[first:needed], res[first:needed], f2_5, channels, int(d.sampleRate))
	} else {
		fixedpoint.SmoothFadeRes(d.fixedTransitionRes, res[:needed], res[:needed], f2_5, channels, int(d.sampleRate))
	}
}

func (d *streamState) prepareFixedCELTFrame(mode int, _ parsedOpusPacket, toc streamTOC, forceReset bool) error {
	if d.fixedCELT == nil {
		d.fixedCELT = fixedpoint.NewCELTDecoderRate(int(d.channels), int(d.sampleRate))
	}
	if forceReset || (d.haveDecoded && int(d.lastMode) != mode && !d.prevRedundancy) {
		d.fixedCELT.Reset()
	}
	d.fixedCELT.SetPhaseInversionDisabled(d.celtDec.PhaseInversionDisabled())
	startBand := 0
	if mode == streamModeHybrid {
		startBand = celt.HybridCELTStartBand
	}
	d.fixedCELT.SetBandRange(startBand, celt.BandwidthFromOpusConfig(toc.bandwidth).EffectiveBands())
	return nil
}

func (d *streamState) prepareFixedSILKRedundancy(toc streamTOC) error {
	if d.fixedCELT == nil {
		d.fixedCELT = fixedpoint.NewCELTDecoderRate(int(d.channels), int(d.sampleRate))
	}
	d.fixedHybridPrevMode = d.lastMode
	d.fixedHybridPrevRedundancy = d.prevRedundancy
	d.fixedHybridRedundant = false
	d.fixedHybridRedundantToSilk = false
	d.fixedHybridRedundantData = nil
	d.fixedHybridRedundantValid = false
	d.fixedHybridCodedChannels = fixedCELTCodedChannels(toc.stereo)
	d.fixedHybridEnd = celt.BandwidthFromOpusConfig(toc.bandwidth).EffectiveBands()
	d.fixedCELT.SetPhaseInversionDisabled(d.celtDec.PhaseInversionDisabled())
	d.fixedCELT.SetBandRange(0, d.fixedHybridEnd)
	return nil
}

func (d *streamState) prepareFixedHybridStream(toc streamTOC) (bool, error) {
	d.fixedHybridPrevMode = d.lastMode
	d.fixedHybridPrevRedundancy = d.prevRedundancy
	if d.fixedCELT == nil {
		d.fixedCELT = fixedpoint.NewCELTDecoderRate(int(d.channels), int(d.sampleRate))
	}
	if d.fixedHybridHook == nil {
		d.fixedHybridHook = &streamFixedHybridHook{st: d}
	}
	d.fixedHybridEnd = celt.BandwidthFromOpusConfig(toc.bandwidth).EffectiveBands()
	d.fixedCELT.SetPhaseInversionDisabled(d.celtDec.PhaseInversionDisabled())
	d.fixedHybridRedundant = false
	d.fixedHybridHandled = false
	d.hybridDec.SetFixedHighband(d.fixedHybridHook)
	return true, nil
}

// prepareFixedHybridHighband preserves the previous CELT history until the
// redundancy flags decide whether opus_decode_frame needs transition PLC.
func (d *streamState) prepareFixedHybridHighband(frameSize int) {
	if d.fixedTransitionArmed && !d.fixedHybridRedundant {
		d.captureFixedCELTTransition(nil, frameSize, min(frameSize, int(d.sampleRate)/200), true)
	}
	if d.haveDecoded && d.fixedHybridPrevMode != streamModeHybrid && !d.fixedHybridPrevRedundancy {
		d.fixedCELT.Reset()
	}
	d.fixedCELT.SetBandRange(celt.HybridCELTStartBand, d.fixedHybridEnd)
}

func (d *streamState) celtFixedRes(parsed parsedOpusPacket, frameSize int, toc streamTOC, res []int32) bool {
	if len(parsed.frames) == 0 || frameSize%len(parsed.frames) != 0 {
		return false
	}
	channels := int(d.channels)
	if d.fixedCELT == nil {
		d.fixedCELT = fixedpoint.NewCELTDecoderRate(channels, int(d.sampleRate))
	}
	codedChannels := fixedCELTCodedChannels(toc.stereo)
	downsample := d.fixedCELTDownsample()
	frameSizePerPacketFrame := frameSize / len(parsed.frames)
	coreFrameSize := frameSizePerPacketFrame * downsample
	d.fixedCELT.SetBandRange(0, celt.BandwidthFromOpusConfig(toc.bandwidth).EffectiveBands())
	needed := frameSizePerPacketFrame * channels
	if cap(d.fixedCELTPCM) < needed {
		d.fixedCELTPCM = make([]int16, needed)
	}
	for i, frame := range parsed.frames {
		if len(frame) <= 1 {
			return false
		}
		d.fixedCELT.DecodeWithECChannels(frame, coreFrameSize, codedChannels, d.fixedCELTPCM[:needed])
		celtRes := d.fixedCELT.LastRes()
		if len(celtRes) < needed {
			return false
		}
		frameRes := res[i*needed : (i+1)*needed]
		copy(frameRes, celtRes[:needed])
	}
	return true
}

func (d *streamState) canDecodeLostFixed() bool {
	if d.lastTOCFrameSize <= 0 {
		return false
	}
	return d.concealmentMode() == streamModeSILK || d.fixedCELT != nil
}

func (d *streamState) decodeLostFixed(frameSize int, floatPCM []float32) ([]int32, error) {
	channels := int(d.channels)
	mode := d.concealmentMode()
	needed := frameSize * channels
	if cap(d.fixedRes) < needed {
		d.fixedRes = make([]int32, needed)
	} else {
		d.fixedRes = d.fixedRes[:needed]
	}
	if mode == streamModeSILK {
		if len(floatPCM) < needed {
			return nil, ErrInvalidPacket
		}
		floatToRes(d.fixedRes, floatPCM[:needed])
		return d.fixedRes, nil
	}
	if d.fixedCELT == nil {
		return nil, ErrInvalidPacket
	}
	if cap(d.fixedCELTPCM) < needed {
		d.fixedCELTPCM = make([]int16, needed)
	}
	downsample := d.fixedCELTDownsample()
	frameSize20ms := int(d.sampleRate) / 50
	chunkLimit := min(frameSize20ms, int(d.lastTOCFrameSize))
	if chunkLimit <= 0 {
		return nil, ErrInvalidPacket
	}
	if mode == streamModeHybrid {
		if len(d.fixedHybridPLCLowband) < needed {
			return nil, ErrInvalidPacket
		}
		d.fixedCELT.SetStartBand(celt.HybridCELTStartBand)
	}
	for offset := 0; offset < frameSize; {
		chunk := nextCELTPLCChunk(frameSize-offset, chunkLimit, frameSize20ms)
		coreFrameSize := chunk * downsample
		start := offset * channels
		end := start + chunk*channels
		if mode == streamModeHybrid {
			for i, sample := range d.fixedHybridPLCLowband[start:end] {
				d.fixedRes[start+i] = int32(sample) << 8
			}
			if decoded := d.fixedCELT.DecodeLostAccum(coreFrameSize, d.fixedRes[start:end]); decoded != chunk {
				return nil, ErrInvalidPacket
			}
		} else {
			if decoded := d.fixedCELT.DecodeWithECChannels(nil, coreFrameSize, fixedCELTCodedChannels(d.lastPacketStereo), d.fixedCELTPCM[start:end]); decoded != chunk {
				return nil, ErrInvalidPacket
			}
			lastRes := d.fixedCELT.LastRes()
			if len(lastRes) < chunk*channels {
				return nil, ErrInvalidPacket
			}
			copy(d.fixedRes[start:end], lastRes[:chunk*channels])
		}
		offset += chunk
	}
	return d.fixedRes, nil
}

func (d *streamState) decodeFixedHybridAccum(rd *rangecoding.Decoder, dataLen, coreFrameSize int, packetStereo bool, accum []int32) bool {
	if d.fixedCELT == nil {
		return false
	}
	d.fixedCELT.SetBandRange(celt.HybridCELTStartBand, d.fixedHybridEnd)
	downsample := d.fixedCELTDownsample()
	return d.fixedCELT.DecodeHybridAccumChannels(rd, dataLen, coreFrameSize, fixedCELTCodedChannels(packetStereo), accum) == coreFrameSize/downsample
}

func (d *streamState) decodeFixedRedundantCELT(reset bool) bool {
	if !d.fixedHybridRedundant || len(d.fixedHybridRedundantData) <= 1 || d.fixedCELT == nil {
		return false
	}
	channels := int(d.channels)
	downsample := d.fixedCELTDownsample()
	f5 := int(d.sampleRate) / 200
	needed := f5 * channels
	coreFrameSize := f5 * downsample
	if cap(d.fixedHybridRedundantRes) < needed {
		d.fixedHybridRedundantRes = make([]int32, needed)
	}
	if cap(d.fixedCELTPCM) < needed {
		d.fixedCELTPCM = make([]int16, needed)
	}
	if reset {
		d.fixedCELT.Reset()
	}
	d.fixedCELT.SetBandRange(0, d.fixedHybridEnd)
	if decoded := d.fixedCELT.DecodeWithECChannels(d.fixedHybridRedundantData, coreFrameSize, d.fixedHybridCodedChannels, d.fixedCELTPCM[:needed]); decoded != f5 {
		return false
	}
	res := d.fixedCELT.LastRes()
	if len(res) < needed {
		return false
	}
	d.fixedHybridRedundantRes = d.fixedHybridRedundantRes[:needed]
	copy(d.fixedHybridRedundantRes, res[:needed])
	d.fixedHybridRedundantValid = true
	return true
}

func (d *streamState) finishFixedRedundancy(res []int32, frameSize int) bool {
	if !d.fixedHybridRedundant {
		return true
	}
	if !d.fixedHybridRedundantValid && !d.decodeFixedRedundantCELT(!d.fixedHybridRedundantToSilk) {
		return false
	}
	channels := int(d.channels)
	f2_5 := int(d.sampleRate) / 400
	f5 := int(d.sampleRate) / 200
	needed := frameSize * channels
	if f2_5 <= 0 || len(res) < needed || len(d.fixedHybridRedundantRes) < f5*channels {
		return false
	}
	if d.fixedHybridRedundantToSilk {
		if d.fixedHybridPrevMode == streamModeSILK && !d.fixedHybridPrevRedundancy {
			return true
		}
		for c := 0; c < channels; c++ {
			for i := 0; i < f2_5; i++ {
				res[i*channels+c] = d.fixedHybridRedundantRes[i*channels+c]
			}
		}
		fadeIn1 := d.fixedHybridRedundantRes[f2_5*channels:]
		fadeIn2 := res[f2_5*channels:]
		fixedpoint.SmoothFadeRes(fadeIn1, fadeIn2, fadeIn2, f2_5, channels, int(d.sampleRate))
		return true
	}
	start := (frameSize - f2_5) * channels
	if start < 0 {
		return false
	}
	fixedpoint.SmoothFadeRes(res[start:], d.fixedHybridRedundantRes[f2_5*channels:], res[start:], f2_5, channels, int(d.sampleRate))
	return true
}

func (d *streamState) resetFixedDecoderState() {
	if d.fixedCELT != nil {
		d.fixedCELT.Reset()
	}
}

func (d *streamState) prepareFixedHybridQEXTPayload(_ parsedOpusPacket) {}
