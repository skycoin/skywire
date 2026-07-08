// Package celt implements the CELT decoder per RFC 6716 Section 4.3.
// CELT (Constrained Energy Lapped Transform) is the transform-based layer
// of Opus for music and general audio.
package celt

// MaxBands is the maximum number of frequency bands in CELT.
// These are Bark-scale bands covering 0-20kHz at 48kHz sample rate.
const MaxBands = 21

// Overlap is the number of overlap samples at 48kHz (2.5ms window overlap).
// This is fixed for all CELT frame sizes.
const Overlap = 120

// DecodeBufferSize matches libopus DEC_PITCH_BUF_SIZE (decode_mem length without overlap).
// This buffer is larger than any single frame to preserve history for short blocks.
const DecodeBufferSize = 2048

// DB6 is the value corresponding to a 6 dB step in CELT's log2 energy units.
// In libopus, energies are stored in log2 units, so 6 dB = 1.0.
const DB6 = 1.0

// BitRes is the Q3 fixed-point resolution used throughout CELT allocation math.
// This mirrors libopus BITRES.
const BitRes = bitRes

// PreemphCoef is the de-emphasis filter coefficient.
// The encoder applies pre-emphasis; decoder applies inverse de-emphasis:
// y[n] = x[n] + PreemphCoef * y[n-1]
// Value matches libopus static_modes_float.h: 0.8500061035f
const PreemphCoef = 0.8500061035

// SilkCELTDelay is the delay compensation in samples at 48kHz for hybrid mode.
// SILK needs to be delayed relative to CELT for proper time alignment.
const SilkCELTDelay = 60

// EBands contains the MDCT bin indices for band edges at 48kHz with 5ms base frame.
// These 22 values define 21 bands. Each band spans from EBands[i] to EBands[i+1].
// For other frame sizes, these indices are scaled appropriately.
//
// Frequency boundaries (approximate):
// 0Hz, 200Hz, 400Hz, 600Hz, 800Hz, 1000Hz, 1200Hz, 1400Hz, 1600Hz, 2000Hz,
// 2400Hz, 2800Hz, 3200Hz, 4000Hz, 4800Hz, 5600Hz, 6800Hz, 8000Hz, 9600Hz,
// 12000Hz, 15600Hz, 20000Hz
//
// Source: libopus celt/modes.c (eBand5ms table)
var EBands = [22]int{
	0, 1, 2, 3, 4, 5, 6, 7, 8, 10,
	12, 14, 16, 20, 24, 28, 34, 40, 48, 60,
	78, 100,
}

var eBandWidths = [MaxBands]int{
	1, 1, 1, 1, 1, 1, 1, 1, 2, 2, 2,
	2, 4, 4, 4, 6, 6, 8, 12, 18, 22,
}

// AlphaCoef contains inter-frame energy prediction coefficients by LM (log mode).
// Used for coarse energy decoding in inter-frame mode.
// Index corresponds to LM: 0=2.5ms, 1=5ms, 2=10ms, 3=20ms
//
// Source: RFC 6716 Section 4.3.2, libopus celt/quant_bands.c
var AlphaCoef = [4]opusVal16{
	29440.0 / 32768.0, // LM=0 (2.5ms): 0.8984375
	26112.0 / 32768.0, // LM=1 (5ms):   0.796875
	21248.0 / 32768.0, // LM=2 (10ms):  0.6484375
	16384.0 / 32768.0, // LM=3 (20ms):  0.5
}

// BetaCoefInter contains inter-band energy prediction coefficients for INTER-frame mode.
// Values vary by LM (log mode / frame size). Source: libopus celt/quant_bands.c
var BetaCoefInter = [4]opusVal16{
	30147.0 / 32768.0, // LM=0 (2.5ms): 0.9200744...
	22282.0 / 32768.0, // LM=1 (5ms):   0.6800537...
	12124.0 / 32768.0, // LM=2 (10ms):  0.3700561...
	6554.0 / 32768.0,  // LM=3 (20ms):  0.2000122...
}

// BetaIntra is the inter-band prediction coefficient for INTRA-frame mode.
// No inter-frame prediction, only inter-band. Source: libopus celt/quant_bands.c
const BetaIntra opusVal16 = 4915.0 / 32768.0 // 0.15

// eMeans contains the mean log-energy per band (log2 units).
// These values are in log2 units (1.0 = 6 dB) and are added during
// denormalization to reconstruct the absolute band energy.
// Source: libopus celt/quant_bands.c (float eMeans table).
var eMeans = [25]celtGLog{
	6.437500, 6.250000, 5.750000, 5.312500, 5.062500,
	4.812500, 4.500000, 4.375000, 4.875000, 4.687500,
	4.562500, 4.437500, 4.875000, 4.625000, 4.312500,
	4.500000, 4.375000, 4.625000, 4.750000, 4.437500,
	3.750000, 3.750000, 3.750000, 3.750000, 3.750000,
}

