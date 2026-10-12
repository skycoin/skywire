// auto_mode.go implements the libopus auto-mode decision chain for the Opus encoder.
// This ports the mode, bandwidth, and stream-channels selection logic from
// opus_encoder.c lines 1273-1695 (libopus 1.6.1).
//
// Reference: tmp_check/opus-1.6.1/src/opus_encoder.c

package encoder

import (
	"math"

	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/types"
)

// StereoWidthMem holds the running cross-correlation state for the stateful
// compute_stereo_width() estimator. It mirrors libopus StereoWidthState
// (src/opus_encoder.c) and persists across frames so the stereo->mono and
// bandwidth decisions see a smoothed width.
type StereoWidthMem struct {
	// XX is the leaky-integrated left-channel auto-correlation energy (libopus
	// "XX").
	XX opusVal32
	// XY is the leaky-integrated left/right cross-correlation energy (libopus
	// "XY").
	XY opusVal32
	// YY is the leaky-integrated right-channel auto-correlation energy (libopus
	// "YY").
	YY opusVal32
	// SmoothedWidth is the time-smoothed stereo-width estimate in [0,1] (libopus
	// "smoothed_width").
	SmoothedWidth opusVal16
	// MaxFollower is the decaying peak-follower that limits how fast the width may
	// drop (libopus "max_follower").
	MaxFollower opusVal16
}

// Bandwidth threshold tables from libopus opus_encoder.c lines 151-174.
// Format: [NB↔MB threshold, hysteresis, MB↔WB threshold, hyst, WB↔SWB threshold, hyst, SWB↔FB threshold, hyst]
var monoVoiceBandwidthThresholds = [8]int{9000, 700, 9000, 700, 13500, 1000, 14000, 2000}
var monoMusicBandwidthThresholds = [8]int{9000, 700, 9000, 700, 11000, 1000, 12000, 2000}
var stereoVoiceBandwidthThresholds = [8]int{9000, 700, 9000, 700, 13500, 1000, 14000, 2000}
var stereoMusicBandwidthThresholds = [8]int{9000, 700, 9000, 700, 11000, 1000, 12000, 2000}

// Stereo/mono threshold bit-rates from libopus opus_encoder.c lines 176-177.
const stereoVoiceThreshold = 19000
const stereoMusicThreshold = 17000

// Mode thresholds for SILK/hybrid vs CELT-only from libopus opus_encoder.c lines 180-184.
// [0] = mono, [1] = stereo. Each: [voice, music].
var autoModeThresholds = [2][2]int{
	{64000, 10000}, // mono
	{44000, 10000}, // stereo
}

// silkFixConst001Q16 is SILK_FIX_CONST(0.01, 16), the Q16 factor decide_fec
// applies to its loss-scaled FEC threshold.
const silkFixConst001Q16 = 655

// FEC threshold table from libopus opus_encoder.c lines 186-192.
// Format: [threshold, hysteresis] for NB, MB, WB, SWB, FB.
var fecThresholdsTable = [10]int{
	12000, 1000, // NB
	14000, 1000, // MB
	16000, 1000, // WB
	20000, 1000, // SWB
	22000, 1000, // FB
}

// frameStereoWidth runs compute_stereo_width() on the raw caller frame for every
// stereo frame not forced to mono, whatever the mode and before the "too little
// space" exit, so width_mem advances exactly as in opus_encode_native()
// (src/opus_encoder.c:1321-1324).
func (e *Encoder) frameStereoWidth(pcm []opusRes, frameSize int) opusVal16 {
	if width, ok := e.fixedStereoWidthForMode(frameSize); ok {
		return width
	}
	if e.channels == 2 && e.forceChannels != 1 {
		return e.computeStereoWidthForMode(pcm, frameSize)
	}
	return 0
}

