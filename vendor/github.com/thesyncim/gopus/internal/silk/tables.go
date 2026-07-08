// Package silk implements the SILK audio decoder per RFC 6716 Section 4.2.
package silk

// ICDF (Inverse Cumulative Distribution Function) tables for SILK parameter decoding.
// All tables are uint16 slices starting at 256 and decreasing to 0.
// These are used with the range decoder's DecodeICDF method.
// Note: uint16 is required because 256 doesn't fit in uint8.
//
// Reference: RFC 6716 Section 4.2

// Frame Type Tables (RFC 6716 Section 4.2.7.3)
// -----------------------------------------------------------------------------

// ICDFFrameTypeVADInactive - frame type for VAD inactive frames (no VAD).
// From libopus silk_type_offset_no_VAD_iCDF = {230, 0}
// Symbol 0 = inactive with probability 230/256 ≈ 90%
var ICDFFrameTypeVADInactive = []uint16{230, 0}

// ICDFFrameTypeVADActive - frame type for VAD active frames.
// From libopus silk_type_offset_VAD_iCDF = {232, 158, 10, 0}
// Encodes (signalType-1)*2 + quantOffset with 4 outcomes:
// 0: unvoiced low, 1: unvoiced high, 2: voiced low, 3: voiced high
var ICDFFrameTypeVADActive = []uint16{232, 158, 10, 0}

// Gain Tables (RFC 6716 Section 4.2.7.4)
// -----------------------------------------------------------------------------

// ICDFGainMSBInactive - gain MSB for inactive signal type.
var ICDFGainMSBInactive = []uint16{
	256, 224, 192, 160, 128, 96, 64, 32, 0,
}

// ICDFGainMSBUnvoiced - gain MSB for unvoiced signal type.
var ICDFGainMSBUnvoiced = []uint16{
	256, 204, 154, 102, 51, 0,
}

// ICDFGainMSBVoiced - gain MSB for voiced signal type.
var ICDFGainMSBVoiced = []uint16{
	256, 255, 244, 220, 186, 145, 100, 56, 20, 0,
}

// ICDFGainLSB - gain LSB (3 bits = 8 values).
var ICDFGainLSB = []uint16{
	256, 224, 192, 160, 128, 96, 64, 32, 0,
}

// ICDFDeltaGain - delta gain between subframes.
// Centered at 4, so delta = decoded - 4.
var ICDFDeltaGain = []uint16{
	256, 250, 245, 239, 230, 219, 203, 180, 149, 111, 73, 41, 20, 8, 2, 0,
}

// LSF Stage 1 Tables (RFC 6716 Section 4.2.7.5.1)
// -----------------------------------------------------------------------------

// ICDFLSFStage1NBMBVoiced - LSF stage 1 codebook index for NB/MB voiced.
var ICDFLSFStage1NBMBVoiced = []uint16{
	256, 240, 226, 214, 202, 190, 178, 166, 154, 142, 130, 118,
	106, 94, 82, 70, 58, 48, 40, 32, 24, 17, 11, 6, 2, 0,
}

// ICDFLSFStage1NBMBUnvoiced - LSF stage 1 codebook index for NB/MB unvoiced.
var ICDFLSFStage1NBMBUnvoiced = []uint16{
	256, 239, 223, 208, 193, 178, 163, 149, 135, 122, 109, 96,
	84, 72, 61, 51, 42, 33, 25, 18, 12, 7, 3, 0,
}

// ICDFLSFStage1WBVoiced - LSF stage 1 codebook index for WB voiced.
var ICDFLSFStage1WBVoiced = []uint16{
	256, 238, 221, 204, 188, 173, 158, 144, 131, 118, 106, 95,
	84, 74, 65, 56, 47, 39, 32, 25, 19, 13, 8, 4, 1, 0,
}

// ICDFLSFStage1WBUnvoiced - LSF stage 1 codebook index for WB unvoiced.
var ICDFLSFStage1WBUnvoiced = []uint16{
	256, 238, 221, 205, 190, 175, 161, 148, 135, 123, 111, 100,
	89, 79, 69, 60, 51, 43, 35, 28, 21, 15, 10, 6, 3, 1, 0,
}

