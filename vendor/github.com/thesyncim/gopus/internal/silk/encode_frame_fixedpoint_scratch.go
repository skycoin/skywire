//go:build gopus_fixed_point

package silk

// silkFixedEncodeScratch holds the reusable per-frame working buffers for the
// FIXED_POINT integer SILK encode path. The integer encode state
// (silkFixedEncodeState) owns one instance and reuses it across every frame of
// a (possibly multi-frame) packet, so the steady-state encode allocates nothing
// once the buffers have grown to the frame's dimensions. It mirrors the float
// path's Encoder scratch fields and the CELT fixedpoint celtEncodeScratch
// pattern: every buffer is grown once via an ensure* helper that reslices when
// capacity is already sufficient.
//
// The buffers carry no cross-frame state; each consumer fully overwrites the
// prefix it uses before reading it. Where libopus serializes two passes over
// the same kernel within one frame (the LBRR re-quantization vs the main NSQ
// rate loop, or each gain/Lambda iteration), the passes run sequentially and
// every leaf overwrites its working buffer, so a single scratch instance is
// shared safely.
type silkFixedEncodeScratch struct {
	// silkEncodeFrameFIXAnalyze per-subframe / coefficient outputs handed to NSQ.
	resNrgQ          []int32 // process_gains residual energy (int32 view)
	gainsQ16         []int32 // process_gains working gains
	predCoefFlat     []int16 // 2*maxLPCOrder packed prediction coefficients
	arQ13            []int16 // nbSubfr*maxShapeLpcOrder AR shaping coefficients
	ltpCoefQ14       []int16 // nbSubfr*ltpOrderConst LTP coefficients
	harmShapeGainQ14 []int32 // nbSubfr harmonic shaping gains
	tiltQ14          []int32 // nbSubfr tilt values
	lfShpQ14         []int32 // nbSubfr low-frequency shaping
	pitchLQ          []int32 // nbSubfr pitch lags (int32 view)

	// silkEncodeFramePayloadFIX rate-control loop buffers.
	gainIndices []int8  // nbSubfr gain indices
	ecBufCopy   []byte  // range-encoder buffer snapshot (found-lower restore)
	pulses      []int8  // frameLength excitation pulses
	gq          []int32 // nbSubfr gains snapshot for re-quantization

	// silkLBRREncodeFIX buffers.
	lbrrTempGainsQ16 []int32 // nbSubfr saved gains
	lbrrPulses       []int8  // frameLength LBRR excitation pulses

	// silkFindPredCoefsFIX buffers.
	lpcInPre     []int16 // nbSubfr*predOrder + frameLength LPC input
	fpLTPCoefQ14 []int16 // nbSubfr*ltpOrder LTP coefficients (result-owned)
	xXLTPQ17     []int32 // nbSubfr*ltpOrder LTP correlation vector
	XXLTPQ17     []int32 // nbSubfr*ltpOrder*ltpOrder LTP correlation matrix
	fpResNrg     []int32 // nbSubfr residual energy
	fpResNrgQ    []int   // nbSubfr residual energy Q

	// silkFindPredCoefsFIX NLSF working vectors. fpNLSFQ15 is mutated by
	// silkProcessNLSFsFixed; fpPrevNLSFQ15 is a copy of the previous-frame NLSFs
	// passed to it so the silkFindPredCoefsInput pointer does not escape via a
	// slice of its embedded array.
	fpNLSFQ15     []int16 // predOrder quantized NLSFs
	fpPrevNLSFQ15 []int16 // predOrder previous-frame NLSFs

	// silkProcessNLSFsFixed buffers.
	nlsfWQW       []int16 // order Laroia weights
	nlsf0TempQ15  []int16 // order interpolated NLSFs
	nlsfW0TempQW  []int16 // order interpolated-half weights
	nlsfIndices   []int8  // order+1 codebook indices
	predCoefQ12_0 []int16 // order first-half LPC coefficients
	predCoefQ12_1 []int16 // order second-half LPC coefficients

	// silkProcessGainsFixed buffers.
	pgGainsUnqQ16  []int32 // nbSubfr unquantized gains
	pgGainsIndices []int8  // nbSubfr gain indices

	// silkFindLPCFIX interpolation buffer.
	findLPCRes []int16 // 2*subfrLength LPC residual

	// silkA2NLSFInto P/Q polynomial scratch (dd+1 <= maxLPCOrder/2+1 entries).
	a2nlsfP [maxLPCOrder/2 + 1]int32
	a2nlsfQ [maxLPCOrder/2 + 1]int32

	// silkFindPitchLagsFIXFrontEnd buffers.
	flWsig     []int16 // pitchLPCWinLength windowed signal
	flAutoCorr []int32 // maxFindPitchLPCOrder+1 autocorrelation
	flRes      []int16 // bufLen whitened residual (consumed downstream as resPitch)

	// silkNoiseShapeAnalysisFIX buffers.
	nsAutoCorr    []int32 // maxShapeLpcOrder+1 autocorrelation
	nsReflCoefQ16 []int32 // maxShapeLpcOrder reflection coefficients
	nsArQ24       []int32 // maxShapeLpcOrder AR coefficients
	nsXWindowed   []int16 // shapeWinLength windowed signal

	// silkVADGetSAQ8 decimation buffer.
	vadX []int16 // xOffset[3]+frameLength/2 decimated bands

	// silkResidualEnergyFixed buffer.
	reLpcRes []int16 // (maxNbSubfr/2)*(lpcOrder+subfrLength) LPC residual

	// celtAutocorrFixed pre-scaled input.
	acXX []int16 // n pre-scaled input (only used when shift > 0)

	// silkPitchAnalysisCoreFixed buffers.
	paFrameScaled  []int16                 // frameLength downscaled input
	paFrame8kHzBuf []int16                 // frameLength8kHz resampled input
	paResScratch   []int32                 // frameLength+4 resampler scratch (12 kHz)
	paFrame4kHz    []int16                 // frameLength4kHz decimated input
	paC            []int16                 // nbSubfr*peCStride8kHz correlations
	paXcorr32      []int32                 // peMaxLag4kHz-peMinLag4kHz+1 cross-correlation
	paDSrch        []int                   // peDSrchLength search lags
	paDComp        []int16                 // peDCompStride lag presence map
	paEnergiesSt3  [][peNbStage3Lags]int32 // nbSubfr*nbCbkSearch stage-3 energies
	paCrossCorrSt3 [][peNbStage3Lags]int32 // nbSubfr*nbCbkSearch stage-3 correlations

	// silkNSQFixed / silkNSQDelDecFixed working buffers.
	nsqSLTPQ15 []int32               // ltpMemLength+frameLength LTP state (Q15)
	nsqSLTP    []int16               // ltpMemLength+frameLength LTP state
	nsqXScQ10  []int32               // subfrLength scaled input
	nsqDelDec  []nsqDelDecStateFixed // nStatesDelayedDecision survivor states

	// silkNoiseShapeQuantizerDelDecFixed per-sample survivor scratch.
	nsqSampleState []nsqSamplePairFixed // nStatesDelayedDecision sample-state pairs
}