// eProbModel contains the coarse energy probability model by LM and intra/inter.
// Values are in Q8: pairs of (probability of zero, decay rate).
// Source: libopus celt/quant_bands.c e_prob_model table.
var eProbModel = [4][2][42]uint8{
	// 120 sample frames (LM=0)
	{
		// Inter
		{
			72, 127, 65, 129, 66, 128, 65, 128, 64, 128, 62, 128, 64, 128,
			64, 128, 92, 78, 92, 79, 92, 78, 90, 79, 116, 41, 115, 40,
			114, 40, 132, 26, 132, 26, 145, 17, 161, 12, 176, 10, 177, 11,
		},
		// Intra
		{
			24, 179, 48, 138, 54, 135, 54, 132, 53, 134, 56, 133, 55, 132,
			55, 132, 61, 114, 70, 96, 74, 88, 75, 88, 87, 74, 89, 66,
			91, 67, 100, 59, 108, 50, 120, 40, 122, 37, 97, 43, 78, 50,
		},
	},
	// 240 sample frames (LM=1)
	{
		// Inter
		{
			83, 78, 84, 81, 88, 75, 86, 74, 87, 71, 90, 73, 93, 74,
			93, 74, 109, 40, 114, 36, 117, 34, 117, 34, 143, 17, 145, 18,
			146, 19, 162, 12, 165, 10, 178, 7, 189, 6, 190, 8, 177, 9,
		},
		// Intra
		{
			23, 178, 54, 115, 63, 102, 66, 98, 69, 99, 74, 89, 71, 91,
			73, 91, 78, 89, 86, 80, 92, 66, 93, 64, 102, 59, 103, 60,
			104, 60, 117, 52, 123, 44, 138, 35, 133, 31, 97, 38, 77, 45,
		},
	},
	// 480 sample frames (LM=2)
	{
		// Inter
		{
			61, 90, 93, 60, 105, 42, 107, 41, 110, 45, 116, 38, 113, 38,
			112, 38, 124, 26, 132, 27, 136, 19, 140, 20, 155, 14, 159, 16,
			158, 18, 170, 13, 177, 10, 187, 8, 192, 6, 175, 9, 159, 10,
		},
		// Intra
		{
			21, 178, 59, 110, 71, 86, 75, 85, 84, 83, 91, 66, 88, 73,
			87, 72, 92, 75, 98, 72, 105, 58, 107, 54, 115, 52, 114, 55,
			112, 56, 129, 51, 132, 40, 150, 33, 140, 29, 98, 35, 77, 42,
		},
	},
	// 960 sample frames (LM=3)
	{
		// Inter
		{
			42, 121, 96, 66, 108, 43, 111, 40, 117, 44, 123, 32, 120, 36,
			119, 33, 127, 33, 134, 34, 139, 21, 147, 23, 152, 20, 158, 25,
			154, 26, 166, 21, 173, 16, 184, 13, 184, 10, 150, 13, 139, 15,
		},
		// Intra
		{
			22, 178, 63, 114, 74, 82, 84, 83, 92, 82, 103, 62, 96, 72,
			96, 67, 101, 73, 107, 72, 113, 55, 118, 52, 125, 52, 118, 52,
			117, 55, 135, 49, 137, 39, 157, 32, 145, 29, 97, 33, 77, 40,
		},
	},
}

// smallEnergyICDF is used for coarse energy fallback when budget is low.
// Source: libopus celt/quant_bands.c small_energy_icdf table.
var smallEnergyICDF = []uint8{2, 1, 0}

const (
	bitRes               = 3
	allocSteps           = 6
	maxFineBits          = 8
	fineOffset           = 21
	qthetaOffset         = 4
	qthetaOffsetTwoPhase = 16
)

// log2FracTable contains log2(k) values in Q3 for k=0..23.
// Source: libopus celt/rate.c LOG2_FRAC_TABLE.
var log2FracTable = [24]uint8{
	0, 8, 13, 16, 19, 21, 23, 24, 26, 27, 28, 29,
	30, 31, 32, 32, 33, 34, 34, 35, 36, 36, 37, 37,
}

// spreadICDF and trimICDF match libopus entropy tables.
// Source: libopus celt/celt.h.
var spreadICDF = []uint8{25, 23, 2, 0}
var trimICDF = []uint8{126, 124, 119, 109, 87, 41, 19, 9, 4, 2, 0}
var tapsetICDF = []uint8{2, 1, 0}

// SpreadICDF exposes the spread entropy table for callers that need it.
var SpreadICDF = spreadICDF

