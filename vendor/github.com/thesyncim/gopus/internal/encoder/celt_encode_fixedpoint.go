//go:build gopus_fixed_point

package encoder

import (
	"math"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/fixedpoint"
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/types"
)

// fixedPointBuild reports whether the gopus_fixed_point integer codec paths are
// compiled in. Golden-fixture tests calibrated against the float SILK encode
// path gate on it (the FIXED_POINT SILK encode produces byte-exact-to-libopus
// payloads that differ from the float golden bytes).
const fixedPointBuild = true

// encoderFixedCELTFields carries the integer (FIXED_POINT) CELT encoder state
// added under the gopus_fixed_point build. It is empty in the default build.
type encoderFixedCELTFields struct {
	fixedCELT        *fixedCELTState
	fixedCELTOut     []byte
	fixedFinalRange  uint32
	fixedCELTUsed    bool
	fixedRawRes      []int32
	fixedEnergyMask  []int32
	fixedMaskActive  bool
	fixedMaskQ24     bool
	fixedMaskPending bool
	fixedFiltered    []int32
	fixedFrameSource []int32
	fixedDelayed     []int32
	fixedDelayBuffer []int32
	fixedHPMem       [4]int32
	fixedInputActive bool
	fixedFrameReady  bool
	fixedFrameCursor int
	fixedWidthMem    fixedStereoWidthMem
	fixedWidthQ15    int16
}

// fixedCELTFinalRange returns the integer CELT encoder's final range coder state
// when the last frame was produced by the integer path. currentFinalRange uses
// it so the encoder's reported final range matches the integer packet.
func (e *Encoder) fixedCELTFinalRange() (uint32, bool) {
	if e.fixedCELTUsed {
		return e.fixedFinalRange, true
	}
	return 0, false
}

func (e *Encoder) fixedQEXTPayloadIfUsed() ([]byte, bool) {
	if !e.fixedCELTUsed || e.fixedCELT == nil {
		return nil, false
	}
	return e.fixedCELT.enc.LastQEXTPayload(), true
}

// clearFixedCELTUsed resets the integer-CELT-used flag at the start of each
// packet so a stale value from a previous CELT frame cannot mis-gate the TOC
// frame-size conversion for a subsequent SILK/Hybrid frame.
func (e *Encoder) clearFixedCELTUsed() { e.fixedCELTUsed = false }

// fixedCELTState holds the integer (FIXED_POINT) CELT encoder used under the
// gopus_fixed_point build to produce byte-exact CELT-mode packets. It is created
// lazily and carries all CELT cross-frame state, so once a CELT-mode packet is
// routed to it every subsequent CELT frame must continue through it.
type fixedCELTState struct {
	enc          *fixedpoint.CELTEncoder
	channels     int
	modeFs       int32
	pcm16        []int16
	rng          *rangecoding.Encoder
	lastQ8       []int32
	lastAnalysis AnalysisInfo
	lastMaxBytes int32
	lastBitrate  int32
	lastLSBDepth int32
}

// celtFixedUpsample mirrors celt_encoder_init's st->upsample =
// resampling_factor(API sample rate): 1 at 48/96 kHz and 2/3/4/6 at 24/16/12/8 kHz.
// 0 means an unsupported API rate.
func (e *Encoder) celtFixedUpsample() int {
	switch e.sampleRate {
	case 96000, 48000:
		return 1
	case 24000:
		return 2
	case 16000:
		return 3
	case 12000:
		return 4
	case 8000:
		return 6
	}
	return 0
}

// activeFixedCELTEndBand follows the CELT control set after SILK reports its
// internal sample rate. During a SILK bandwidth switch, this can differ from
// the requested bandwidth (src/opus_encoder.c, opus_encode_frame_native).
func (e *Encoder) activeFixedCELTEndBand() int {
	return e.celtEncoder.Bandwidth().EffectiveBands()
}