// LSF Stage 2 Tables (RFC 6716 Section 4.2.7.5.2)
// 8 probability tables per bandwidth group (NB/MB and WB)
// -----------------------------------------------------------------------------

// ICDFLSFStage2NBMB - LSF stage 2 residual tables for NB/MB.
// 8 tables, each with varying number of symbols.
var ICDFLSFStage2NBMB = [8][]uint16{
	{256, 212, 168, 127, 85, 42, 0},
	{256, 235, 195, 146, 90, 37, 0},
	{256, 218, 175, 133, 91, 47, 0},
	{256, 226, 185, 139, 91, 43, 0},
	{256, 231, 192, 147, 96, 44, 0},
	{256, 238, 206, 164, 113, 58, 0},
	{256, 232, 196, 155, 107, 54, 0},
	{256, 228, 190, 148, 101, 50, 0},
}

// ICDFLSFStage2WB - LSF stage 2 residual tables for WB.
// 8 tables with potentially different symbol counts.
var ICDFLSFStage2WB = [8][]uint16{
	{256, 212, 168, 127, 85, 42, 0},
	{256, 235, 195, 146, 90, 37, 0},
	{256, 218, 175, 133, 91, 47, 0},
	{256, 226, 185, 139, 91, 43, 0},
	{256, 231, 192, 147, 96, 44, 0},
	{256, 238, 206, 164, 113, 58, 0},
	{256, 232, 196, 155, 107, 54, 0},
	{256, 228, 190, 148, 101, 50, 0},
}

// ICDFLSFInterpolation - LSF interpolation weight index.
// 5 possible interpolation values (0, 0.25, 0.5, 0.75, 1).
var ICDFLSFInterpolation = []uint16{
	256, 200, 150, 100, 50, 0,
}

// Pitch/LTP Tables (RFC 6716 Section 4.2.7.6)
// -----------------------------------------------------------------------------

// ICDFPitchLagNB - pitch lag MSB for narrowband.
// 8 values for high/contour parts.
var ICDFPitchLagNB = []uint16{
	256, 230, 204, 178, 153, 128, 102, 76, 51, 0,
}

// ICDFPitchLagMB - pitch lag MSB for mediumband.
var ICDFPitchLagMB = []uint16{
	256, 237, 218, 199, 181, 162, 144, 127, 109, 92, 76, 60, 45, 30, 15, 0,
}

// ICDFPitchLagWB - pitch lag MSB for wideband.
var ICDFPitchLagWB = []uint16{
	256, 245, 234, 223, 213, 203, 193, 183, 173, 163, 153, 143,
	133, 124, 115, 106, 97, 88, 79, 70, 62, 54, 46, 38, 30, 22, 15, 8, 0,
}

// ICDFPitchContourNB - pitch contour for narrowband (16 values).
var ICDFPitchContourNB = []uint16{
	256, 235, 215, 195, 175, 155, 135, 115, 95, 75, 55, 35, 17, 10, 5, 2, 0,
}

// ICDFPitchContourMB - pitch contour for mediumband.
var ICDFPitchContourMB = []uint16{
	256, 178, 110, 55, 0,
}

// ICDFPitchContourWB - pitch contour for wideband.
var ICDFPitchContourWB = []uint16{
	256, 178, 110, 55, 0,
}

// ICDFLTPFilterIndexLowPeriod - LTP filter periodicity index (low period).
var ICDFLTPFilterIndexLowPeriod = []uint16{
	256, 185, 114, 43, 0,
}

// ICDFLTPFilterIndexMidPeriod - LTP filter periodicity index (mid period).
var ICDFLTPFilterIndexMidPeriod = []uint16{
	256, 196, 138, 83, 36, 0,
}

// ICDFLTPFilterIndexHighPeriod - LTP filter periodicity index (high period).
var ICDFLTPFilterIndexHighPeriod = []uint16{
	256, 206, 157, 109, 63, 21, 0,
}

