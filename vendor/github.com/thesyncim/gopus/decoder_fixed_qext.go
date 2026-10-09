//go:build gopus_fixed_point && gopus_qext

package gopus

import (
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/fixedpoint"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

func (d *Decoder) prepareFixedQEXTHybrid(data []byte, celtBW celt.CELTBandwidth, needCeltReset, packetStereo bool, qextPayload []byte) bool {
	if !d.fixedPacketActive || len(data) <= 1 || d.fixedQEXT.invalid {
		return false
	}
	if d.fixedQEXT.decoder == nil {
		decoder, err := fixedpoint.NewQEXTCELTDecoder(int(d.channels), int(d.sampleRate))
		if err != nil {
			return false
		}
		d.fixedQEXT.decoder = decoder
	}
	d.fixedQEXT.hybridActive = true
	d.fixedQEXT.hybridPayload = qextPayload
	d.fixedQEXT.hybridChannels = 1
	if packetStereo {
		d.fixedQEXT.hybridChannels = 2
	}
	d.fixedQEXT.redundantRangeValid = false
	d.fixedHybridEnd = celtBW.EffectiveBands()
	d.fixedHybridReset = needCeltReset
	d.fixedHybridErr = nil
	d.fixedRedundantValid = false
	d.fixedTransitionValid = false
	d.fixedHybridFrameActive = true
	d.hybridDecoder.SetFixedHighband((*fixedHybridHighbandHook)(d))
	return true
}

func (d *Decoder) decodeFixedQEXTHybridHighband(silkInt16 []int16, filled int, main *rangecoding.Decoder, dataLen, frameSizeAPI, frameSize48 int, packetStereo bool) bool {
	if !d.fixedHybridArmed() || !d.fixedQEXT.hybridActive || d.fixedQEXT.decoder == nil || main == nil {
		return false
	}
	channels := int(d.channels)
	needed := frameSizeAPI * channels
	if cap(d.fixedHybridRes) < needed {
		d.fixedHybridRes = make([]int32, needed)
	}
	res := d.fixedHybridRes[:needed]
	for i := 0; i < needed; i++ {
		var sample int16
		if i < filled && i < len(silkInt16) {
			sample = silkInt16[i]
		}
		res[i] = int32(sample) << 8
	}

	if d.fixedHybridReset {
		d.fixedQEXT.decoder.Reset()
	}
	d.fixedQEXT.decoder.SetPhaseInversionDisabled(d.celtDecoder.PhaseInversionDisabled())
	d.fixedQEXT.decoder.SetBandRange(celt.HybridCELTStartBand, d.fixedHybridEnd)
	downsample := 48000 / int(d.sampleRate)
	if downsample <= 0 {
		downsample = 1
	}
	coreFrameSize := frameSizeAPI * downsample
	wantCoreFrameSize := frameSize48
	if d.sampleRate == 96000 {
		coreFrameSize = frameSizeAPI
		wantCoreFrameSize *= 2
	}
	if wantCoreFrameSize > 0 && wantCoreFrameSize != coreFrameSize {
		d.fixedHybridErr = ErrInvalidPacket
		return true
	}
	codedChannels := 1
	if packetStereo {
		codedChannels = 2
	}
	decoded := d.fixedQEXT.decoder.DecodeHybridAccumWithEC(main, dataLen, coreFrameSize,
		codedChannels, d.fixedQEXT.hybridPayload, res)
	if decoded != frameSizeAPI {
		d.fixedHybridErr = ErrInvalidPacket
		return true
	}
	if cap(d.fixedHybridInt16) < needed {
		d.fixedHybridInt16 = make([]int16, needed)
	}
	int16Out := d.fixedHybridInt16[:needed]
	for i := range res {
		int16Out[i] = fixedpoint.Res2Int16(res[i])
	}
	d.appendFixedOutput(int16Out, res)
	d.mainDecodeRng = d.fixedQEXT.decoder.FinalRange()
	return true
}

func (d *Decoder) decodeFixedQEXTTransitionPLC(transSizeAPI int) bool {
	if !d.fixedHybridArmed() || !d.fixedQEXT.hybridActive || d.fixedQEXT.decoder == nil || transSizeAPI <= 0 {
		return false
	}
	channels := int(d.channels)
	needed := transSizeAPI * channels
	if cap(d.fixedTransitionRes) < needed {
		d.fixedTransitionRes = make([]int32, needed)
	}
	res := d.fixedTransitionRes[:needed]
	downsample := 48000 / int(d.sampleRate)
	if downsample <= 0 {
		downsample = 1
	}
	d.fixedQEXT.decoder.SetStartBand(0)
	if decoded := d.fixedQEXT.decoder.DecodeLost(transSizeAPI*downsample, res); decoded != transSizeAPI {
		return false
	}
	if d.decodeGainQ8 != 0 {
		fixedpoint.ApplyDecodeGainRes(res, fixedpoint.DecodeGainQ16(d.decodeGainQ8))
	}
	d.fixedTransitionRes = res
	d.fixedTransitionValid = true
	return true
}

func (d *Decoder) fixedAccumulateQEXTFECHybridToSILKFade(frameSizeAPI, fadeSamplesAPI int, packetStereo bool, celtBW celt.CELTBandwidth) bool {
	if !d.fixedPacketActive {
		return true
	}
	if d.fixedQEXT.invalid || d.fixedQEXT.decoder == nil || frameSizeAPI <= 0 || fadeSamplesAPI <= 0 || fadeSamplesAPI > frameSizeAPI {
		return false
	}
	channels := int(d.channels)
	needed := frameSizeAPI * channels
	fadeNeeded := fadeSamplesAPI * channels
	offset := len(d.fixedRes) - needed
	if offset < 0 || len(d.fixedInt16) != len(d.fixedRes) {
		return false
	}

	downsample := 48000 / int(d.sampleRate)
	if downsample <= 0 {
		downsample = 1
	}
	d.fixedQEXT.decoder.SetPhaseInversionDisabled(d.celtDecoder.PhaseInversionDisabled())
	d.fixedQEXT.decoder.SetBandRange(0, celtBW.EffectiveBands())
	codedChannels := 1
	if packetStereo {
		codedChannels = 2
	}
	rd := &d.scratchRangeDecoder
	rd.Init(celtSilenceFrame2B[:])
	if decoded := d.fixedQEXT.decoder.DecodeHybridAccumWithEC(
		rd, len(celtSilenceFrame2B), fadeSamplesAPI*downsample, codedChannels, nil, d.fixedRes[offset:offset+fadeNeeded],
	); decoded != fadeSamplesAPI {
		return false
	}
	for i := range fadeNeeded {
		d.fixedInt16[offset+i] = fixedpoint.Res2Int16(d.fixedRes[offset+i])
	}
	return true
}

func (d *Decoder) decodeFixedQEXTRedundantCELTWithChannels(redundantData []byte, celtBW celt.CELTBandwidth, reset bool, codedChannels int) bool {
	if !d.fixedPacketActive || d.fixedQEXT.invalid || codedChannels < 1 || codedChannels > 2 {
		return false
	}
	if d.fixedQEXT.decoder == nil {
		decoder, err := fixedpoint.NewQEXTCELTDecoder(int(d.channels), int(d.sampleRate))
		if err != nil {
			return false
		}
		d.fixedQEXT.decoder = decoder
	}
	downsample := 48000 / int(d.sampleRate)
	if downsample <= 0 {
		downsample = 1
	}
	frameSizeAPI := int(d.sampleRate) / 200
	needed := frameSizeAPI * int(d.channels)
	if cap(d.fixedRedundantRes) < needed {
		d.fixedRedundantRes = make([]int32, needed)
	}
	res := d.fixedRedundantRes[:needed]
	if reset {
		d.fixedQEXT.decoder.Reset()
	}
	d.fixedQEXT.decoder.SetPhaseInversionDisabled(d.celtDecoder.PhaseInversionDisabled())
	d.fixedQEXT.decoder.SetBandRange(0, celtBW.EffectiveBands())
	d.fixedQEXT.redundantDec.Init(redundantData)
	coreFrameSize := frameSizeAPI * downsample
	decoded := d.fixedQEXT.decoder.DecodeFrameWithEC(&d.fixedQEXT.redundantDec,
		len(redundantData), coreFrameSize, codedChannels, nil, res)
	if decoded != frameSizeAPI {
		d.fixedHybridErr = ErrInvalidPacket
		return true
	}
	d.fixedQEXT.redundantRange = d.fixedQEXT.decoder.FinalRange()
	d.fixedQEXT.redundantRangeValid = true
	d.fixedRedundantValid = true
	return true
}

func (d *Decoder) armFixedQEXTHybridLost(frameSizeAPI int, silkStereo bool) bool {
	if !d.fixedPacketActive || d.fixedQEXT.invalid || d.fixedQEXT.decoder == nil || frameSizeAPI <= 0 {
		return false
	}
	capLen := frameSizeAPI
	if silkStereo {
		capLen *= 2
	}
	if cap(d.fixedHybridPLCSilk) < capLen {
		d.fixedHybridPLCSilk = make([]int16, capLen)
	}
	d.fixedHybridPLCSilk = d.fixedHybridPLCSilk[:capLen]
	d.silkDecoder.ArmPLCLowbandCapture(d.fixedHybridPLCSilk)
	d.fixedHybridPLCStereo = silkStereo
	d.fixedHybridPLCChannels = int(d.channels)
	d.fixedQEXT.hybridActive = true
	d.fixedQEXT.hybridPayload = nil
	d.fixedHybridFrameActive = true
	return true
}

func (d *Decoder) finishFixedQEXTHybridLost(frameSizeAPI, filled int) bool {
	if !d.fixedQEXT.hybridActive || d.fixedQEXT.decoder == nil || frameSizeAPI <= 0 {
		return false
	}
	channels := d.fixedHybridPLCChannels
	needed := frameSizeAPI * channels
	if cap(d.fixedHybridPLCRes) < needed {
		d.fixedHybridPLCRes = make([]int32, needed)
	}
	res := d.fixedHybridPLCRes[:needed]
	src := d.fixedHybridPLCSilk
	switch {
	case channels == 2 && d.fixedHybridPLCStereo:
		for i := 0; i < needed; i++ {
			var sample int16
			if i < filled && i < len(src) {
				sample = src[i]
			}
			res[i] = int32(sample) << 8
		}
	case channels == 2:
		for i := 0; i < frameSizeAPI; i++ {
			var sample int16
			if i < filled && i < len(src) {
				sample = src[i]
			}
			v := int32(sample) << 8
			res[2*i], res[2*i+1] = v, v
		}
	default:
		for i := 0; i < needed; i++ {
			var sample int16
			if i < filled && i < len(src) {
				sample = src[i]
			}
			res[i] = int32(sample) << 8
		}
	}
	downsample := 48000 / int(d.sampleRate)
	if downsample <= 0 {
		downsample = 1
	}
	d.fixedQEXT.decoder.SetStartBand(celt.HybridCELTStartBand)
	if decoded := d.fixedQEXT.decoder.DecodeLostAccum(frameSizeAPI*downsample, res); decoded != frameSizeAPI {
		d.clearFixedQEXTHybrid()
		d.fixedHybridFrameActive = false
		return false
	}
	if cap(d.fixedHybridPLCInt16) < needed {
		d.fixedHybridPLCInt16 = make([]int16, needed)
	}
	int16Out := d.fixedHybridPLCInt16[:needed]
	for i := range res {
		int16Out[i] = fixedpoint.Res2Int16(res[i])
	}
	d.appendFixedOutput(int16Out, res)
	d.clearFixedQEXTHybrid()
	d.fixedHybridFrameActive = false
	return true
}

// decodeFixedQEXTHybridFEC runs the QEXT CELT PLC accumulation used while
// recovering a Hybrid LBRR frame. The SILK decoder has already written its
// lowband into pcm; converting that resampler output to opus_res before
// DecodeLostAccum preserves the FIXED_POINT accumulation order. celtFrameSize
// is measured at 48 kHz by the public decoder, so native 96 kHz CELT doubles
// that geometry before entering the QEXT decoder.
func (d *Decoder) decodeFixedQEXTHybridFEC(pcm []float32, frameSizeAPI, celtFrameSize int, celtBW celt.CELTBandwidth) bool {
	if !d.fixedPacketActive || d.fixedQEXT.invalid || frameSizeAPI <= 0 || celtFrameSize <= 0 {
		return false
	}
	if d.fixedQEXT.decoder == nil {
		decoder, err := fixedpoint.NewQEXTCELTDecoder(int(d.channels), int(d.sampleRate))
		if err != nil {
			return false
		}
		d.fixedQEXT.decoder = decoder
	}
	channels := int(d.channels)
	needed := frameSizeAPI * channels
	if len(pcm) < needed {
		return false
	}
	if d.haveDecoded && d.prevMode != ModeHybrid && !d.prevRedundancy {
		d.fixedQEXT.decoder.Reset()
	}
	if cap(d.fixedHybridPLCRes) < needed {
		d.fixedHybridPLCRes = make([]int32, needed)
	}
	res := d.fixedHybridPLCRes[:needed]
	for i, sample := range pcm[:needed] {
		res[i] = int32(int16(sample*32768)) << 8
	}
	d.fixedQEXT.decoder.SetPhaseInversionDisabled(d.celtDecoder.PhaseInversionDisabled())
	d.fixedQEXT.decoder.SetBandRange(celt.HybridCELTStartBand, celtBW.EffectiveBands())
	coreFrameSize := celtFrameSize
	if d.sampleRate == 96000 {
		coreFrameSize *= 2
	}
	downsample := 48000 / int(d.sampleRate)
	if downsample <= 0 {
		downsample = 1
	}
	concealedSamples := min(frameSizeAPI, coreFrameSize/downsample)
	if concealedSamples <= 0 || d.fixedQEXT.decoder.DecodeLostAccum(coreFrameSize, res[:concealedSamples*channels]) != concealedSamples {
		return false
	}
	if cap(d.fixedHybridPLCInt16) < needed {
		d.fixedHybridPLCInt16 = make([]int16, needed)
	}
	int16Out := d.fixedHybridPLCInt16[:needed]
	for i := range res {
		int16Out[i] = fixedpoint.Res2Int16(res[i])
	}
	d.appendFixedOutput(int16Out, res)
	return true
}

func (d *Decoder) fixedQEXTHybridFinalRange() uint32 {
	if d.fixedQEXT.hybridActive && d.fixedQEXT.decoder != nil {
		return d.fixedQEXT.decoder.FinalRange()
	}
	return d.mainDecodeRng
}

func (d *Decoder) fixedQEXTRedundantFinalRange() (uint32, bool) {
	return d.fixedQEXT.redundantRange, d.fixedQEXT.redundantRangeValid
}

func (d *Decoder) fixedSmoothFadeRes(in1, in2, out []int32, overlap, channels, sampleRate int) {
	fixedpoint.SmoothFadeResQEXT(in1, in2, out, overlap, channels, sampleRate)
}

func (d *Decoder) clearFixedQEXTHybrid() {
	d.fixedQEXT.hybridActive = false
	d.fixedQEXT.hybridPayload = nil
	d.fixedQEXT.hybridChannels = 0
	d.fixedQEXT.redundantRangeValid = false
}

// decodeFixedQEXTCELTFrame records output from the selected fixed-point
// ENABLE_QEXT CELT decoder. This received-frame path supports API rates from
// 8 kHz through 96 kHz when the packet does not cross a mode transition. Rates
// below 48 kHz use the 48 kHz core geometry with integer output downsampling.
// The float decoder still runs first to retain shared public state used by
// other codec paths; the fixed result replaces its public samples and range.
func (d *Decoder) decodeFixedQEXTCELTFrame(main *rangecoding.Decoder, dataLen, frameSize int, packetStereo bool, bandwidth celt.CELTBandwidth, qextPayload []byte) (bool, error) {
	if !d.fixedPacketActive {
		return false, nil
	}
	if frameSize <= 0 {
		d.invalidateFixedQEXTCELT()
		return false, nil
	}
	if d.fixedQEXT.invalid {
		return false, nil
	}
	if d.fixedQEXT.decoder == nil {
		decoder, err := fixedpoint.NewQEXTCELTDecoder(int(d.channels), int(d.sampleRate))
		if err != nil {
			return false, err
		}
		d.fixedQEXT.decoder = decoder
	}
	core := d.fixedQEXT.decoder
	core.SetPhaseInversionDisabled(d.celtDecoder.PhaseInversionDisabled())
	core.SetBandRange(0, bandwidth.EffectiveBands())

	downsample := 48000 / int(d.sampleRate)
	if downsample <= 0 {
		downsample = 1
	}
	coreFrameSize := frameSize * downsample
	needed := frameSize * int(d.channels)
	if cap(d.fixedQEXT.res) < needed {
		d.fixedQEXT.res = make([]int32, needed)
	}
	res := d.fixedQEXT.res[:needed]
	codedChannels := 1
	if packetStereo {
		codedChannels = 2
	}
	decoded := core.DecodeFrameWithEC(main, dataLen, coreFrameSize, codedChannels, qextPayload, res)
	if decoded < 0 {
		return false, ErrInvalidPacket
	}
	if decoded != frameSize {
		return false, ErrInvalidPacket
	}
	int16Out := d.fixedCELTScratch(needed)
	for i, sample := range res {
		int16Out[i] = fixedpoint.Res2Int16(sample)
	}
	d.appendFixedOutput(int16Out, res)
	d.mainDecodeRng = core.FinalRange()
	return true, nil
}

func (d *Decoder) decodeFixedQEXTCELTLostFrame(frameSize int) bool {
	if !d.fixedPacketActive || d.fixedQEXT.invalid || d.fixedQEXT.decoder == nil || frameSize <= 0 {
		return false
	}
	downsample := 48000 / int(d.sampleRate)
	if downsample <= 0 {
		downsample = 1
	}
	coreFrameSize := frameSize * downsample
	needed := frameSize * int(d.channels)
	if cap(d.fixedQEXT.res) < needed {
		d.fixedQEXT.res = make([]int32, needed)
	}
	res := d.fixedQEXT.res[:needed]
	if decoded := d.fixedQEXT.decoder.DecodeLost(coreFrameSize, res); decoded != frameSize {
		d.invalidateFixedQEXTCELT()
		return false
	}
	int16Out := d.fixedCELTScratch(needed)
	for i, sample := range res {
		int16Out[i] = fixedpoint.Res2Int16(sample)
	}
	d.appendFixedOutput(int16Out, res)
	d.mainDecodeRng = d.fixedQEXT.decoder.FinalRange()
	return true
}

// resetFixedQEXTCELT resets received-frame history while preserving the
// public phase-inversion control in the native decoder.
func (d *Decoder) resetFixedQEXTCELT() {
	if d.fixedQEXT.decoder != nil {
		d.fixedQEXT.decoder.Reset()
	}
	d.fixedQEXT.invalid = false
	d.clearFixedQEXTHybrid()
}

// invalidateFixedQEXTCELT prevents later frames from claiming exact output
// after a packet-loss or mode-transition frame that the native QEXT sidecar
// does not advance yet.
func (d *Decoder) invalidateFixedQEXTCELT() {
	d.fixedQEXT.invalid = true
}

func (d *Decoder) setFixedQEXTPhaseInversionDisabled(disabled bool) {
	if d.fixedQEXT.decoder != nil {
		d.fixedQEXT.decoder.SetPhaseInversionDisabled(disabled)
	}
}