// celtFixedFrameSizeInScope reports whether the integer CELT encoder supports
// the frame's static 48 kHz mode and API-rate upsampling layout.
func (e *Encoder) celtFixedFrameSizeInScope(frameSize int) bool {
	upsample := e.celtFixedUpsample()
	if upsample == 0 {
		return false
	}
	// The API-rate frameSize must upsample to a valid core block
	// (shortMdctSize<<LM). Native 96 kHz CELT uses shortMdctSize=240 and
	// supports blocks through 1920 samples; standard modes use 120 and 960.
	shortMdctSize, maxCore := 120, 960
	if e.sampleRate == 96000 {
		shortMdctSize, maxCore = 240, 1920
	}
	core := frameSize * upsample
	if core <= 0 || core > maxCore || core%shortMdctSize != 0 {
		return false
	}
	c := int(e.channels)
	if c != 1 && c != 2 {
		return false
	}
	if !e.fixedCELTEnergyMaskInScope() {
		return false
	}
	return true
}

// fixedCELTEnergyMaskInScope keeps non-finite and out-of-range float API masks
// on the float path. The fixed CELT control accepts a Q24 celt_glog (int32).
func (e *Encoder) fixedCELTEnergyMaskInScope() bool {
	if !e.fixedMaskActive {
		return true
	}
	if e.fixedMaskQ24 {
		return len(e.fixedEnergyMask) == int(e.channels)*celt.MaxBands
	}
	for _, mask := range e.celtEnergyMask {
		if _, ok := fixedCELTEnergyMaskValue(mask); !ok {
			return false
		}
	}
	return true
}

// fixedCELTEnergyMaskValue applies the fixed build's GCONST2 rounding to one
// float32 public mask value with fixed-width arithmetic. The input is a
// public-boundary float; the result is the Q24 celt_glog control value.
func fixedCELTEnergyMaskValue(value float32) (int32, bool) {
	valueBits := math.Float32bits(value)
	if valueBits&0x7f800000 == 0x7f800000 {
		return 0, false
	}
	scaled := value * float32(1<<24)
	if scaled < -1<<31 || scaled >= 1<<31 {
		return 0, false
	}
	whole := int32(scaled)
	fraction := scaled - float32(whole)
	if fraction >= 0.5 || (whole < 0 && fraction > -0.5) {
		whole++
	}
	return whole, true
}

// setFixedCELTEnergyMask converts the public float mask at the codec boundary
// to the fixed build's Q24 celt_glog. The conversion mirrors GCONST2 in
// celt/fixed_generic.h; the backing slice belongs to the encoder and remains
// reusable across frames.
func (e *Encoder) setFixedCELTEnergyMask(enc *fixedpoint.CELTEncoder) {
	if !e.fixedMaskActive {
		enc.SetEnergyMask(nil)
		return
	}
	if e.fixedMaskQ24 {
		enc.SetEnergyMask(e.fixedEnergyMask)
		return
	}
	if len(e.celtEnergyMask) == 0 {
		enc.SetEnergyMask(nil)
		return
	}
	if cap(e.fixedEnergyMask) < len(e.celtEnergyMask) {
		e.fixedEnergyMask = make([]int32, len(e.celtEnergyMask))
	} else {
		e.fixedEnergyMask = e.fixedEnergyMask[:len(e.celtEnergyMask)]
	}
	for i, mask := range e.celtEnergyMask {
		v, ok := fixedCELTEnergyMaskValue(mask)
		if !ok {
			panic("fixed CELT energy mask escaped scope check")
		}
		e.fixedEnergyMask[i] = v
	}
	enc.SetEnergyMask(e.fixedEnergyMask)
}

// syncFixedCELTEnergyMask mirrors OPUS_SET_ENERGY_MASK: the control updates
// the live CELT state, while a value set before lazy CELT creation is consumed
// once when that state is created. CELT reset clears the inner pointer.
func (e *Encoder) syncFixedCELTEnergyMask() {
	e.fixedMaskQ24 = false
	e.fixedMaskActive = len(e.celtEnergyMask) > 0
	if e.fixedCELT == nil {
		e.fixedMaskPending = e.fixedMaskActive
		return
	}
	if e.fixedCELTEnergyMaskInScope() {
		e.setFixedCELTEnergyMask(e.fixedCELT.enc)
	} else {
		e.fixedCELT.enc.SetEnergyMask(nil)
	}
	e.fixedMaskPending = false
}