// computeStereoWidthForMode implements the normalized float stereo width and
// state history from libopus compute_stereo_width().
// Reference: src/opus_encoder.c:854-938.
func (e *Encoder) computeStereoWidthForMode(pcm []opusRes, frameSize int) opusVal16 {
	if e.channels != 2 || len(pcm) < frameSize*2 {
		return 0
	}

	frameRate := int(e.sampleRate) / frameSize
	shortAlpha := opusVal16(25.0 / opusVal16(max(frameRate, 50)))

	// Accumulate per-frame energy and cross-correlation (unrolled by 4).
	var xx, xy, yy opusVal32
	for i, j := 0, 0; i < frameSize-3; i, j = i+4, j+8 {
		var pxx, pxy, pyy opusVal32
		p := pcm[j : j+8 : j+8]
		x0, y0 := p[0], p[1]
		x1, y1 := p[2], p[3]
		x2, y2 := p[4], p[5]
		x3, y3 := p[6], p[7]
		if outerTargetV3FMA {
			// The pinned AMD64 v3 C object seeds each pair with sample 1, then
			// contracts sample 0 into that rounded product.
			pxx, pxy, pyy = x1*x1, x1*y1, y1*y1
			pxx += x0 * x0
			pxy += x0 * y0
			pyy += y0 * y0
		} else {
			pxx += x0 * x0
			pxy += x0 * y0
			pyy += y0 * y0
			pxx += x1 * x1
			pxy += x1 * y1
			pyy += y1 * y1
		}
		pxx += x2 * x2
		pxy += x2 * y2
		pyy += y2 * y2
		pxx += x3 * x3
		pxy += x3 * y3
		pyy += y3 * y3
		xx += pxx
		xy += pxy
		yy += pyy
	}

	// Safety check (float-point path, opus_encoder.c line 903-906).
	if !(xx < 1e9) || opusVal32IsNaN(xx) || !(yy < 1e9) || opusVal32IsNaN(yy) {
		xx = 0
		xy = 0
		yy = 0
	}

	mem := &e.widthMem
	// Only short_alpha uses the 50 Hz floor; width smoothing uses the actual
	// frame rate (opus_encoder.c:compute_stereo_width).
	// Exponential smoothing.
	mem.XX += shortAlpha * (xx - mem.XX)
	// The AMD64 v3 C object rounds alpha*xy, then fuses beta*oldXY with it.
	// stereoWidthXYUpdate selects that contraction only on the matching target.
	mem.XY = stereoWidthXYUpdate(1-shortAlpha, mem.XY, round32(shortAlpha*xy))
	mem.YY += shortAlpha * (yy - mem.YY)

	// Clamp to non-negative.
	if mem.XX < 0 {
		mem.XX = 0
	}
	if mem.XY < 0 {
		mem.XY = 0
	}
	if mem.YY < 0 {
		mem.YY = 0
	}

	maxEnergy := mem.XX
	if mem.YY > maxEnergy {
		maxEnergy = mem.YY
	}
	if maxEnergy > 8e-4 {
		sqrtXX := celtSqrtOpusVal32(mem.XX)
		sqrtYY := celtSqrtOpusVal32(mem.YY)
		qrrtXX := celtSqrtOpusVal32(sqrtXX) // fourth root
		qrrtYY := celtSqrtOpusVal32(sqrtYY)

		const epsilon opusVal32 = 1e-15
		sqrtProd := sqrtXX * sqrtYY
		// Clamp cross-correlation.
		if mem.XY > sqrtProd {
			mem.XY = sqrtProd
		}
		// Inter-channel correlation.
		corr := mem.XY / (epsilon + sqrtProd)
		// Approximate loudness difference.
		ldiff := absOpusVal16(qrrtXX-qrrtYY) / (epsilon + qrrtXX + qrrtYY)
		// Width = sqrt(1 - corr^2) * ldiff, clamped to [0, 1].
		decorr := stereoWidthDecorrelation(corr)
		if decorr < 0 {
			decorr = 0
		}
		// C stores width before subtracting the prior smoothed value.
		width := round32(minf(1.0, celtSqrtOpusVal32(decorr)) * ldiff)

		// Smoothing over one second.
		fr := opusVal16(frameRate)
		mem.SmoothedWidth += (width - mem.SmoothedWidth) / fr
		// Peak follower.
		followerDecay := mem.MaxFollower - 0.02/fr
		if followerDecay < mem.SmoothedWidth {
			followerDecay = mem.SmoothedWidth
		}
		mem.MaxFollower = followerDecay
	}

	// Return clamped to [0, 1], scaled by 20x peak follower.
	result := 20.0 * mem.MaxFollower
	if result > 1.0 {
		result = 1.0
	}
	if result < 0 {
		result = 0
	}
	return result
}