// TrimICDF exposes the allocation trim entropy table for callers that need it.
var TrimICDF = trimICDF

// tfSelectTable maps TF selection to per-band resolution changes.
// Source: libopus celt/celt.c tf_select_table.
var tfSelectTable = [4][8]int8{
	{0, -1, 0, -1, 0, -1, 0, -1},
	{0, -1, 0, -2, 1, 0, 1, -1},
	{0, -2, 0, -3, 2, 0, 1, -1},
	{0, -2, 0, -3, 3, 0, 1, -1},
}

// LogN contains log2 of band widths (in Q3 fixed-point) for bit allocation.
// This is used in the bit allocation algorithm to weight bands.
// For band i, width = EBands[i+1] - EBands[i], and LogN[i] = round(log2(width) * 256)
//
// Source: libopus celt/modes.c (logN400 table)
var LogN = [21]int{
	0, 0, 0, 0, 0, 0, 0, 0,
	8, 8, 8, 8,
	16, 16, 16,
	21, 21,
	24,
	29,
	34,
	36,
}

// cacheCaps contains per-band PVQ caps for 48kHz modes (LM=0..3, C=1..2).
// Source: libopus celt/static_modes_float.h cache_caps50 table.
var cacheCaps = [168]uint8{
	224, 224, 224, 224, 224, 224, 224, 224, 160, 160, 160, 160, 185, 185, 185,
	178, 178, 168, 134, 61, 37, 224, 224, 224, 224, 224, 224, 224, 224, 240,
	240, 240, 240, 207, 207, 207, 198, 198, 183, 144, 66, 40, 160, 160, 160,
	160, 160, 160, 160, 160, 185, 185, 185, 185, 193, 193, 193, 183, 183, 172,
	138, 64, 38, 240, 240, 240, 240, 240, 240, 240, 240, 207, 207, 207, 207,
	204, 204, 204, 193, 193, 180, 143, 66, 40, 185, 185, 185, 185, 185, 185,
	185, 185, 193, 193, 193, 193, 193, 193, 193, 183, 183, 172, 138, 65, 39,
	207, 207, 207, 207, 207, 207, 207, 207, 204, 204, 204, 204, 201, 201, 201,
	188, 188, 176, 141, 66, 40, 193, 193, 193, 193, 193, 193, 193, 193, 193,
	193, 193, 193, 194, 194, 194, 184, 184, 173, 139, 65, 39, 204, 204, 204,
	204, 204, 204, 204, 204, 201, 201, 201, 201, 198, 198, 198, 187, 187, 175,
	140, 66, 40,
}

// cacheIndex50 maps (LM+1, band) to offsets into cacheBits50.
// Source: libopus celt/static_modes_float.h cache_index50 table.
var cacheIndex50 = [105]int16{
	-1, -1, -1, -1, -1, -1, -1, -1, 0, 0, 0, 0, 41, 41, 41,
	82, 82, 123, 164, 200, 222, 0, 0, 0, 0, 0, 0, 0, 0, 41,
	41, 41, 41, 123, 123, 123, 164, 164, 240, 266, 283, 295, 41, 41, 41,
	41, 41, 41, 41, 41, 123, 123, 123, 123, 240, 240, 240, 266, 266, 305,
	318, 328, 336, 123, 123, 123, 123, 123, 123, 123, 123, 240, 240, 240, 240,
	305, 305, 305, 318, 318, 343, 351, 358, 364, 240, 240, 240, 240, 240, 240,
	240, 240, 305, 305, 305, 305, 343, 343, 343, 351, 351, 370, 376, 382, 387,
}

