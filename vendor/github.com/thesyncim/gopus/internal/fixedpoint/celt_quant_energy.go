//go:build gopus_fixed_point

package fixedpoint

// This file ports the pure-integer log-domain energy prediction helpers of the
// libopus FIXED_POINT CELT energy quantizer (celt/quant_bands.c). These are the
// entropy-coder-independent pieces that convert between band amplitudes and the
// log2 energy domain used by quant_coarse_energy / quant_fine_energy.
//
// The default (non-QEXT) FIXED_POINT build resolves celt_log2_db to
// SHL32(EXTEND32(celt_log2(x)), DB_SHIFT-10), so these helpers call the existing
// CeltLog2 kernel; no new math primitive is introduced.

// dbShift mirrors celt/arch.h DB_SHIFT: log-energy values are Q(DB_SHIFT)=Q24.
const dbShift = 24

// eMeans holds the per-band mean energy quantized in Q4, matching the
// FIXED_POINT celt/quant_bands.c eMeans[25] table.
var eMeans = [25]int8{
	103, 100, 92, 85, 81,
	77, 72, 70, 78, 75,
	73, 71, 78, 74, 69,
	72, 70, 74, 76, 71,
	60, 60, 60, 60, 60,
}

// gconst evaluates the FIXED_POINT GCONST(x) macro for the energy log domain:
// (celt_glog)(0.5 + x*(1<<DB_SHIFT)). It is only used here with exact integer
// arguments, where the result is x<<DB_SHIFT.
func gconst(x int32) int32 {
	return x << dbShift
}

// Amp2Log2 ports the FIXED_POINT celt/quant_bands.c amp2Log2: for each channel c
// and band i in [0,effEnd) it converts the Q12 band amplitude bandE into a
// Q(DB_SHIFT)=Q24 log2 energy with the per-band mean removed, then fills the
// remaining bands [effEnd,end) with -GCONST(14.f).
//
// The conversion is
//
//	bandLogE[k] = celt_log2_db(bandE[k]) - SHL32(eMeans[i], DB_SHIFT-4) + GCONST(2.f)
//
// where celt_log2_db(x) = celt_log2(x)<<(DB_SHIFT-10). The +GCONST(2.f) term
// compensates for bandE being Q12 while celt_log2 (CeltLog2) takes a Q14 input.
//
// Inputs mirror the libopus CELTMode plumbing:
//
//	bandE     band amplitudes (Q12), channel-major, length >= C*nbEBands
//	bandLogE  output log2 energies (Q24), channel-major, length >= C*nbEBands
//	nbEBands  m->nbEBands
//	effEnd    bands carrying real energy
//	end       total active bands (effEnd <= end <= nbEBands)
//	C         channel count
func Amp2Log2(bandE []int32, bandLogE []int32, nbEBands, effEnd, end, C int) {
	for c := 0; c < C; c++ {
		base := c * nbEBands
		for i := 0; i < effEnd; i++ {
			logE := int32(CeltLog2(bandE[base+i])) << (dbShift - 10)
			logE -= int32(eMeans[i]) << (dbShift - 4)
			logE += gconst(2)
			bandLogE[base+i] = logE
		}
		for i := effEnd; i < end; i++ {
			bandLogE[base+i] = -gconst(14)
		}
	}
}