// celtSqrtOpusVal32 mirrors libopus celt_sqrt() in the float build:
// celt/mathops.h casts the C sqrt() result back to float.
func celtSqrtOpusVal32(x opusVal32) opusVal32 {
	if x <= 0 {
		return 0
	}
	return opusVal32(opusmath.SqrtF32(float32(x)))
}

func absOpusVal16(x opusVal16) opusVal16 {
	if x < 0 {
		return -x
	}
	return x
}

func opusVal32IsNaN(x opusVal32) bool {
	return math.Float32bits(float32(x))&0x7fffffff > 0x7f800000
}

// decideFEC implements libopus decide_fec() (opus_encoder.c lines 940-971).
// It decides whether to enable LBRR (FEC) and may reduce bandwidth to afford it.
// Returns the LBRR coded decision; bandwidth may be modified via pointer.
func decideFEC(useInBandFEC bool, packetLoss int32, lastFEC bool, mode Mode, bandwidth *types.Bandwidth, equivRate int32) bool {
	if !useInBandFEC || packetLoss == 0 || mode == ModeCELT {
		return false
	}
	origBandwidth := *bandwidth

	for {
		idx := int(*bandwidth - types.BandwidthNarrowband)
		if idx < 0 || idx*2+1 >= len(fecThresholdsTable) {
			break
		}
		lbrrRateThreshold := int32(fecThresholdsTable[2*idx])
		hysteresis := int32(fecThresholdsTable[2*idx+1])

		if lastFEC {
			lbrrRateThreshold -= hysteresis
		} else {
			lbrrRateThreshold += hysteresis
		}

		// silk_SMULWB(silk_MUL(threshold, 125-min(loss,25)), SILK_FIX_CONST(0.01, 16)):
		// the Q16 constant 0.01 is 655, so the scaled threshold rounds down from
		// threshold*(125-loss)*655/65536 (src/opus_encoder.c:954-955).
		loss := min(packetLoss, 25)
		lbrrRateThreshold = smulwb(lbrrRateThreshold*(125-loss), silkFixConst001Q16)

		if equivRate > lbrrRateThreshold {
			return true
		} else if packetLoss <= 5 {
			return false
		} else if *bandwidth > types.BandwidthNarrowband {
			(*bandwidth)--
		} else {
			break
		}
	}

	// Couldn't find any bandwidth to enable FEC; keep original.
	*bandwidth = origBandwidth
	return false
}

// autoVoiceRatioFromAnalysis computes voice_ratio from analysis info.
// Matches libopus opus_encoder.c lines 1273-1291.
func (e *Encoder) autoVoiceRatioFromAnalysis() {
	if e.signalType != types.SignalAuto {
		return
	}
	if !e.lastAnalysisValid {
		return
	}
	var prob float32
	switch e.prevMode {
	case ModeAuto:
		// First frame or unknown previous mode.
		prob = e.lastAnalysisInfo.MusicProb
	case ModeCELT:
		prob = e.lastAnalysisInfo.MusicProbMax
	default:
		prob = e.lastAnalysisInfo.MusicProbMin
	}
	e.voiceRatio = opusmath.FloorHalfPlusF32ToInt32(float32(100) * (float32(1) - prob))
}

// updateDetectedBandwidth computes detected_bandwidth from analysis info.
// Matches libopus opus_encoder.c lines 1294-1304.
func (e *Encoder) updateDetectedBandwidth() {
	e.detectedBandwidth = 0
	e.detectedBandwidthValid = false
	if !e.lastAnalysisValid {
		return
	}
	e.detectedBandwidthValid = true
	abw := e.lastAnalysisInfo.BandwidthIndex
	switch {
	case abw <= 12:
		e.detectedBandwidth = types.BandwidthNarrowband
	case abw <= 14:
		e.detectedBandwidth = types.BandwidthMediumband
	case abw <= 16:
		e.detectedBandwidth = types.BandwidthWideband
	case abw <= 18:
		e.detectedBandwidth = types.BandwidthSuperwideband
	default:
		e.detectedBandwidth = types.BandwidthFullband
	}
}

