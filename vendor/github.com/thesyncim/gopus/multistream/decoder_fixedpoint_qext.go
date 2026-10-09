//go:build gopus_fixed_point && gopus_qext

package multistream

import (
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/fixedpoint"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

type streamFixedQEXTFields struct {
	decoder      *fixedpoint.QEXTCELTDecoder
	rangeDecoder rangecoding.Decoder
	payloads     streamQEXTPayloads
}

func (d *streamState) fixedCELTDownsample() int {
	if d.sampleRate == 96000 {
		return 1
	}
	downsample := 48000 / int(d.sampleRate)
	if downsample <= 0 {
		return 1
	}
	return downsample
}

func (d *streamState) beginFixedCELTTransition(mode int, gainQ8 int32) {
	d.fixedTransitionArmed = d.qext.decoder != nil && d.haveDecoded &&
		((mode != streamModeCELT && d.lastMode == streamModeCELT) ||
			(mode == streamModeCELT && (d.lastMode == streamModeSILK || d.lastMode == streamModeHybrid) && !d.prevRedundancy))
	d.fixedTransitionReady = false
	d.fixedTransitionHasMain = false
	d.fixedTransitionGainQ8 = gainQ8
}

func (d *streamState) canCaptureFixedHybridTransition() bool {
	return d.qext.decoder != nil
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
	if !active || transSize <= 0 || d.qext.decoder == nil {
		return
	}
	channels := int(d.channels)
	needed := transSize * channels
	if main != nil && len(main) < needed {
		return
	}
	downsample := d.fixedCELTDownsample()
	coreFrameSize := transSize * downsample
	if cap(d.fixedTransitionRes) < needed {
		d.fixedTransitionRes = make([]int32, needed)
	}
	if cap(d.fixedTransitionMain) < needed {
		d.fixedTransitionMain = make([]int32, needed)
	}
	d.fixedTransitionRes = d.fixedTransitionRes[:needed]
	d.fixedTransitionMain = d.fixedTransitionMain[:needed]
	if d.qext.decoder.DecodeLost(coreFrameSize, d.fixedTransitionRes) != transSize {
		return
	}
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
	if !active || transSize <= 0 || d.qext.decoder == nil {
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
		fixedpoint.SmoothFadeResQEXT(d.fixedTransitionRes[first:needed], res[first:needed], res[first:needed], f2_5, channels, int(d.sampleRate))
	} else {
		fixedpoint.SmoothFadeResQEXT(d.fixedTransitionRes, res[:needed], res[:needed], f2_5, channels, int(d.sampleRate))
	}
}

func (d *streamState) ensureFixedQEXTCELT() error {
	if d.qext.decoder != nil {
		return nil
	}
	decoder, err := fixedpoint.NewQEXTCELTDecoder(int(d.channels), int(d.sampleRate))
	if err != nil {
		return err
	}
	d.qext.decoder = decoder
	return nil
}

func (d *streamState) resetFixedQEXTForMode(mode int, forceReset bool) error {
	if err := d.ensureFixedQEXTCELT(); err != nil {
		return err
	}
	if forceReset || (d.haveDecoded && int(d.lastMode) != mode && !d.prevRedundancy) {
		d.qext.decoder.Reset()
	}
	d.qext.decoder.SetPhaseInversionDisabled(d.celtDec.PhaseInversionDisabled())
	return nil
}

func (d *streamState) prepareFixedCELTFrame(mode int, parsed parsedOpusPacket, toc streamTOC, forceReset bool) error {
	if mode == streamModeCELT {
		if d.ignoreExtensions || len(parsed.padding) == 0 {
			d.qext.payloads.collect(nil, 0, qextPacketExtensionID)
		} else {
			d.qext.payloads.collect(parsed.padding, parsed.paddingFrameCount, qextPacketExtensionID)
		}
	}
	if err := d.resetFixedQEXTForMode(mode, forceReset); err != nil {
		return err
	}
	startBand := 0
	if mode == streamModeHybrid {
		startBand = celt.HybridCELTStartBand
	}
	d.qext.decoder.SetBandRange(startBand, celt.BandwidthFromOpusConfig(toc.bandwidth).EffectiveBands())
	return nil
}

func (d *streamState) prepareFixedSILKRedundancy(toc streamTOC) error {
	if err := d.ensureFixedQEXTCELT(); err != nil {
		return err
	}
	d.fixedHybridPrevMode = d.lastMode
	d.fixedHybridPrevRedundancy = d.prevRedundancy
	d.fixedHybridRedundant = false
	d.fixedHybridRedundantToSilk = false
	d.fixedHybridRedundantData = nil
	d.fixedHybridRedundantValid = false
	d.fixedHybridCodedChannels = fixedCELTCodedChannels(toc.stereo)
	d.fixedHybridEnd = celt.BandwidthFromOpusConfig(toc.bandwidth).EffectiveBands()
	d.qext.decoder.SetPhaseInversionDisabled(d.celtDec.PhaseInversionDisabled())
	d.qext.decoder.SetBandRange(0, d.fixedHybridEnd)
	return nil
}

func (d *streamState) prepareFixedHybridStream(toc streamTOC) (bool, error) {
	d.fixedHybridPrevMode = d.lastMode
	d.fixedHybridPrevRedundancy = d.prevRedundancy
	if err := d.ensureFixedQEXTCELT(); err != nil {
		return false, err
	}
	if d.fixedHybridHook == nil {
		d.fixedHybridHook = &streamFixedHybridHook{st: d}
	}
	d.fixedHybridEnd = celt.BandwidthFromOpusConfig(toc.bandwidth).EffectiveBands()
	d.qext.decoder.SetPhaseInversionDisabled(d.celtDec.PhaseInversionDisabled())
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
		d.qext.decoder.Reset()
	}
	d.qext.decoder.SetBandRange(celt.HybridCELTStartBand, d.fixedHybridEnd)
}