// ICDFLTPGainLow - LTP gain index for low periodicity.
var ICDFLTPGainLow = []uint16{
	256, 224, 192, 160, 128, 96, 64, 32, 0,
}

// ICDFLTPGainMid - LTP gain index for mid periodicity.
var ICDFLTPGainMid = []uint16{
	256, 240, 224, 208, 192, 176, 160, 144, 128, 112, 96, 80, 64, 48, 32, 16, 0,
}

// ICDFLTPGainHigh - LTP gain index for high periodicity.
var ICDFLTPGainHigh = []uint16{
	256, 248, 240, 232, 224, 216, 208, 200, 192, 184, 176, 168, 160, 152, 144, 136,
	128, 120, 112, 104, 96, 88, 80, 72, 64, 56, 48, 40, 32, 24, 16, 8, 0,
}

// Excitation Tables (RFC 6716 Section 4.2.7.8)
// -----------------------------------------------------------------------------

// ICDFExcitationPulseCount - pulse count per shell block.
var ICDFExcitationPulseCount = []uint16{
	256, 240, 224, 208, 192, 176, 160, 144, 128, 112, 96, 80, 64, 48, 32, 16, 0,
}

// ICDFExcitationSplit - binary split for shell coding.
// Multiple tables for different pulse counts.
var icdfExcitationSplit0 = []uint16{256, 0}
var icdfExcitationSplit1 = []uint16{256, 128, 0}
var icdfExcitationSplit2 = []uint16{256, 171, 85, 0}
var icdfExcitationSplit3 = []uint16{256, 192, 128, 64, 0}
var icdfExcitationSplit4 = []uint16{256, 205, 154, 102, 51, 0}
var icdfExcitationSplit5 = []uint16{256, 213, 171, 128, 85, 43, 0}
var icdfExcitationSplit6 = []uint16{256, 219, 183, 146, 110, 73, 37, 0}
var icdfExcitationSplit7 = []uint16{256, 224, 192, 160, 128, 96, 64, 32, 0}
var icdfExcitationSplit8 = []uint16{256, 228, 199, 171, 142, 114, 85, 57, 28, 0}
var icdfExcitationSplit9 = []uint16{256, 230, 205, 179, 154, 128, 102, 77, 51, 26, 0}
var icdfExcitationSplit10 = []uint16{256, 233, 210, 186, 163, 140, 116, 93, 70, 47, 23, 0}
var icdfExcitationSplit11 = []uint16{256, 235, 213, 192, 171, 149, 128, 107, 85, 64, 43, 21, 0}
var icdfExcitationSplit12 = []uint16{256, 236, 216, 197, 177, 158, 138, 118, 99, 79, 59, 39, 20, 0}
var icdfExcitationSplit13 = []uint16{256, 238, 219, 201, 183, 164, 146, 128, 110, 91, 73, 55, 37, 18, 0}
var icdfExcitationSplit14 = []uint16{256, 239, 222, 204, 187, 170, 152, 135, 118, 101, 83, 66, 49, 31, 14, 0}
var icdfExcitationSplit15 = []uint16{256, 240, 224, 208, 192, 176, 160, 144, 128, 112, 96, 80, 64, 48, 32, 16, 0}
var icdfExcitationSplit16 = []uint16{256, 241, 226, 211, 195, 180, 165, 150, 135, 120, 105, 90, 75, 60, 45, 30, 15, 0}

// ICDFExcitationSplit indexed by pulse count.
var ICDFExcitationSplit = [][]uint16{
	icdfExcitationSplit0, icdfExcitationSplit1, icdfExcitationSplit2, icdfExcitationSplit3,
	icdfExcitationSplit4, icdfExcitationSplit5, icdfExcitationSplit6, icdfExcitationSplit7,
	icdfExcitationSplit8, icdfExcitationSplit9, icdfExcitationSplit10, icdfExcitationSplit11,
	icdfExcitationSplit12, icdfExcitationSplit13, icdfExcitationSplit14, icdfExcitationSplit15,
	icdfExcitationSplit16,
}

// ICDFExcitationLSB - LSB for pulse magnitudes.
var ICDFExcitationLSB = []uint16{
	256, 136, 0,
}

