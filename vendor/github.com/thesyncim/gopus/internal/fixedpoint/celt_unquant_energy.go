//go:build gopus_fixed_point

package fixedpoint

import "github.com/thesyncim/gopus/internal/rangecoding"

// This file ports the entropy-coder-driven FIXED_POINT CELT energy
// unquantizers from celt/quant_bands.c: unquant_coarse_energy,
// unquant_fine_energy and unquant_energy_finalise. All log-energy values are
// celt_glog == opus_val32 in Q(DB_SHIFT)=Q24. The range decode is delegated to
// the shared rangecoding.Decoder, whose Laplace/ICDF/bit primitives are already
// byte-exact with libopus.

// predCoef and betaCoef are the FIXED_POINT inter-frame prediction and
// inter-band decay coefficients (Q15) indexed by LM. betaIntra is the intra
// inter-band decay (Q15). These mirror celt/quant_bands.c.
var (
	predCoef  = [4]int16{29440, 26112, 21248, 16384}
	betaCoef  = [4]int16{30147, 22282, 12124, 6554}
	betaIntra = int16(4915)
)

// eProbModel holds the Laplace coarse-energy probability models indexed by
// [LM][intra][2*band .. 2*band+1] (probability of 0 and decay rate, both Q8).
// It matches celt/quant_bands.c e_prob_model[4][2][42].
var eProbModel = [4][2][42]uint8{
	{
		{
			72, 127, 65, 129, 66, 128, 65, 128, 64, 128, 62, 128, 64, 128,
			64, 128, 92, 78, 92, 79, 92, 78, 90, 79, 116, 41, 115, 40,
			114, 40, 132, 26, 132, 26, 145, 17, 161, 12, 176, 10, 177, 11,
		},
		{
			24, 179, 48, 138, 54, 135, 54, 132, 53, 134, 56, 133, 55, 132,
			55, 132, 61, 114, 70, 96, 74, 88, 75, 88, 87, 74, 89, 66,
			91, 67, 100, 59, 108, 50, 120, 40, 122, 37, 97, 43, 78, 50,
		},
	},
	{
		{
			83, 78, 84, 81, 88, 75, 86, 74, 87, 71, 90, 73, 93, 74,
			93, 74, 109, 40, 114, 36, 117, 34, 117, 34, 143, 17, 145, 18,
			146, 19, 162, 12, 165, 10, 178, 7, 189, 6, 190, 8, 177, 9,
		},
		{
			23, 178, 54, 115, 63, 102, 66, 98, 69, 99, 74, 89, 71, 91,
			73, 91, 78, 89, 86, 80, 92, 66, 93, 64, 102, 59, 103, 60,
			104, 60, 117, 52, 123, 44, 138, 35, 133, 31, 97, 38, 77, 45,
		},
	},
	{
		{
			61, 90, 93, 60, 105, 42, 107, 41, 110, 45, 116, 38, 113, 38,
			112, 38, 124, 26, 132, 27, 136, 19, 140, 20, 155, 14, 159, 16,
			158, 18, 170, 13, 177, 10, 187, 8, 192, 6, 175, 9, 159, 10,
		},
		{
			21, 178, 59, 110, 71, 86, 75, 85, 84, 83, 91, 66, 88, 73,
			87, 72, 92, 75, 98, 72, 105, 58, 107, 54, 115, 52, 114, 55,
			112, 56, 129, 51, 132, 40, 150, 33, 140, 29, 98, 35, 77, 42,
		},
	},
	{
		{
			42, 121, 96, 66, 108, 43, 111, 40, 117, 44, 123, 32, 120, 36,
			119, 33, 127, 33, 134, 34, 139, 21, 147, 23, 152, 20, 158, 25,
			154, 26, 166, 21, 173, 16, 184, 13, 184, 10, 150, 13, 139, 15,
		},
		{
			22, 178, 63, 114, 74, 82, 84, 83, 92, 82, 103, 62, 96, 72,
			96, 67, 101, 73, 107, 72, 113, 55, 118, 52, 125, 52, 118, 52,
			117, 55, 135, 49, 137, 39, 157, 32, 145, 29, 97, 33, 77, 40,
		},
	},
}

// smallEnergyICDF is the inverse CDF used when only a couple of bits remain for
// a coarse-energy symbol. Matches celt/quant_bands.c small_energy_icdf.
var smallEnergyICDF = []uint8{2, 1, 0}

// maxFineBits mirrors celt/quant_bands.c MAX_FINE_BITS.
const maxFineBits = 8

// Laplace decode constants, matching celt/laplace.c.
const (
	laplaceLogMinP = 0
	laplaceMinP    = 1 << laplaceLogMinP
	laplaceNMin    = 16
	laplaceFTBits  = 15
	laplaceFS      = 1 << laplaceFTBits
)

// laplaceGetFreq1 mirrors celt/laplace.c ec_laplace_get_freq1.
func laplaceGetFreq1(fs0, decay int) int {
	ft := laplaceFS - laplaceMinP*(2*laplaceNMin) - fs0
	return (ft * (16384 - decay)) >> 15
}