// autoVoiceEst computes voice_est from voice_ratio, matching libopus lines 1413-1426.
func (e *Encoder) autoVoiceEst() int32 {
	if e.signalType == types.SignalVoice {
		return 127
	}
	if e.signalType == types.SignalMusic {
		return 0
	}
	if e.voiceRatio >= 0 {
		voiceEst := (e.voiceRatio * 327) >> 8
		// OPUS_APPLICATION_AUDIO clamp.
		if !e.voipApp && voiceEst > 115 {
			voiceEst = 115
		}
		return voiceEst
	}
	if e.voipApp {
		return 115
	}
	return 48
}

// autoStreamChannelsDecision decides mono vs stereo encoding.
// Matches libopus opus_encoder.c lines 1428-1453.
func (e *Encoder) autoStreamChannelsDecision(voiceEst, equivRate int32) {
	if e.forceChannels > 0 && e.channels == 2 {
		e.streamChannels = e.forceChannels
		return
	}
	if e.channels == 2 {
		stereoThreshold := stereoMusicThreshold +
			(voiceEst*voiceEst*(stereoVoiceThreshold-stereoMusicThreshold))/16384
		if e.streamChannels == 2 {
			stereoThreshold -= 1000
		} else {
			stereoThreshold += 1000
		}
		if equivRate > stereoThreshold {
			e.streamChannels = 2
		} else {
			e.streamChannels = 1
		}
	} else {
		e.streamChannels = int32(e.channels)
	}
}

// applyStereoToMonoTransition delays a forced or automatic stereo-to-mono
// change for one frame while SILK is active, matching opus_encoder.c:1562-1570.
func (e *Encoder) applyStereoToMonoTransition(mode Mode) {
	if e.streamChannels == 1 && e.prevChannels == 2 && e.toMono == 0 &&
		mode != ModeCELT && e.prevMode != ModeCELT {
		e.toMono = 1
		e.streamChannels = 2
	} else {
		e.toMono = 0
	}
}

func (e *Encoder) updateStreamChannelsForFrame(frameSize int) {
	frameRate := int(e.sampleRate) / frameSize
	if frameRate <= 0 {
		frameRate = 50
	}
	useVBR := e.bitrateMode != ModeCBR
	equivRate := e.computeEquivRate(e.bitrate, int32(e.channels), int32(frameRate),
		useVBR, ModeAuto, e.complexity, e.packetLoss)
	e.autoStreamChannelsDecision(e.autoVoiceEst(), equivRate)
}

// autoModeDecision selects SILK-only vs CELT-only using interpolated thresholds.
// Matches libopus opus_encoder.c lines 1492-1527.
//
// silkUseDTX mirrors libopus st->silk_mode.useDTX (opus_encoder.c:1461):
// use_dtx && !(analysis_info.valid || is_silence). The DTX-favours-SILK guard
// below keys off this, NOT the raw use_dtx flag, so at a complexity where the
// tonality analysis is valid the guard does not fire and the mode stays whatever
// the rate/threshold picked (commonly CELT for music-like stereo content).
func (e *Encoder) autoModeDecision(stereoWidth opusVal16, voiceEst, equivRate int32, frameSize, maxDataBytes int, silkUseDTX bool) Mode {
	// Interpolate mode thresholds based on stereo width.
	modeVoiceF := (1.0-stereoWidth)*opusVal16(autoModeThresholds[0][0]) +
		stereoWidth*opusVal16(autoModeThresholds[1][0])
	modeVoice := int32(modeVoiceF + 0.5)
	// Note: libopus uses [1][1] for both terms (bug/intentional since values are equal).
	modeMusic := int32(autoModeThresholds[1][1])

	threshold := modeMusic + (voiceEst*voiceEst*(modeVoice-modeMusic))/16384
	if fixedThreshold, ok := e.fixedModeThreshold(voiceEst); ok {
		threshold = fixedThreshold
	}

	if e.voipApp {
		threshold += 8000
	}

	// Hysteresis based on previous mode.
	switch e.prevMode {
	case ModeCELT:
		threshold -= 4000
	case ModeSILK, ModeHybrid:
		threshold += 4000
	}

	mode := ModeSILK
	if equivRate >= threshold {
		mode = ModeCELT
	}

	// FEC guard: with in-band FEC and sufficient loss, use SILK.
	// When fec_config == 2, don't force SILK unless voice_est > 25 (music-safe mode).
	// Matches libopus opus_encoder.c line 1517.
	if e.fecEnabled && e.packetLoss > (128-voiceEst)>>4 &&
		(e.fecConfig != InBandFECMusicSafe || voiceEst > 25) {
		mode = ModeSILK
	}

	// DTX guard: voiced content with DTX uses SILK for its DTX feature, but only
	// when the generalized (CELT) DTX cannot be used — i.e. silk_mode.useDTX, which
	// is false once the tonality analysis is valid. Matches opus_encoder.c:1519-1522.
	if silkUseDTX && voiceEst > 100 {
		mode = ModeSILK
	}

	// Low-rate CELT fallback. libopus checks the packet byte budget, not the
	// configured target bitrate.
	if maxDataBytes < lowRateCELTByteThreshold(int(e.sampleRate), frameSize) {
		mode = ModeCELT
	}

	return mode
}