// Excitation sign tables indexed by (signalType * 2 + quantOffset) * 7 + pulseCount.
// 3 signal types x 2 quant offsets x 7 pulse counts = 42 tables.
// Signal type 0 (inactive), quant offset 0
var icdfExcitationSign_0_0_1 = []uint16{256, 128, 0}
var icdfExcitationSign_0_0_2 = []uint16{256, 128, 0}
var icdfExcitationSign_0_0_3 = []uint16{256, 128, 0}
var icdfExcitationSign_0_0_4 = []uint16{256, 128, 0}
var icdfExcitationSign_0_0_5 = []uint16{256, 128, 0}
var icdfExcitationSign_0_0_6 = []uint16{256, 128, 0}

// Signal type 0 (inactive), quant offset 1
var icdfExcitationSign_0_1_1 = []uint16{256, 128, 0}
var icdfExcitationSign_0_1_2 = []uint16{256, 128, 0}
var icdfExcitationSign_0_1_3 = []uint16{256, 128, 0}
var icdfExcitationSign_0_1_4 = []uint16{256, 128, 0}
var icdfExcitationSign_0_1_5 = []uint16{256, 128, 0}
var icdfExcitationSign_0_1_6 = []uint16{256, 128, 0}

// Signal type 1 (unvoiced), quant offset 0
var icdfExcitationSign_1_0_1 = []uint16{256, 185, 0}
var icdfExcitationSign_1_0_2 = []uint16{256, 168, 0}
var icdfExcitationSign_1_0_3 = []uint16{256, 155, 0}
var icdfExcitationSign_1_0_4 = []uint16{256, 146, 0}
var icdfExcitationSign_1_0_5 = []uint16{256, 138, 0}
var icdfExcitationSign_1_0_6 = []uint16{256, 133, 0}

// Signal type 1 (unvoiced), quant offset 1
var icdfExcitationSign_1_1_1 = []uint16{256, 172, 0}
var icdfExcitationSign_1_1_2 = []uint16{256, 157, 0}
var icdfExcitationSign_1_1_3 = []uint16{256, 146, 0}
var icdfExcitationSign_1_1_4 = []uint16{256, 138, 0}
var icdfExcitationSign_1_1_5 = []uint16{256, 132, 0}
var icdfExcitationSign_1_1_6 = []uint16{256, 128, 0}

// Signal type 2 (voiced), quant offset 0
var icdfExcitationSign_2_0_1 = []uint16{256, 162, 0}
var icdfExcitationSign_2_0_2 = []uint16{256, 152, 0}
var icdfExcitationSign_2_0_3 = []uint16{256, 143, 0}
var icdfExcitationSign_2_0_4 = []uint16{256, 137, 0}
var icdfExcitationSign_2_0_5 = []uint16{256, 132, 0}
var icdfExcitationSign_2_0_6 = []uint16{256, 128, 0}

// Signal type 2 (voiced), quant offset 1
var icdfExcitationSign_2_1_1 = []uint16{256, 150, 0}
var icdfExcitationSign_2_1_2 = []uint16{256, 142, 0}
var icdfExcitationSign_2_1_3 = []uint16{256, 136, 0}
var icdfExcitationSign_2_1_4 = []uint16{256, 131, 0}
var icdfExcitationSign_2_1_5 = []uint16{256, 128, 0}
var icdfExcitationSign_2_1_6 = []uint16{256, 125, 0}