// laplaceDecode ports celt/laplace.c ec_laplace_decode using the shared range
// decoder. fs and decay are the Q15 probability-of-zero and Q14 decay.
func laplaceDecode(dec *rangecoding.Decoder, fs, decay int) int {
	fm := int(dec.DecodeBin(laplaceFTBits))
	if fm < fs {
		dec.Update(0, uint32(fs), uint32(laplaceFS))
		return 0
	}

	val := 1
	fl := fs
	fs = laplaceGetFreq1(fs, decay) + laplaceMinP
	for fs > laplaceMinP && fm >= fl+2*fs {
		fs *= 2
		fl += fs
		fs = ((fs-2*laplaceMinP)*decay)>>15 + laplaceMinP
		val++
	}
	if fs <= laplaceMinP {
		di := (fm - fl) >> (laplaceLogMinP + 1)
		val += di
		fl += 2 * di * laplaceMinP
	}
	if fm < fl+fs {
		val = -val
	} else {
		fl += fs
	}
	fh := fl + fs
	if fh > laplaceFS {
		fh = laplaceFS
	}
	dec.Update(uint32(fl), uint32(fh), uint32(laplaceFS))
	return val
}

// UnquantCoarseEnergy ports the FIXED_POINT celt/quant_bands.c
// unquant_coarse_energy. It decodes the Laplace-coded coarse band energies for
// bands [start,end) into oldEBands (channel-major, Q24), applying inter-frame
// (coef) and inter-band (beta) prediction. oldEBands must already hold the
// previous-frame energies (or zeros for the intra/first frame) and has length
// C*nbEBands.
func UnquantCoarseEnergy(dec *rangecoding.Decoder, oldEBands []int32, start, end, nbEBands, C, LM int, intra bool) {
	probIdx := 0
	if intra {
		probIdx = 1
	}
	prob := eProbModel[LM][probIdx]

	var coef, beta int16
	if intra {
		coef = 0
		beta = betaIntra
	} else {
		beta = betaCoef[LM]
		coef = predCoef[LM]
	}

	budget := dec.StorageBits()

	var prev [2]int64
	for i := start; i < end; i++ {
		for c := 0; c < C; c++ {
			var qi int
			tell := dec.Tell()
			switch {
			case budget-tell >= 15:
				pi := 2 * imin(i, 20)
				qi = laplaceDecode(dec, int(prob[pi])<<7, int(prob[pi+1])<<6)
			case budget-tell >= 2:
				qi = dec.DecodeICDF(smallEnergyICDF, 2)
				qi = (qi >> 1) ^ -(qi & 1)
			case budget-tell >= 1:
				qi = -dec.DecodeBit(1)
			default:
				qi = -1
			}
			q := int32(qi) << dbShift

			idx := i + c*nbEBands
			oldEBands[idx] = max32(-gconst(9), oldEBands[idx])
			tmp := int32(int64(mult16x32q15(coef, oldEBands[idx])) + prev[c] + int64(q))
			tmp = min32(gconst(28), max32(-gconst(28), tmp))
			oldEBands[idx] = tmp
			prev[c] = prev[c] + int64(q) - int64(mult16x32q15(beta, q))
		}
	}
}

// UnquantFineEnergy ports the FIXED_POINT celt/quant_bands.c
// unquant_fine_energy. It refines bands [start,end) of oldEBands using
// extraQuant[i] fine bits per band (prevQuant may be nil).
func UnquantFineEnergy(dec *rangecoding.Decoder, oldEBands []int32, start, end, nbEBands, C int, prevQuant, extraQuant []int32) {
	for i := start; i < end; i++ {
		extra := extraQuant[i]
		if extra <= 0 {
			continue
		}
		if dec.Tell()+C*int(extra) > dec.StorageBits() {
			continue
		}
		var prev int32
		if prevQuant != nil {
			prev = prevQuant[i]
		}
		for c := 0; c < C; c++ {
			q2 := int32(dec.DecodeRawBits(uint(extra)))
			offset := vshr32(2*q2+1, int(extra)-dbShift+1) - gconst1Half
			offset = shr32(offset, int(prev))
			oldEBands[i+c*nbEBands] += offset
		}
	}
}

// UnquantEnergyFinalise ports the FIXED_POINT celt/quant_bands.c
// unquant_energy_finalise. It spends the remaining bitsLeft refining bands in
// two priority passes.
func UnquantEnergyFinalise(dec *rangecoding.Decoder, oldEBands []int32, start, end, nbEBands, C int, fineQuant, finePriority []int32, bitsLeft int) {
	for prio := int32(0); prio < 2; prio++ {
		for i := start; i < end && bitsLeft >= C; i++ {
			if fineQuant[i] >= maxFineBits || finePriority[i] != prio {
				continue
			}
			for c := 0; c < C; c++ {
				q2 := int32(dec.DecodeRawBits(1))
				offset := shr32((q2<<dbShift)-gconst1Half, int(fineQuant[i])+1)
				oldEBands[i+c*nbEBands] += offset
				bitsLeft--
			}
		}
	}
}

// gconst1Half is GCONST(.5f) = (celt_glog)(0.5 + 0.5*(1<<DB_SHIFT)) = 1<<23.
const gconst1Half = int32(1) << (dbShift - 1)
