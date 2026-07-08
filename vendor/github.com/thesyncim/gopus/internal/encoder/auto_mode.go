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

// FEC threshold table from libopus opus_encoder.c lines 186-192.
// Format: [threshold, hysteresis] for NB, MB, WB, SWB, FB.
var fecThresholdsTable = [10]int{
	12000, 1000, // NB
	14000, 1000, // MB
	16000, 1000, // WB
	20000, 1000, // SWB
	22000, 1000, // FB
}

// computeStereoWidthForMode implements libopus compute_stereo_width() (float-point path).
// It updates e.widthMem and returns stereo width in [0, 1] range (Q15 scale as float).
// Reference: opus_encoder.c lines 854-938.
func (e *Encoder) computeStereoWidthForMode(pcm []opusRes, frameSize int) opusVal16 {
	if e.channels != 2 || len(pcm) < frameSize*2 {
		return 0
	}

	frameRate := max(int(e.sampleRate)/frameSize, 50)
	shortAlpha := opusVal16(25.0 / opusVal16(frameRate))

	// Accumulate per-frame energy and cross-correlation (unrolled by 4).
	var xx, xy, yy opusVal32
	for i, j := 0, 0; i < frameSize-3; i, j = i+4, j+8 {
		var pxx, pxy, pyy opusVal32
		x0, y0 := pcm[j], pcm[j+1]
		x1, y1 := pcm[j+2], pcm[j+3]
		x2, y2 := pcm[j+4], pcm[j+5]
		x3, y3 := pcm[j+6], pcm[j+7]
		pxx += x0 * x0
		pxy += x0 * y0
		pyy += y0 * y0
		pxx += x1 * x1
		pxy += x1 * y1
		pyy += y1 * y1
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
	// Exponential smoothing.
	mem.XX += shortAlpha * (xx - mem.XX)
	// Rewritten to avoid overflow on abrupt sign change (opus_encoder.c line 911).
	mem.XY = (1-shortAlpha)*mem.XY + shortAlpha*xy
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
		decorr := 1.0 - corr*corr
		if decorr < 0 {
			decorr = 0
		}
		width := minf(1.0, celtSqrtOpusVal32(decorr)) * ldiff

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

		// silk_SMULWB(silk_MUL(threshold, 125-min(loss,25)), SILK_FIX_CONST(0.01, 16))
		// = threshold * (125 - min(loss, 25)) * 0.01 / (essentially integer multiply then shift)
		loss := min(packetLoss, 25)
		// In float: threshold * (125 - loss) / 100
		lbrrRateThreshold = lbrrRateThreshold * (125 - loss) / 100

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
	if e.prevMode == ModeAuto || e.prevMode == 0 {
		// First frame or unknown previous mode.
		prob = e.lastAnalysisInfo.MusicProb
	} else if e.prevMode == ModeCELT {
		prob = e.lastAnalysisInfo.MusicProbMax
	} else {
		prob = e.lastAnalysisInfo.MusicProbMin
	}
	e.voiceRatio = opusmath.FloorHalfPlusF32ToInt32(float32(100) * (float32(1) - prob))
}

// updateDetectedBandwidth computes detected_bandwidth from analysis info.
// Matches libopus opus_encoder.c lines 1294-1304.
func (e *Encoder) updateDetectedBandwidth() {
	e.detectedBandwidth = 0
	if !e.lastAnalysisValid {
		return
	}
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

	if e.voipApp {
		threshold += 8000
	}

	// Hysteresis based on previous mode.
	if e.prevMode == ModeCELT {
		threshold -= 4000
	} else if e.prevMode == ModeSILK || e.prevMode == ModeHybrid {
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
	if e.detectedBandwidth > 0 && !e.userBandwidthSet {
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
// Called from Encode() when e.mode == ModeAuto.
// Updates e.bandwidth, e.streamChannels, e.voiceRatio, e.detectedBandwidth,
// e.autoBandwidth, e.first.
// Returns the selected mode.
func (e *Encoder) autoModeAndBandwidthDecision(pcm []opusRes, frameSize, maxDataBytes int, isSilence bool) Mode {
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

	// Step 3: Compute detected bandwidth from analysis (lines 1294-1304).
	e.updateDetectedBandwidth()

	// Step 4: Compute stereo width (line 1322).
	var stereoWidth opusVal16
	if e.channels == 2 && e.forceChannels != 1 {
		stereoWidth = e.computeStereoWidthForMode(pcm, frameSize)
	}

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

	// Step 9: Mode selection with interpolated thresholds (lines 1492-1527).
	// silk_mode.useDTX (opus_encoder.c:1461): DTX favours SILK only when the
	// generalized DTX is unusable, i.e. DTX on AND the analysis is invalid/silent.
	silkUseDTX := e.dtxEnabled && !(e.lastAnalysisValid || isSilence)
	mode := e.autoModeDecision(stereoWidth, voiceEst, equivRate, frameSize, maxDataBytes, silkUseDTX)

	// Step 10: Frame size constraint (lines 1533-1537).
	if mode != ModeCELT && frameSize < int(e.sampleRate)/100 {
		mode = ModeCELT
	}
	if e.lfe {
		mode = ModeCELT
	}

	// Step 11: Stereo→mono transition delay (lines 1562-1570).
	// When switching from stereo to mono, delay by two frames for smooth SILK downmix.
	// toMono is set to 1 on the first frame, then cleared on the next.
	if e.streamChannels == 1 && e.prevChannels == 2 && e.toMono == 0 &&
		mode != ModeCELT && e.prevMode != ModeCELT {
		e.toMono = 1
		e.streamChannels = 2
	} else {
		e.toMono = 0
	}

	// Step 12: Recompute equiv_rate with mode decision (lines 1572-1574).
	equivRate = e.computeEquivRate(e.bitrate, e.streamChannels, int32(frameRate), useVBR,
		mode, e.complexity, e.packetLoss)

	// Step 13: Auto bandwidth selection (lines 1583-1627).
	// Run when CELT-only, first frame, or SILK allows bandwidth switch.
	allowBWSwitch := e.silkAllowBandwidthSwitch()
	if mode == ModeCELT || e.first || allowBWSwitch {
		bw := e.autoSelectBandwidth(voiceEst, equivRate)
		e.bandwidth = bw
		e.autoBandwidth = bw
	}

	// Prevent SWB/FB until SILK LP filter is inactive (lines 1625-1626).
	if !e.first && mode != ModeCELT && !e.silkInWBModeWithoutVariableLP() &&
		e.bandwidth > types.BandwidthWideband {
		e.bandwidth = types.BandwidthWideband
	}

	// Step 14: Bandwidth clamping (lines 1629-1684).
	e.bandwidth = e.autoClampBandwidth(e.bandwidth, mode, equivRate, maxRate)

	// Step 15: decide_fec (line 1675-1676).
	bw := e.bandwidth
	e.lbrrCoded = decideFEC(e.fecEnabled, e.packetLoss, e.lbrrCoded, mode, &bw, equivRate)
	e.bandwidth = bw

	// Step 16: Mode fixup based on final bandwidth (lines 1692-1695).
	mode = autoModeFixup(mode, e.bandwidth)

	// Track previous channels for stereo→mono transition. st->first is cleared
	// later, at the common commit point in encodeOpusResWithAnalysisMaxBytes
	// (libopus opus_encode_native line 2562), so the low-space / SILK nBytes==0
	// early returns correctly leave it set.
	e.prevChannels = e.streamChannels

	return mode
}

// silkAllowBandwidthSwitch checks if the SILK encoder allows bandwidth switching.
// In libopus, this is set when SILK's internal sample rate is below the API rate.
// Matches libopus silk_encode_frame_FLP.c allowBandwidthSwitch output.
func (e *Encoder) silkAllowBandwidthSwitch() bool {
	if e.silkEncoder == nil {
		return false
	}
	return e.silkEncoder.AllowBandwidthSwitch()
}

// silkInWBModeWithoutVariableLP checks if SILK is in WB mode with LP filter inactive.
// Matches libopus: silk_mode.inWBmodeWithoutVariableLP = (fs_kHz == 16 && sLP.mode == 0).
func (e *Encoder) silkInWBModeWithoutVariableLP() bool {
	if e.silkEncoder == nil {
		return true // Conservative: don't restrict bandwidth if SILK not initialized.
	}
	return e.silkEncoder.InWBModeWithoutVariableLP()
}