// ICDFExcitationSign indexed by [signalType][quantOffset][pulseCount-1].
// Note: pulseCount 0 doesn't need sign decoding.
var ICDFExcitationSign = [3][2][6][]uint16{
	// Signal type 0 (inactive)
	{
		{icdfExcitationSign_0_0_1, icdfExcitationSign_0_0_2, icdfExcitationSign_0_0_3,
			icdfExcitationSign_0_0_4, icdfExcitationSign_0_0_5, icdfExcitationSign_0_0_6},
		{icdfExcitationSign_0_1_1, icdfExcitationSign_0_1_2, icdfExcitationSign_0_1_3,
			icdfExcitationSign_0_1_4, icdfExcitationSign_0_1_5, icdfExcitationSign_0_1_6},
	},
	// Signal type 1 (unvoiced)
	{
		{icdfExcitationSign_1_0_1, icdfExcitationSign_1_0_2, icdfExcitationSign_1_0_3,
			icdfExcitationSign_1_0_4, icdfExcitationSign_1_0_5, icdfExcitationSign_1_0_6},
		{icdfExcitationSign_1_1_1, icdfExcitationSign_1_1_2, icdfExcitationSign_1_1_3,
			icdfExcitationSign_1_1_4, icdfExcitationSign_1_1_5, icdfExcitationSign_1_1_6},
	},
	// Signal type 2 (voiced)
	{
		{icdfExcitationSign_2_0_1, icdfExcitationSign_2_0_2, icdfExcitationSign_2_0_3,
			icdfExcitationSign_2_0_4, icdfExcitationSign_2_0_5, icdfExcitationSign_2_0_6},
		{icdfExcitationSign_2_1_1, icdfExcitationSign_2_1_2, icdfExcitationSign_2_1_3,
			icdfExcitationSign_2_1_4, icdfExcitationSign_2_1_5, icdfExcitationSign_2_1_6},
	},
}

// VAD and LBRR Tables (RFC 6716 Section 4.2.3-4.2.4)
// -----------------------------------------------------------------------------

// ICDFVADFlag - VAD flag decoding (per frame).
var ICDFVADFlag = []uint16{
	256, 155, 0,
}

// ICDFLBRRFlag - LBRR (Low Bitrate Redundancy) flag decoding.
var ICDFLBRRFlag = []uint16{
	256, 205, 0,
}

// ICDFLBRRFlags2 - LBRR flags for 2 frames.
var ICDFLBRRFlags2 = []uint16{
	256, 217, 188, 65, 0,
}

// ICDFLBRRFlags3 - LBRR flags for 3 frames.
var ICDFLBRRFlags3 = []uint16{
	256, 226, 204, 183, 132, 108, 66, 17, 0,
}

// Rate Control Tables (RFC 6716 Section 4.2.7.2)
// -----------------------------------------------------------------------------

// ICDFRateLevelUnvoiced - rate level index for unvoiced.
var ICDFRateLevelUnvoiced = []uint16{
	256, 241, 221, 193, 159, 118, 72, 31, 0,
}

// ICDFRateLevelVoiced - rate level index for voiced.
var ICDFRateLevelVoiced = []uint16{
	256, 232, 200, 162, 120, 78, 42, 14, 0,
}

// LCG Seed Table (RFC 6716 Section 4.2.7.8.5)
// -----------------------------------------------------------------------------

// ICDFLCGSeed - uniform 4-symbol distribution for LCG seed.
var ICDFLCGSeed = []uint16{
	256, 192, 128, 64, 0,
}

// Stereo Tables (RFC 6716 Section 4.2.8)
// -----------------------------------------------------------------------------

// ICDFStereoOnlyFlag - stereo-only flag (side channel present).
var ICDFStereoOnlyFlag = []uint16{
	256, 128, 0,
}

// ICDFStereoPredWeight - stereo prediction weight index.
var ICDFStereoPredWeight = []uint16{
	256, 223, 191, 159, 127, 95, 63, 31, 0,
}

// ICDFStereoPredWeightDelta - stereo prediction weight delta.
var ICDFStereoPredWeightDelta = []uint16{
	256, 244, 220, 180, 126, 72, 36, 12, 0,
}

// Additional Tables (RFC 6716 Section 4.2)
// -----------------------------------------------------------------------------

// ICDFPitchDelta - pitch lag delta for contour coding.
var ICDFPitchDelta = []uint16{
	256, 232, 204, 171, 128, 85, 52, 24, 0,
}

// ICDFPitchLowBitsQ2 - low bits (Q2) of pitch lag.
var ICDFPitchLowBitsQ2 = []uint16{
	256, 192, 128, 64, 0,
}