func lowRateCELTByteThreshold(sampleRate, frameSize int) int {
	frameRate := sampleRate / frameSize
	minRate := 6000
	if frameRate > 50 {
		minRate = 9000
	}
	return bitrateBitsForFrame(minRate, sampleRate, frameSize) / 8
}

func bitrateBitsForFrame(bitrate, sampleRate, frameSize int) int {
	if sampleRate <= 0 || frameSize <= 0 {
		return 0
	}
	unitsPerFrame := 6 * sampleRate / frameSize
	if unitsPerFrame <= 0 {
		return 0
	}
	return bitrate * 6 / unitsPerFrame
}

// autoSelectBandwidth implements the libopus auto-bandwidth selection loop.
// Matches opus_encoder.c lines 1583-1627.
func (e *Encoder) autoSelectBandwidth(voiceEst, equivRate int32) types.Bandwidth {
	var voiceThresholds, musicThresholds *[8]int
	if e.channels == 2 && e.forceChannels != 1 {
		voiceThresholds = &stereoVoiceBandwidthThresholds
		musicThresholds = &stereoMusicBandwidthThresholds
	} else {
		voiceThresholds = &monoVoiceBandwidthThresholds
		musicThresholds = &monoMusicBandwidthThresholds
	}

	// Interpolate bandwidth thresholds based on voice estimation.
	var bwThresholds [8]int
	for i := range 8 {
		bwThresholds[i] = musicThresholds[i] +
			int((voiceEst*voiceEst*int32(voiceThresholds[i]-musicThresholds[i]))/16384)
	}

	bandwidth := types.BandwidthFullband
	for bandwidth > types.BandwidthNarrowband {
		idx := int(bandwidth - types.BandwidthMediumband)
		threshold := bwThresholds[2*idx]
		hysteresis := bwThresholds[2*idx+1]

		if !e.first {
			if e.autoBandwidth >= bandwidth {
				threshold -= hysteresis
			} else {
				threshold += hysteresis
			}
		}

		if equivRate >= int32(threshold) {
			break
		}
		bandwidth--
	}

	// We don't use mediumband anymore, except during mode transitions.
	if bandwidth == types.BandwidthMediumband {
		bandwidth = types.BandwidthWideband
	}

	return bandwidth
}

// selectAutoBandwidth updates the selected bandwidth in the cases where
// libopus reruns its rate-dependent bandwidth selection. Keep autoBandwidth
// before max-bandwidth and user-bandwidth clamps so a later relaxed limit can
// restore the automatic choice.
func (e *Encoder) selectAutoBandwidth(mode Mode, voiceEst, equivRate int32) {
	if mode != ModeCELT && !e.first && !e.silkMode.AllowBandwidthSwitch {
		return
	}

	e.bandwidth = e.autoSelectBandwidth(voiceEst, equivRate)
	e.autoBandwidth = e.bandwidth
	// Prevent any transition to SWB/FB until SILK has switched to WB and turned
	// off the variable LP filter.
	if !e.first && mode != ModeCELT && !e.silkMode.InWBModeWithoutVariableLP &&
		e.bandwidth > types.BandwidthWideband {
		e.bandwidth = types.BandwidthWideband
	}
}