// SetCELTEnergyMaskQ24 applies one per-band mask already expressed in the
// fixed build's Q24 celt_glog format. The input is copied into reusable encoder
// storage before it reaches the CELT control.
func (e *Encoder) SetCELTEnergyMaskQ24(mask []int32) {
	if len(mask) == 0 {
		e.SetCELTEnergyMask(nil)
		return
	}
	needed := int(e.channels) * celt.MaxBands
	if len(mask) < needed {
		panic("fixed CELT energy mask is shorter than channels*21")
	}
	if cap(e.fixedEnergyMask) < needed {
		e.fixedEnergyMask = make([]int32, needed)
	} else {
		e.fixedEnergyMask = e.fixedEnergyMask[:needed]
	}
	copy(e.fixedEnergyMask, mask[:needed])
	e.fixedMaskActive = true
	e.fixedMaskQ24 = true
	if cap(e.celtEnergyMask) < needed {
		e.celtEnergyMask = make([]float32, needed)
	} else {
		e.celtEnergyMask = e.celtEnergyMask[:needed]
	}
	for i, value := range e.fixedEnergyMask {
		e.celtEnergyMask[i] = float32(value) * (1.0 / float32(1<<24))
	}
	e.syncCELTEnergyMask()
	if e.fixedCELT == nil {
		e.fixedMaskPending = true
		return
	}
	e.fixedCELT.enc.SetEnergyMask(e.fixedEnergyMask)
	e.fixedMaskPending = false
}

func (e *Encoder) setFixedCELTLFE(enabled bool) {
	if e.fixedCELT != nil {
		e.fixedCELT.enc.SetLFE(enabled)
	}
}

// fixedSilkSurroundRateOffset ports the fixed-point mask arithmetic from
// src/opus_encoder.c:2069-2106. The float mask is only a public-boundary view;
// native Q24 controls remain authoritative when supplied by multistream.
func (e *Encoder) fixedSilkSurroundRateOffset(silkBitRate int32) (int32, bool) {
	if !e.fixedMaskActive {
		return 0, false
	}
	end := 17
	srate := int32(16000)
	switch e.bandwidth {
	case types.BandwidthNarrowband:
		end = 13
		srate = 8000
	case types.BandwidthMediumband:
		end = 15
		srate = 12000
	}
	maskSum := int32(0)
	for channel := range int(e.channels) {
		for band := range end {
			index := celt.MaxBands*channel + band
			var mask int32
			if e.fixedMaskQ24 {
				if index >= len(e.fixedEnergyMask) {
					return 0, false
				}
				mask = e.fixedEnergyMask[index]
			} else {
				if index >= len(e.celtEnergyMask) {
					return 0, false
				}
				var ok bool
				mask, ok = fixedCELTEnergyMaskValue(e.celtEnergyMask[index])
				if !ok {
					return 0, false
				}
			}
			if mask > 1<<23 {
				mask = 1 << 23
			}
			if mask < -(2 << 24) {
				mask = -(2 << 24)
			}
			if mask > 0 {
				mask >>= 1
			}
			maskSum += mask
		}
	}
	// GCONST(.2f) in fixed_generic.h is 3355443 in Q24.
	maskingDepth := (maskSum / int32(end)) * int32(e.channels)
	maskingDepth += 3355443
	shiftedDepth := int32(int16(maskingDepth >> (24 - 10)))
	product := int32(int16(srate)) * shiftedDepth
	rateOffset := (product + (1 << 9)) >> 10 // PSHR32(product, 10)
	rateOffset = max(rateOffset, -2*silkBitRate/3)
	if e.bandwidth == types.BandwidthSuperwideband || e.bandwidth == types.BandwidthFullband {
		rateOffset = 3 * rateOffset / 5
	}
	return rateOffset, true
}

// celtFixedEncodeInScope reports whether the selected pure-CELT frame is in the
// integer encoder's scope. The caller has already resolved requested mode,
// LFE, and frame-size fallbacks to an actual CELT frame.
func (e *Encoder) celtFixedEncodeInScope(frameSize int) bool {
	return e.celtFixedFrameSizeInScope(frameSize)
}

