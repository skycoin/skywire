//go:build gopus_fixed_point

package encoder

import "github.com/thesyncim/gopus/internal/fixedpoint"

// fixedStereoWidthMem mirrors StereoWidthState in src/opus_encoder.c for the
// FIXED_POINT build. Energies and correlations are opus_val32; widths are Q15.
type fixedStereoWidthMem struct {
	XX, XY, YY                 int32
	SmoothedWidth, MaxFollower int16
}

func fixedWidthResToVal16(x int32) int16 {
	x = (x + 128) >> 8 // RES2VAL16: SAT16(PSHR32(opus_res, RES_SHIFT))
	if x > 32767 {
		return 32767
	}
	if x < -32768 {
		return -32768
	}
	return int16(x)
}

func fixedWidthMul16x32Q15(a int16, b int32) int32 {
	return int32((int64(a) * int64(b)) >> 15)
}

// fixedStereoWidthForMode runs compute_stereo_width from src/opus_encoder.c
// over the exact opus_res Q8 input. The fixed encoder cannot use the float
// estimator because its Q15 smoothing changes the mode-switch threshold.
func (e *Encoder) fixedStereoWidthForMode(frameSize int) (opusVal16, bool) {
	if !e.fixedInputActive || len(e.fixedRawRes) < frameSize*int(e.channels) {
		return 0, false
	}
	if e.channels != 2 || e.forceChannels == 1 {
		e.fixedWidthQ15 = 0
		return 0, true
	}
	pcm := e.fixedRawRes
	frameRate := int(e.sampleRate) / frameSize
	shortAlpha := int16(25 * 32767 / max(50, frameRate))
	shift := int(fixedpoint.CeltILog2(int32(frameSize))) - 2
	var xx, xy, yy int32
	for i := 0; i < frameSize-3; i += 4 {
		var pxx, pxy, pyy int32
		for j := 0; j < 4; j++ {
			x := fixedWidthResToVal16(pcm[2*(i+j)])
			y := fixedWidthResToVal16(pcm[2*(i+j)+1])
			pxx += (int32(x) * int32(x)) >> 2
			pxy += (int32(x) * int32(y)) >> 2
			pyy += (int32(y) * int32(y)) >> 2
		}
		xx += pxx >> shift
		xy += pxy >> shift
		yy += pyy >> shift
	}
	m := &e.fixedWidthMem
	m.XX += fixedWidthMul16x32Q15(shortAlpha, xx-m.XX)
	m.XY = fixedWidthMul16x32Q15(32767-shortAlpha, m.XY) + fixedWidthMul16x32Q15(shortAlpha, xy)
	m.YY += fixedWidthMul16x32Q15(shortAlpha, yy-m.YY)
	m.XX = max(0, m.XX)
	m.XY = max(0, m.XY)
	m.YY = max(0, m.YY)
	if max(m.XX, m.YY) > 210 { // QCONST16(8e-4f, 18)
		sqrtXX := int16(fixedpoint.CeltSqrt(m.XX))
		sqrtYY := int16(fixedpoint.CeltSqrt(m.YY))
		qrrtXX := int16(fixedpoint.CeltSqrt(int32(sqrtXX)))
		qrrtYY := int16(fixedpoint.CeltSqrt(int32(sqrtYY)))
		m.XY = min(m.XY, int32(sqrtXX)*int32(sqrtYY))
		corr := int16(fixedpoint.FracDiv32(m.XY, 1+int32(sqrtXX)*int32(sqrtYY)) >> 16)
		quarterRootDiff := int32(qrrtXX) - int32(qrrtYY)
		if quarterRootDiff < 0 {
			quarterRootDiff = -quarterRootDiff
		}
		ldiff := int16(32767 * quarterRootDiff / (1 + int32(qrrtXX) + int32(qrrtYY)))
		width := int16((int32(min(32767, fixedpoint.CeltSqrt((1<<30)-int32(corr)*int32(corr)))) * int32(ldiff)) >> 15)
		m.SmoothedWidth += int16((int32(width) - int32(m.SmoothedWidth)) / int32(frameRate))
		m.MaxFollower = max(m.MaxFollower-int16(655/frameRate), m.SmoothedWidth) // QCONST16(.02f, 15)
	}
	e.fixedWidthQ15 = int16(min(32767, 20*int32(m.MaxFollower)))
	return opusVal16(e.fixedWidthQ15) * (1.0 / 32768.0), true
}

// fixedModeThreshold uses the FIXED_POINT MULT16_32_Q15 rounding in
// src/opus_encoder.c:1496-1500. The shared caller then applies hysteresis.
func (e *Encoder) fixedModeThreshold(voiceEst int32) (int32, bool) {
	if !e.fixedInputActive {
		return 0, false
	}
	width := e.fixedWidthQ15
	modeVoice := fixedWidthMul16x32Q15(32767-width, 64000) + fixedWidthMul16x32Q15(width, 44000)
	modeMusic := fixedWidthMul16x32Q15(32767-width, 10000) + fixedWidthMul16x32Q15(width, 10000)
	return modeMusic + (voiceEst * voiceEst * (modeVoice - modeMusic) >> 14), true
}