// autoClampBandwidth applies bandwidth clamping rules.
// Matches libopus opus_encoder.c lines 1629-1684.
func (e *Encoder) autoClampBandwidth(bandwidth types.Bandwidth, mode Mode, equivRate int32, maxRate int) types.Bandwidth {
	// Max bandwidth limit.
	if bandwidth > e.maxBandwidth {
		bandwidth = e.maxBandwidth
	}

	// User-forced bandwidth overrides auto selection.
	if e.userBandwidthSet {
		bandwidth = e.userBandwidth
	}

	// Prevent hybrid at unsafe CBR/max rates (line 1636-1639).
	if mode != ModeCELT {
		if maxRate < 15000 {
			if bandwidth > types.BandwidthWideband {
				bandwidth = types.BandwidthWideband
			}
		}
	}

	// Nyquist rate clamping (lines 1643-1650).
	if e.sampleRate <= 24000 && bandwidth > types.BandwidthSuperwideband {
		bandwidth = types.BandwidthSuperwideband
	}
	if e.sampleRate <= 16000 && bandwidth > types.BandwidthWideband {
		bandwidth = types.BandwidthWideband
	}
	if e.sampleRate <= 12000 && bandwidth > types.BandwidthMediumband {
		bandwidth = types.BandwidthMediumband
	}
	if e.sampleRate <= 8000 && bandwidth > types.BandwidthNarrowband {
		bandwidth = types.BandwidthNarrowband
	}

	// Use detected bandwidth to reduce encoded bandwidth (lines 1653-1673).
	if e.detectedBandwidthValid && !e.userBandwidthSet {
		var minDetected types.Bandwidth
		switch {
		case equivRate <= 18000*e.streamChannels && mode == ModeCELT:
			minDetected = types.BandwidthNarrowband
		case equivRate <= 24000*e.streamChannels && mode == ModeCELT:
			minDetected = types.BandwidthMediumband
		case equivRate <= 30000*e.streamChannels:
			minDetected = types.BandwidthWideband
		case equivRate <= 44000*e.streamChannels:
			minDetected = types.BandwidthSuperwideband
		default:
			minDetected = types.BandwidthFullband
		}
		detected := max(e.detectedBandwidth, minDetected)
		if bandwidth > detected {
			bandwidth = detected
		}
	}

	// CELT doesn't support mediumband; use wideband instead (line 1681-1682).
	if mode == ModeCELT && bandwidth == types.BandwidthMediumband {
		bandwidth = types.BandwidthWideband
	}

	// LFE forces narrowband (line 1683-1684).
	if e.lfe {
		bandwidth = types.BandwidthNarrowband
	}

	return bandwidth
}

// autoModeFixup adjusts mode based on final bandwidth decision.
// Matches libopus opus_encoder.c lines 1692-1695.
func autoModeFixup(mode Mode, bandwidth types.Bandwidth) Mode {
	if mode == ModeSILK && bandwidth > types.BandwidthWideband {
		return ModeHybrid
	}
	if mode == ModeHybrid && bandwidth <= types.BandwidthWideband {
		return ModeSILK
	}
	return mode
}