// celtFixedHybridEncodeInScope reports whether the integer CELT encoder can
// continue a shared range coder for one Hybrid frame.
func (e *Encoder) celtFixedHybridEncodeInScope(frameSize int) bool {
	if e.restrictedSilkApp {
		return false
	}
	return e.celtFixedFrameSizeInScope(frameSize)
}

// encodeCELTFrameFixed runs the integer CELT encoder for one in-scope frame and
// returns the CELT payload bytes (TOC-excluded), matching the float
// celt.Encoder.EncodeFrame contract. ok is false when the frame is out of the
// integer encoder's scope, in which case the caller must use the float path.
func (e *Encoder) encodeCELTFrameFixed(pcm []opusRes, frameSize, bitrate, maxPayloadBytes int, prefilled bool) (out []byte, ok bool, err error) {
	e.fixedCELTUsed = false
	if !e.celtFixedEncodeInScope(frameSize) {
		return nil, false, nil
	}
	channels := int(e.channels)
	if len(pcm) != frameSize*channels {
		return nil, false, ErrInvalidFrameSize
	}

	st := e.ensureFixedCELT(channels)
	// QEXT is enabled for the pure CELT frame itself. Prefill, Hybrid and
	// redundancy calls set it off at their own fixed-CELT entry points.
	st.enc.SetQEXTEnabled(extsupport.QEXT && e.qextActive())
	st.enc.SetLFE(e.lfe)
	st.enc.SetBandRange(0, e.activeFixedCELTEndBand())
	st.enc.SetStreamChannels(int32(e.celtEncoder.StreamChannels()))
	st.enc.SetComplexity(int(e.complexity))
	st.enc.SetBitrate(bitrate)
	st.enc.SetLSBDepth(int(e.lsbDepth))
	st.enc.SetPrediction(int32(e.celtEncoder.Prediction()))
	st.enc.SetSilkInfo(0, 0)
	if e.lastAnalysisValid && !prefilled {
		st.lastAnalysis = e.lastAnalysisInfo
		st.enc.SetAnalysisInfo(fixedpoint.CELTAnalysisInfo{
			Valid:         true,
			Bandwidth:     e.lastAnalysisInfo.BandwidthIndex,
			LeakBoost:     e.lastAnalysisInfo.LeakBoost,
			Activity:      e.lastAnalysisInfo.Activity,
			Tonality:      e.lastAnalysisInfo.Tonality,
			TonalitySlope: e.lastAnalysisInfo.TonalitySlope,
			MaxPitchRatio: e.lastAnalysisInfo.MaxPitchRatio,
		})
	} else {
		st.lastAnalysis = AnalysisInfo{}
		st.enc.SetAnalysisInfo(fixedpoint.CELTAnalysisInfo{})
	}
	// Mirror the live float CELT controls after configureCELTRate. libopus
	// updates constrained_vbr only in its VBR branch, so CBR preserves the
	// constraint left by initialization or the previous VBR frame.
	st.enc.SetVBR(e.celtEncoder.VBR())
	st.enc.SetConstrainedVBR(e.celtEncoder.ConstrainedVBR())

	// All supported public input APIs carry opus_res Q8 through DC rejection and
	// the delay buffer. The int16 seam serves callers outside that source path.
	var pcmRes []int32
	if e.fixedFrameReady && len(e.fixedDelayed) == len(pcm) {
		pcmRes = e.fixedDelayed
		st.lastQ8 = pcmRes
		st.pcm16 = st.pcm16[:0]
	} else {
		st.lastQ8 = nil
		if cap(st.pcm16) < len(pcm) {
			st.pcm16 = make([]int16, len(pcm))
		}
		st.pcm16 = st.pcm16[:len(pcm)]
		for i, v := range pcm {
			st.pcm16[i] = opusmath.Float32ToInt16(float32(v))
		}
	}

	// The fixed CELT main payload is capped at 1275 bytes. QEXT also reserves an
	// extension payload, so its frame budget follows the caller's packet capacity
	// instead of truncating the combined CELT result at the 1275-byte main-payload limit.
	nbCompressedBytes := celtPacketSizeCap
	if extsupport.QEXT && e.qextActive() && maxPayloadBytes > nbCompressedBytes {
		nbCompressedBytes = maxPayloadBytes
	}
	if maxPayloadBytes > 0 && maxPayloadBytes < nbCompressedBytes {
		nbCompressedBytes = maxPayloadBytes
	}
	st.lastMaxBytes = int32(nbCompressedBytes)
	st.lastBitrate = int32(bitrate)
	st.lastLSBDepth = e.lsbDepth

	if cap(st.rng.Buffer()) < nbCompressedBytes {
		buf := make([]byte, nbCompressedBytes)
		st.rng.Init(buf)
	} else {
		buf := st.rng.Buffer()[:nbCompressedBytes]
		for i := range buf {
			buf[i] = 0
		}
		st.rng.Init(buf)
	}

	var n int
	if pcmRes != nil {
		n = st.enc.EncodeWithECRes(pcmRes, frameSize, st.rng, nbCompressedBytes)
	} else {
		n = st.enc.EncodeWithEC(st.pcm16, frameSize, st.rng, nbCompressedBytes)
	}
	e.fixedFinalRange = st.enc.FinalRange()
	out = append(e.fixedCELTOut[:0], st.rng.Buffer()[:n]...)
	e.fixedCELTOut = out
	e.fixedCELTUsed = true
	return out, true, nil
}

