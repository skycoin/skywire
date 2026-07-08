package silk

var silk_NLSF_CB_WB = nlsfCB{
	nVectors:           32,
	order:              16,
	quantStepSizeQ16:   int16(silkFixConst(0.15, 16)),
	invQuantStepSizeQ6: int16(silkFixConst(1.0/0.15, 6)),
	cb1NLSFQ8:          silk_NLSF_CB1_WB_Q8,
	cb1WghtQ9:          silk_NLSF_CB1_WB_Wght_Q9,
	cb1ICDF:            silk_NLSF_CB1_iCDF_WB,
	predQ8:             silk_NLSF_PRED_WB_Q8,
	ecSel:              silk_NLSF_CB2_SELECT_WB,
	ecICDF:             silk_NLSF_CB2_iCDF_WB,
	ecRatesQ5:          silk_NLSF_CB2_BITS_WB_Q5,
	deltaMinQ15:        silk_NLSF_DELTA_MIN_WB_Q15,
}

var silk_NLSF_CB_NB_MB = nlsfCB{
	nVectors:           32,
	order:              10,
	quantStepSizeQ16:   int16(silkFixConst(0.18, 16)),
	invQuantStepSizeQ6: int16(silkFixConst(1.0/0.18, 6)),
	cb1NLSFQ8:          silk_NLSF_CB1_NB_MB_Q8,
	cb1WghtQ9:          silk_NLSF_CB1_Wght_Q9,
	cb1ICDF:            silk_NLSF_CB1_iCDF_NB_MB,
	predQ8:             silk_NLSF_PRED_NB_MB_Q8,
	ecSel:              silk_NLSF_CB2_SELECT_NB_MB,
	ecICDF:             silk_NLSF_CB2_iCDF_NB_MB,
	ecRatesQ5:          silk_NLSF_CB2_BITS_NB_MB_Q5,
	deltaMinQ15:        silk_NLSF_DELTA_MIN_NB_MB_Q15,
}