// autoModeAndBandwidthDecision implements the full libopus auto-mode decision chain.
// Called from Encode() for automatic mode or a low-delay application, which
// fixes CELT mode while retaining automatic channel and bandwidth decisions.
// Updates e.bandwidth, e.streamChannels, e.voiceRatio, e.detectedBandwidth,
// e.autoBandwidth, e.first.
// Returns the selected mode.
func (e *Encoder) autoModeAndBandwidthDecision(stereoWidth opusVal16, frameSize, maxDataBytes int, isSilence bool) (mode, prevModeNext Mode) {
	frameRate := int(e.sampleRate) / frameSize
	if frameRate <= 0 {
		frameRate = 50
	}
	useVBR := e.bitrateMode != ModeCBR
	maxRate := e.maxRateForFrame(frameSize, maxDataBytes)

	// Step 1: Reset voice_ratio for non-silent frames (line 1275-1276).
	if !isSilence {
		e.voiceRatio = -1
	}

	// Step 2: Compute voice_ratio from analysis (lines 1279-1291).
	e.autoVoiceRatioFromAnalysis()

	// Step 3: stereoWidth is the frame's compute_stereo_width() result
	// (line 1322), measured by frameStereoWidth before the low-space exit.
	// Detected bandwidth is refreshed at native entry for both automatic and
	// user-forced modes.

	// Step 5: First-pass equiv_rate with e.channels (line 1410-1411).
	equivRate := e.computeEquivRate(e.bitrate, int32(e.channels), int32(frameRate), useVBR,
		ModeAuto, e.complexity, e.packetLoss)

	// Step 6: Compute voice_est (lines 1413-1426).
	voiceEst := e.autoVoiceEst()

	// Step 7: Stream channels decision (lines 1428-1453).
	e.autoStreamChannelsDecision(voiceEst, equivRate)

	// Step 8: Recompute equiv_rate with stream_channels (lines 1454-1456).
	equivRate = e.computeEquivRate(e.bitrate, e.streamChannels, int32(frameRate), useVBR,
		ModeAuto, e.complexity, e.packetLoss)

	// Step 9: Application override or interpolated mode thresholds (lines 1466-1527).
	if e.lowDelay {
		// opus_encoder.c:1467-1473 pins restricted low-delay/CELT to CELT
		// before the channel-dependent bandwidth decision at lines 1583-1627.
		mode = ModeCELT
	} else {
		// silk_mode.useDTX (opus_encoder.c:1461) favours SILK only when the
		// generalized DTX is unusable: DTX on with invalid/silent analysis.
		mode = e.autoModeDecision(stereoWidth, voiceEst, equivRate, frameSize, maxDataBytes, e.silkMode.UseDTX)
	}

	// Step 10: Frame size constraint (lines 1533-1537).
	if mode != ModeCELT && frameSize < int(e.sampleRate)/100 {
		mode = ModeCELT
	}
	if e.lfe {
		mode = ModeCELT
	}
	// A switch into CELT-only keeps the previous mode for this frame and
	// codes the redundant CELT frame (lines 1541-1557), before the
	// bandwidth decision and the mode fixup see the mode.
	mode, prevModeNext = e.applyCELTTransitionDelay(frameSize, mode)

	// Step 11: Stereo-to-mono transition delay (lines 1562-1570).
	e.applyStereoToMonoTransition(mode)

	// Step 12: Recompute equiv_rate with mode decision (lines 1572-1574).
	equivRate = e.computeEquivRate(e.bitrate, e.streamChannels, int32(frameRate), useVBR,
		mode, e.complexity, e.packetLoss)

	// Step 13: Auto bandwidth selection (lines 1583-1627). It runs for CELT-only
	// frames, on the first frame, and when the last SILK packet reported that
	// low speech activity allows a bandwidth switch.
	e.selectAutoBandwidth(mode, voiceEst, equivRate)

	// Step 14: Bandwidth clamping (lines 1629-1684).
	e.bandwidth = e.autoClampBandwidth(e.bandwidth, mode, equivRate, maxRate)

	// Step 15: decide_fec (line 1675-1676).
	bw := e.bandwidth
	e.lbrrCoded = decideFEC(e.fecEnabled, e.packetLoss, e.lbrrCoded, mode, &bw, equivRate)
	e.bandwidth = bw

	// Step 16: Mode fixup based on final bandwidth (lines 1692-1695).
	mode = autoModeFixup(mode, e.bandwidth)
	if prevModeNext != ModeCELT {
		prevModeNext = mode
	}

	// prev_channels and st->first advance at the end of the frame
	// (opus_encode_frame_native), so the low-space and SILK DTX early returns
	// leave them unchanged.
	return mode, prevModeNext
}