// ICDFPitchLowBitsQ3 - low bits (Q3) of pitch lag.
var ICDFPitchLowBitsQ3 = []uint16{
	256, 224, 192, 160, 128, 96, 64, 32, 0,
}

// ICDFPitchLowBitsQ4 - low bits (Q4) of pitch lag.
var ICDFPitchLowBitsQ4 = []uint16{
	256, 240, 224, 208, 192, 176, 160, 144, 128, 112, 96, 80, 64, 48, 32, 16, 0,
}

// Shell Block Tables (RFC 6716 Section 4.2.7.8.3)
// -----------------------------------------------------------------------------

// Shell coding tables indexed by total pulse count.
// These define the probability distribution for shell coding partitions.
var icdfShellBlock0 = []uint16{256, 0}
var icdfShellBlock1 = []uint16{256, 128, 0}
var icdfShellBlock2 = []uint16{256, 171, 85, 0}
var icdfShellBlock3 = []uint16{256, 192, 128, 64, 0}
var icdfShellBlock4 = []uint16{256, 205, 154, 102, 51, 0}
var icdfShellBlock5 = []uint16{256, 213, 171, 128, 85, 43, 0}
var icdfShellBlock6 = []uint16{256, 219, 183, 146, 110, 73, 37, 0}
var icdfShellBlock7 = []uint16{256, 224, 192, 160, 128, 96, 64, 32, 0}
var icdfShellBlock8 = []uint16{256, 228, 199, 171, 142, 114, 85, 57, 28, 0}
var icdfShellBlock9 = []uint16{256, 230, 205, 179, 154, 128, 102, 77, 51, 26, 0}
var icdfShellBlock10 = []uint16{256, 233, 210, 186, 163, 140, 116, 93, 70, 47, 23, 0}
var icdfShellBlock11 = []uint16{256, 235, 213, 192, 171, 149, 128, 107, 85, 64, 43, 21, 0}
var icdfShellBlock12 = []uint16{256, 236, 216, 197, 177, 158, 138, 118, 99, 79, 59, 39, 20, 0}
var icdfShellBlock13 = []uint16{256, 238, 219, 201, 183, 164, 146, 128, 110, 91, 73, 55, 37, 18, 0}
var icdfShellBlock14 = []uint16{256, 239, 222, 204, 187, 170, 152, 135, 118, 101, 83, 66, 49, 31, 14, 0}
var icdfShellBlock15 = []uint16{256, 240, 224, 208, 192, 176, 160, 144, 128, 112, 96, 80, 64, 48, 32, 16, 0}
var icdfShellBlock16 = []uint16{256, 241, 226, 211, 195, 180, 165, 150, 135, 120, 105, 90, 75, 60, 45, 30, 15, 0}
var icdfShellBlock17 = []uint16{256, 242, 227, 213, 198, 184, 170, 155, 141, 127, 113, 99, 85, 71, 57, 43, 29, 14, 0}

// ICDFShellBlocks indexed by total pulse count.
var ICDFShellBlocks = [][]uint16{
	icdfShellBlock0, icdfShellBlock1, icdfShellBlock2, icdfShellBlock3,
	icdfShellBlock4, icdfShellBlock5, icdfShellBlock6, icdfShellBlock7,
	icdfShellBlock8, icdfShellBlock9, icdfShellBlock10, icdfShellBlock11,
	icdfShellBlock12, icdfShellBlock13, icdfShellBlock14, icdfShellBlock15,
	icdfShellBlock16, icdfShellBlock17,
}

// Additional Gain Tables (RFC 6716 Section 4.2.7.4)
// -----------------------------------------------------------------------------

// ICDFGainHighBits - high bits of absolute gain indexed by signal type.
var ICDFGainHighBits = [][]uint16{
	// Inactive
	{256, 224, 192, 160, 128, 96, 64, 32, 0},
	// Unvoiced
	{256, 204, 153, 102, 51, 0},
	// Voiced
	{256, 255, 244, 220, 186, 145, 100, 56, 20, 0},
}

// ICDFGainDelta - delta gain between subframes (same as ICDFDeltaGain).
var ICDFGainDelta = ICDFDeltaGain