// encodeHybridCELTFrameFixed codes the Hybrid CELT bands into the range coder
// already seeded by the SILK and redundancy-signalling layers. pcmQ8 is the
// ENABLE_RES24 opus_res frame after top-level high-pass, delay, high-band and
// stereo-width processing. The caller owns that frame and the shared coder.
func (e *Encoder) encodeHybridCELTFrameFixed(pcmQ8 []int32, frameSize, bitrate, maxPayloadBytes int, re *rangecoding.Encoder, prefilled bool) (out []byte, ok bool, err error) {
	e.fixedCELTUsed = false
	if !e.celtFixedHybridEncodeInScope(frameSize) {
		return nil, false, nil
	}
	if re == nil || len(pcmQ8) != frameSize*int(e.channels) {
		return nil, false, ErrInvalidFrameSize
	}

	channels := int(e.channels)
	st := e.ensureFixedCELT(channels)
	st.enc.SetQEXTEnabled(false)
	st.enc.SetLFE(e.lfe)
	st.enc.SetBandRange(17, e.activeFixedCELTEndBand())
	st.enc.SetStreamChannels(int32(e.celtEncoder.StreamChannels()))
	st.enc.SetComplexity(int(e.complexity))
	st.enc.SetBitrate(bitrate)
	st.enc.SetLSBDepth(int(e.lsbDepth))
	st.enc.SetPrediction(int32(e.celtEncoder.Prediction()))
	if !prefilled {
		st.enc.SetSilkInfo(e.silkMode.SignalType, e.silkMode.Offset)
	}
	if e.lastAnalysisValid && !prefilled {
		st.lastAnalysis = e.lastAnalysisInfo
		st.enc.SetAnalysisInfo(fixedpoint.CELTAnalysisInfo{
			Valid:         true,
			Bandwidth:     e.lastAnalysisInfo.BandwidthIndex,
			LeakBoost:     e.lastAnalysisInfo.LeakBoost,
			Activity:      e.lastAnalysisInfo.Activity,
			Tonality:      e.lastAnalysisInfo.Tonality,
			TonalitySlope: e.lastAnalysisInfo.TonalitySlope,
			MaxPitchRatio: e.lastAnalysisInfo.MaxPitchRatio,
		})
	} else {
		st.lastAnalysis = AnalysisInfo{}
		st.enc.SetAnalysisInfo(fixedpoint.CELTAnalysisInfo{})
	}
	st.enc.SetVBR(e.celtEncoder.VBR())
	// libopus sets constrained VBR only for CELT-only frames. Hybrid VBR uses
	// the rate left after SILK without constraining CELT to it.
	st.enc.SetConstrainedVBR(false)

	st.lastQ8 = pcmQ8
	st.pcm16 = st.pcm16[:0]
	st.lastMaxBytes = int32(maxPayloadBytes)
	st.lastBitrate = int32(bitrate)
	st.lastLSBDepth = e.lsbDepth

	n := st.enc.EncodeWithECRes(pcmQ8, frameSize, re, maxPayloadBytes)
	e.fixedFinalRange = re.Range()
	e.fixedCELTUsed = true
	return re.Buffer()[:n], true, nil
}