func (d *streamState) celtFixedRes(parsed parsedOpusPacket, frameSize int, toc streamTOC, res []int32) bool {
	if len(parsed.frames) == 0 || d.qext.decoder == nil || frameSize%len(parsed.frames) != 0 {
		return false
	}
	downsample := d.fixedCELTDownsample()
	frameSizePerPacketFrame := frameSize / len(parsed.frames)
	coreFrameSize := frameSizePerPacketFrame * downsample
	channels := int(d.channels)
	codedChannels := fixedCELTCodedChannels(toc.stereo)
	frameSamples := frameSizePerPacketFrame * channels
	if len(res) < frameSamples*len(parsed.frames) {
		return false
	}
	for i, frame := range parsed.frames {
		if len(frame) <= 1 {
			return false
		}
		d.qext.rangeDecoder.Init(frame)
		frameRes := res[i*frameSamples : (i+1)*frameSamples]
		decoded := d.qext.decoder.DecodeFrameWithEC(&d.qext.rangeDecoder, len(frame), coreFrameSize, codedChannels, d.qext.payloads.frame(i), frameRes)
		if decoded != frameSizePerPacketFrame {
			return false
		}
	}
	return true
}

func (d *streamState) canDecodeLostFixed() bool {
	if d.lastTOCFrameSize <= 0 {
		return false
	}
	return d.concealmentMode() == streamModeSILK || d.qext.decoder != nil
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
	if d.qext.decoder == nil {
		return nil, ErrInvalidPacket
	}
	res := d.fixedRes
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
		d.qext.decoder.SetStartBand(celt.HybridCELTStartBand)
	}
	for offset := 0; offset < frameSize; {
		chunk := nextCELTPLCChunk(frameSize-offset, chunkLimit, frameSize20ms)
		coreFrameSize := chunk * downsample
		start := offset * channels
		end := start + chunk*channels
		if mode == streamModeHybrid {
			for i, sample := range d.fixedHybridPLCLowband[start:end] {
				res[start+i] = int32(sample) << 8
			}
			if decoded := d.qext.decoder.DecodeLostAccum(coreFrameSize, res[start:end]); decoded != chunk {
				return nil, ErrInvalidPacket
			}
		} else {
			if decoded := d.qext.decoder.DecodeLost(coreFrameSize, res[start:end]); decoded != chunk {
				return nil, ErrInvalidPacket
			}
		}
		offset += chunk
	}
	return res, nil
}

func (d *streamState) decodeFixedHybridAccum(rd *rangecoding.Decoder, dataLen, coreFrameSize int, packetStereo bool, accum []int32) bool {
	if d.qext.decoder == nil || rd == nil {
		return false
	}
	d.qext.decoder.SetBandRange(celt.HybridCELTStartBand, d.fixedHybridEnd)
	decoded := d.qext.decoder.DecodeHybridAccumWithEC(rd, dataLen, coreFrameSize, fixedCELTCodedChannels(packetStereo), d.qext.payloads.frame(0), accum)
	downsample := d.fixedCELTDownsample()
	return decoded == coreFrameSize/downsample
}

func (d *streamState) decodeFixedRedundantCELT(reset bool) bool {
	if !d.fixedHybridRedundant || len(d.fixedHybridRedundantData) <= 1 || d.qext.decoder == nil {
		return false
	}
	channels := int(d.channels)
	downsample := d.fixedCELTDownsample()
	f5 := int(d.sampleRate) / 200
	needed := f5 * channels
	coreFrameSize := f5 * downsample
	if cap(d.fixedHybridRedundantRes) < needed {
		d.fixedHybridRedundantRes = make([]int32, needed)
	} else {
		d.fixedHybridRedundantRes = d.fixedHybridRedundantRes[:needed]
	}
	if reset {
		d.qext.decoder.Reset()
	}
	d.qext.decoder.SetBandRange(0, d.fixedHybridEnd)
	d.qext.rangeDecoder.Init(d.fixedHybridRedundantData)
	if decoded := d.qext.decoder.DecodeFrameWithEC(&d.qext.rangeDecoder, len(d.fixedHybridRedundantData), coreFrameSize, d.fixedHybridCodedChannels, nil, d.fixedHybridRedundantRes); decoded != f5 {
		return false
	}
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
		fixedpoint.SmoothFadeResQEXT(fadeIn1, fadeIn2, fadeIn2, f2_5, channels, int(d.sampleRate))
		return true
	}
	start := (frameSize - f2_5) * channels
	if start < 0 {
		return false
	}
	fixedpoint.SmoothFadeResQEXT(res[start:], d.fixedHybridRedundantRes[f2_5*channels:], res[start:], f2_5, channels, int(d.sampleRate))
	return true
}

func (d *streamState) resetFixedDecoderState() {
	if d.qext.decoder != nil {
		d.qext.decoder.Reset()
	}
}

func (d *streamState) prepareFixedHybridQEXTPayload(parsed parsedOpusPacket) {
	if d.ignoreExtensions || len(parsed.padding) == 0 {
		d.qext.payloads.collect(nil, 0, qextPacketExtensionID)
		return
	}
	d.qext.payloads.collect(parsed.padding, parsed.paddingFrameCount, qextPacketExtensionID)
}
