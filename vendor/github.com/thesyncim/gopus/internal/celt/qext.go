package celt

// qextPacketSizeCap matches libopus QEXT_PACKET_SIZE_CAP.
const qextPacketSizeCap = 3825

// nbQEXTBands matches libopus NB_QEXT_BANDS.
const nbQEXTBands = 14

// qextEBands180 and qextEBands240 match libopus celt/modes.c.
// They describe the extra-band edges used by the optional QEXT path.
var qextEBands180 = [15]int{
	74, 82, 90, 98, 106, 114, 122, 130, 138, 146, 154, 162, 168, 174, 180,
}

var qextLogN180Full = [15]int{
	24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 21, 21, 21,
}

var qextEBands240 = [15]int{
	100, 110, 120, 130, 140, 150, 160, 170, 180, 190, 200, 210, 220, 230, 240,
}

var qextLogN240 = [14]int{
	27, 27, 27, 27, 27, 27, 27, 27, 27, 27, 27, 27, 27, 27,
}

// qextCache* tables match libopus celt/static_modes_float.h qext_cache_*50.
var qextCacheIndex50 = [70]int16{
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 41,
	41, 41, 41, 41, 41, 41, 41, 41, 41, 41, 41, 41, 41, 61, 61,
	61, 61, 61, 61, 61, 61, 61, 61, 61, 61, 61, 61, 72, 72, 72,
	72, 72, 72, 72, 72, 72, 72, 72, 72, 72, 72, 80, 80, 80, 80,
	80, 80, 80, 80, 80, 80, 80, 80, 80, 80,
}

var qextCacheBits50 = [86]uint8{
	40, 26, 45, 59, 70, 79, 87, 94, 100, 105, 110, 114, 118, 122, 125,
	128, 131, 136, 141, 146, 150, 153, 157, 160, 163, 168, 173, 178, 182, 185,
	189, 192, 195, 200, 205, 210, 214, 217, 221, 224, 227, 19, 34, 61, 83,
	101, 118, 132, 145, 157, 167, 177, 186, 194, 202, 209, 216, 222, 234, 245,
	254, 10, 42, 77, 107, 133, 157, 179, 200, 219, 236, 253, 7, 50, 93,
	131, 165, 197, 227, 255, 5, 58, 109, 155, 197, 237,
}

var qextCacheCaps50 = [112]uint8{
	159, 159, 159, 159, 159, 159, 159, 159, 159, 159, 159, 159, 159, 159, 171,
	171, 171, 171, 171, 171, 171, 171, 171, 171, 171, 171, 171, 171, 163, 163,
	163, 163, 163, 163, 163, 163, 163, 163, 163, 163, 163, 163, 167, 167, 167,
	167, 167, 167, 167, 167, 167, 167, 167, 167, 167, 167, 163, 163, 163, 163,
	163, 163, 163, 163, 163, 163, 163, 163, 163, 163, 166, 166, 166, 166, 166,
	166, 166, 166, 166, 166, 166, 166, 166, 166, 163, 163, 163, 163, 163, 163,
	163, 163, 163, 163, 163, 163, 163, 163, 165, 165, 165, 165, 165, 165, 165,
	165, 165, 165, 165, 165, 165, 165,
}

type qextModeConfig struct {
	ShortMDCTSize int
	EBands        []int
	LogN          []int
	EffBands      int
	CacheIndex    []int16
	CacheBits     []uint8
	CacheCaps     []uint8
}