// cacheBits50 contains concatenated pulse caches indexed by cacheIndex50.
// Each cache starts with maxPseudo and then bits per pseudo-pulse.
// Source: libopus celt/static_modes_float.h cache_bits50 table.
var cacheBits50 = [392]uint8{
	40, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7,
	7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7,
	7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 40, 15, 23, 28,
	31, 34, 36, 38, 39, 41, 42, 43, 44, 45, 46, 47, 47, 49, 50,
	51, 52, 53, 54, 55, 55, 57, 58, 59, 60, 61, 62, 63, 63, 65,
	66, 67, 68, 69, 70, 71, 71, 40, 20, 33, 41, 48, 53, 57, 61,
	64, 66, 69, 71, 73, 75, 76, 78, 80, 82, 85, 87, 89, 91, 92,
	94, 96, 98, 101, 103, 105, 107, 108, 110, 112, 114, 117, 119, 121, 123,
	124, 126, 128, 40, 23, 39, 51, 60, 67, 73, 79, 83, 87, 91, 94,
	97, 100, 102, 105, 107, 111, 115, 118, 121, 124, 126, 129, 131, 135, 139,
	142, 145, 148, 150, 153, 155, 159, 163, 166, 169, 172, 174, 177, 179, 35,
	28, 49, 65, 78, 89, 99, 107, 114, 120, 126, 132, 136, 141, 145, 149,
	153, 159, 165, 171, 176, 180, 185, 189, 192, 199, 205, 211, 216, 220, 225,
	229, 232, 239, 245, 251, 21, 33, 58, 79, 97, 112, 125, 137, 148, 157,
	166, 174, 182, 189, 195, 201, 207, 217, 227, 235, 243, 251, 17, 35, 63,
	86, 106, 123, 139, 152, 165, 177, 187, 197, 206, 214, 222, 230, 237, 250,
	25, 31, 55, 75, 91, 105, 117, 128, 138, 146, 154, 161, 168, 174, 180,
	185, 190, 200, 208, 215, 222, 229, 235, 240, 245, 255, 16, 36, 65, 89,
	110, 128, 144, 159, 173, 185, 196, 207, 217, 226, 234, 242, 250, 11, 41,
	74, 103, 128, 151, 172, 191, 209, 225, 241, 255, 9, 43, 79, 110, 138,
	163, 186, 207, 227, 246, 12, 39, 71, 99, 123, 144, 164, 182, 198, 214,
	228, 241, 253, 9, 44, 81, 113, 142, 168, 192, 214, 235, 255, 7, 49,
	90, 127, 160, 191, 220, 247, 6, 51, 95, 134, 170, 203, 234, 7, 47,
	87, 123, 155, 184, 212, 237, 6, 52, 97, 137, 174, 208, 240, 5, 57,
	106, 151, 192, 231, 5, 59, 111, 158, 202, 243, 5, 55, 103, 147, 187,
	224, 5, 60, 113, 161, 206, 248, 4, 65, 122, 175, 224, 4, 67, 127,
	182, 234,
}

// SmallDiv contains precomputed values for efficient small division in Laplace decoding.
// SmallDiv[i] = (1 << 16) / (i + 1) for i in 0..128
// Used for energy decoding to avoid expensive division operations.
//
// Source: libopus celt/laplace.c (ec_laplace_decode)
var SmallDiv = [129]uint16{
	65535, 32768, 21845, 16384, 13107, 10923, 9362, 8192, 7282, 6554, // 0-9
	5958, 5461, 5041, 4681, 4369, 4096, 3855, 3641, 3449, 3277, // 10-19
	3121, 2979, 2849, 2731, 2621, 2521, 2427, 2341, 2260, 2185, // 20-29
	2114, 2048, 1986, 1928, 1872, 1820, 1771, 1725, 1680, 1638, // 30-39
	1598, 1560, 1524, 1489, 1456, 1425, 1394, 1365, 1337, 1311, // 40-49
	1285, 1260, 1237, 1214, 1192, 1170, 1150, 1130, 1111, 1092, // 50-59
	1074, 1057, 1040, 1024, 1008, 993, 978, 964, 950, 936, // 60-69
	923, 910, 898, 886, 874, 862, 851, 840, 830, 819, // 70-79
	809, 799, 790, 780, 771, 762, 753, 745, 736, 728, // 80-89
	720, 712, 705, 697, 690, 683, 676, 669, 662, 655, // 90-99
	649, 643, 636, 630, 624, 618, 612, 607, 601, 596, // 100-109
	590, 585, 580, 575, 570, 565, 560, 555, 551, 546, // 110-119
	542, 537, 533, 529, 524, 520, 516, 512, 508, // 120-128
}

// BandWidth returns the number of MDCT bins in the given band at base frame size.
// For band i, this is EBands[i+1] - EBands[i].
func BandWidth(band int) int {
	if band < 0 || band >= MaxBands {
		return 0
	}
	return eBandWidths[band]
}

// ScaledBandStart returns the scaled MDCT bin index for the start of a band.
// For frame sizes larger than 2.5ms, indices are scaled by (frameSize/120).
func ScaledBandStart(band, frameSize int) int {
	if band < 0 || band > MaxBands {
		return 0
	}
	scale := frameSize / Overlap // 1 for 2.5ms, 2 for 5ms, 4 for 10ms, 8 for 20ms
	return EBands[band] * scale
}

// ScaledBandEnd returns the scaled MDCT bin index for the end of a band.
func ScaledBandEnd(band, frameSize int) int {
	if band < 0 || band >= MaxBands {
		return 0
	}
	scale := frameSize / Overlap
	return EBands[band+1] * scale
}

// ScaledBandWidth returns the number of MDCT bins in a band for a given frame size.
func ScaledBandWidth(band, frameSize int) int {
	if band < 0 || band >= MaxBands {
		return 0
	}
	scale := frameSize / Overlap
	return eBandWidths[band] * scale
}