// scratchBuf lazily allocates the integer encoder's reusable encode scratch.
func (st *silkFixedEncodeState) scratchBuf() *silkFixedEncodeScratch {
	if st.scratch == nil {
		st.scratch = &silkFixedEncodeScratch{}
	}
	return st.scratch
}

// fixedScratch returns the encoder-owned reusable integer encode scratch,
// lazily creating the integer state if it has not been set up yet (the isolated
// per-kernel oracle tests build a bare Encoder). The production encode path
// always has e.fixed established by ensureFixedState before this is reached.
func (e *Encoder) fixedScratch() *silkFixedEncodeScratch {
	if e.fixed == nil {
		e.fixed = &silkFixedEncodeState{}
	}
	return e.fixed.scratchBuf()
}

func ensureIntSlice(buf *[]int, n int) []int {
	if cap(*buf) < n {
		*buf = make([]int, n)
	} else {
		*buf = (*buf)[:n]
	}
	return *buf
}

func ensureStage3LagSlice(buf *[][peNbStage3Lags]int32, n int) [][peNbStage3Lags]int32 {
	if cap(*buf) < n {
		*buf = make([][peNbStage3Lags]int32, n)
	} else {
		*buf = (*buf)[:n]
	}
	return *buf
}

func ensureNSQDelDecSlice(buf *[]nsqDelDecStateFixed, n int) []nsqDelDecStateFixed {
	if cap(*buf) < n {
		*buf = make([]nsqDelDecStateFixed, n)
	} else {
		*buf = (*buf)[:n]
	}
	return *buf
}

func ensureNSQSamplePairSlice(buf *[]nsqSamplePairFixed, n int) []nsqSamplePairFixed {
	if cap(*buf) < n {
		*buf = make([]nsqSamplePairFixed, n)
	} else {
		*buf = (*buf)[:n]
	}
	return *buf
}