// encodeRedundantCELTFrameFixed codes one selected fixed-point 5 ms redundancy
// frame. It continues the fixed CELT history while using the independent range
// coder that carries the redundant packet section.
func (e *Encoder) encodeRedundantCELTFrameFixed(pcmQ8 []int32, frameSize, bitrate, maxPayloadBytes int, hybrid, analysis bool) (out []byte, finalRange uint32, ok bool, err error) {
	if !e.celtFixedFrameSizeInScope(frameSize) || len(pcmQ8) != frameSize*int(e.channels) || maxPayloadBytes <= 0 {
		return nil, 0, false, nil
	}
	st := e.ensureFixedCELT(int(e.channels))
	st.enc.SetQEXTEnabled(false)
	st.enc.SetLFE(e.lfe)
	st.enc.SetBandRange(0, e.activeFixedCELTEndBand())
	st.enc.SetStreamChannels(int32(e.celtEncoder.StreamChannels()))
	st.enc.SetComplexity(int(e.complexity))
	st.enc.SetBitrate(bitrate)
	st.enc.SetLSBDepth(int(e.lsbDepth))
	st.enc.SetPrediction(int32(e.celtEncoder.Prediction()))
	if hybrid {
		st.enc.SetSilkInfo(e.silkMode.SignalType, e.silkMode.Offset)
	}
	if analysis && e.lastAnalysisValid {
		st.lastAnalysis = e.lastAnalysisInfo
		st.enc.SetAnalysisInfo(fixedpoint.CELTAnalysisInfo{
			Valid:         true,
			Bandwidth:     e.lastAnalysisInfo.BandwidthIndex,
			LeakBoost:     e.lastAnalysisInfo.LeakBoost,
			Activity:      e.lastAnalysisInfo.Activity,
			Tonality:      e.lastAnalysisInfo.Tonality,
			TonalitySlope: e.lastAnalysisInfo.TonalitySlope,
			MaxPitchRatio: e.lastAnalysisInfo.MaxPitchRatio,
		})
	} else {
		st.lastAnalysis = AnalysisInfo{}
		st.enc.SetAnalysisInfo(fixedpoint.CELTAnalysisInfo{})
	}
	st.enc.SetVBR(false)
	st.enc.SetConstrainedVBR(false)
	st.lastQ8 = pcmQ8
	st.lastMaxBytes = int32(maxPayloadBytes)
	st.lastBitrate = int32(bitrate)
	st.lastLSBDepth = e.lsbDepth

	if cap(st.rng.Buffer()) < maxPayloadBytes {
		st.rng.Init(make([]byte, maxPayloadBytes))
	} else {
		buf := st.rng.Buffer()[:maxPayloadBytes]
		clear(buf)
		st.rng.Init(buf)
	}
	n := st.enc.EncodeWithECRes(pcmQ8, frameSize, st.rng, maxPayloadBytes)
	return st.rng.Buffer()[:n], st.rng.Range(), true, nil
}

// prefillCELTFrameFixed mirrors the reset and 2-byte CELT tmp_prefill in
// opus_encode_frame_native. pcmQ8 is the exact ENABLE_RES24 prefill window from
// the fixed-point delay buffer. The current CELT controls survive Reset, while
// SILKInfo and AnalysisInfo clear with the CELT reset region.
func (e *Encoder) prefillCELTFrameFixed(pcmQ8 []int32, frameSize, startBand, bitrate, maxPayloadBytes int, prediction int32) bool {
	if !e.celtFixedFrameSizeInScope(frameSize) || len(pcmQ8) != frameSize*int(e.channels) || maxPayloadBytes < 2 {
		return false
	}
	st := e.ensureFixedCELT(int(e.channels))
	st.enc.SetQEXTEnabled(false)
	st.enc.SetLFE(e.lfe)
	st.enc.SetBandRange(startBand, e.activeFixedCELTEndBand())
	st.enc.SetStreamChannels(int32(e.celtEncoder.StreamChannels()))
	st.enc.SetComplexity(int(e.complexity))
	st.enc.SetBitrate(bitrate)
	st.enc.SetLSBDepth(int(e.lsbDepth))
	st.enc.SetPrediction(prediction)
	st.enc.SetVBR(e.celtEncoder.VBR())
	st.enc.SetConstrainedVBR(startBand == 0 && e.bitrateMode == ModeCVBR)
	st.enc.Reset()

	if cap(st.rng.Buffer()) < maxPayloadBytes {
		st.rng.Init(make([]byte, maxPayloadBytes))
	} else {
		buf := st.rng.Buffer()[:maxPayloadBytes]
		clear(buf)
		st.rng.Init(buf)
	}
	st.enc.EncodeWithECRes(pcmQ8, frameSize, st.rng, maxPayloadBytes)
	st.enc.SetPrediction(0)
	return true
}