// computeQEXTModeConfig mirrors libopus compute_qext_mode() mode selection.
// It does not enable QEXT by itself; it only prepares the mode/tables that the
// future encoder/decoder wiring will need.
func computeQEXTModeConfig(sampleRate, shortMDCTSize int) (qextModeConfig, bool) {
	cfg := qextModeConfig{
		ShortMDCTSize: shortMDCTSize,
		CacheIndex:    qextCacheIndex50[:],
		CacheBits:     qextCacheBits50[:],
		CacheCaps:     qextCacheCaps50[:],
	}

	switch {
	case shortMDCTSize*48000 == 120*sampleRate:
		cfg.EBands = qextEBands240[:]
		cfg.LogN = qextLogN240[:]
	case shortMDCTSize*48000 == 90*sampleRate:
		// libopus ships one trailing qext_logN_180 value that sits past the
		// active NB_QEXT_BANDS window. We keep the exact source table above and
		// expose the active prefix here.
		cfg.EBands = qextEBands180[:]
		cfg.LogN = qextLogN180Full[:nbQEXTBands]
	default:
		return qextModeConfig{}, false
	}

	cfg.EffBands = nbQEXTBands
	for cfg.EffBands > 0 && cfg.EBands[cfg.EffBands] > shortMDCTSize {
		cfg.EffBands--
	}
	return cfg, true
}

func qextShortMDCTSize(frameSize int) int {
	mode := GetModeConfig(frameSize)
	if mode.ShortBlocks <= 0 {
		return frameSize
	}
	return frameSize / mode.ShortBlocks
}

// computeQEXTReservation mirrors the packet-space reservation part of the
// libopus ENABLE_QEXT path closely enough for the CELT-only internal encode
// flow. It returns the shrunken main payload size, the ext payload bytes
// available to the secondary range coder (excluding the extension ID byte),
// and the packet-level padding-byte count that would be required once the
// top-level Opus packet builder starts carrying the extension.
//
// cbrVBRTargetBytes is the VBR-equivalent main-coder target bytes for CBR
// mode (celt_encoder.c: the target produced by calling compute_vbr() with
// tf_estimate2=min(1,2*tf) plus ec_tell_frac). Pass 0 for VBR/CVBR mode,
// where nbCompressedBytes is already the VBR target. Reference:
// celt/celt_encoder.c lines 2539-2558.
func computeQEXTReservation(nbCompressedBytes, minAllowed, frameSize, channels, fs int, toneishness float32, cbrVBRTargetBytes int) (mainBytes, payloadBytes, paddingBytes int) {
	if nbCompressedBytes <= 0 {
		return nbCompressedBytes, 0, 0
	}
	if fs <= 0 {
		fs = 48000
	}

	offsetBytes := (channels * 80000 * frameSize) / (fs * 8)
	qextBytes := max(nbCompressedBytes-1275, max(0, (nbCompressedBytes-offsetBytes)*4/5))
	if qextBytes <= 20 {
		return nbCompressedBytes, 0, 0
	}
	// Match the libopus QEXT reservation path:
	//   For VBR: target = nbCompressedBytes - qextBytes/3 (the preliminary budget).
	//   For CBR: target = cbrVBRTargetBytes (from compute_vbr + ec_tell_frac).
	//   qext_bytes += scale * (nbCompressedBytes - target - qext_bytes)
	// Reference: celt/celt_encoder.c lines 2539-2558.
	scale := float32(1.0) - toneishness*toneishness
	if scale < 0 {
		scale = 0
	}
	if scale > 1 {
		scale = 1
	}
	targetBytes := nbCompressedBytes - qextBytes/3
	if cbrVBRTargetBytes > 0 {
		targetBytes = cbrVBRTargetBytes
	}
	qextBytes += roundFloat32ToInt(scale * float32((nbCompressedBytes-targetBytes)-qextBytes))
	qextBytes = max(nbCompressedBytes-1275, max(21, qextBytes))

	paddingBytes = (qextBytes + 253) / 254
	qextBytes = min(qextBytes, nbCompressedBytes-minAllowed-paddingBytes-1)
	paddingBytes = (qextBytes + 253) / 254
	if qextBytes <= 20 {
		return nbCompressedBytes, 0, 0
	}

	mainBytes = nbCompressedBytes - qextBytes - paddingBytes - 1
	if mainBytes < minAllowed {
		return nbCompressedBytes, 0, 0
	}

	payloadBytes = qextBytes - 1
	if payloadBytes <= 0 {
		return nbCompressedBytes, 0, 0
	}
	return mainBytes, payloadBytes, paddingBytes
}

func roundFloat32ToInt(x float32) int {
	if x >= 0 {
		return int(x + 0.5)
	}
	return int(x - 0.5)
}