// LastFixedCELTInputQ8 returns the exact opus_res frame consumed by integer
// CELT. The view is valid until the next encode call.
func (e *Encoder) LastFixedCELTInputQ8() []int32 {
	if !e.fixedCELTUsed || e.fixedCELT == nil {
		return nil
	}
	return e.fixedCELT.lastQ8
}

// LastFixedCELTAnalysis returns the analysis snapshot consumed by the last
// integer CELT frame. The value contains no borrowed buffers.
func (e *Encoder) LastFixedCELTAnalysis() AnalysisInfo {
	if !e.fixedCELTUsed || e.fixedCELT == nil {
		return AnalysisInfo{}
	}
	return e.fixedCELT.lastAnalysis
}

// LastFixedCELTControls reports the rate and caller capacity that the last
// integer CELT frame received, including the short API's effective LSB depth.
func (e *Encoder) LastFixedCELTControls() (bitrate, maxBytes, lsbDepth int) {
	if !e.fixedCELTUsed || e.fixedCELT == nil {
		return 0, 0, 0
	}
	return int(e.fixedCELT.lastBitrate), int(e.fixedCELT.lastMaxBytes), int(e.fixedCELT.lastLSBDepth)
}

func (e *Encoder) ensureFixedCELT(channels int) *fixedCELTState {
	return e.ensureFixedCELTRate(channels, int(e.sampleRate))
}

func (e *Encoder) ensureFixedCELTRate(channels, modeFs int) *fixedCELTState {
	if e.fixedCELT == nil || e.fixedCELT.channels != channels || int(e.fixedCELT.modeFs) != modeFs {
		e.fixedCELT = &fixedCELTState{
			enc:      fixedpoint.NewCELTEncoderRate(channels, modeFs),
			channels: channels,
			modeFs:   int32(modeFs),
			rng:      &rangecoding.Encoder{},
		}
		e.fixedMaskPending = e.fixedMaskActive
	}
	if e.fixedMaskPending {
		e.setFixedCELTEnergyMask(e.fixedCELT.enc)
		e.fixedMaskPending = false
	}
	e.fixedCELT.enc.SetPacketLoss(int(e.packetLoss))
	return e.fixedCELT
}

// resetFixedCELT clears the integer CELT cross-frame state, mirroring the float
// celtEncoder.Reset() done on a CELT mode transition. The selected mode rate is
// preserved when the fixed state is recreated.
func (e *Encoder) resetFixedCELT() {
	e.fixedMaskPending = false
	e.fixedMaskActive = false
	e.fixedMaskQ24 = false
	e.fixedEnergyMask = e.fixedEnergyMask[:0]
	e.fixedHPMem = [4]int32{}
	e.fixedWidthMem = fixedStereoWidthMem{}
	e.fixedWidthQ15 = 0
	clear(e.fixedDelayBuffer)
	e.fixedInputActive = false
	e.fixedFrameReady = false
	e.fixedFrameCursor = 0
	if e.fixedCELT != nil {
		e.fixedCELT.enc.Reset()
	}
}

func (e *Encoder) resetFixedCELTState() {
	if e.fixedCELT != nil {
		e.fixedCELT.enc.Reset()
		e.fixedCELT.lastAnalysis = AnalysisInfo{}
	}
}

const celtPacketSizeCap = 1275
