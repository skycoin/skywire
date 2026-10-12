//go:build amd64 && goexperiment.simd && !nosimd && !purego

package silk

import (
	"math/bits"
	"simd/archsimd"
	"unsafe"
)

// nsqDelDecAVX2State is NSQ_del_dec_struct of libopus
// silk/x86/NSQ_del_dec_avx2.c: the delayed-decision states in
// structure-of-arrays form with one vector lane per state.
//
// Every per-state row holds state k at index 2k, the low half of 64-bit lane
// k. Int32x8.MulWidenEven reads exactly those halves, so a row multiplies by a
// coefficient without a sign-extension step, and every per-state value stays
// in a 256-bit register.
type nsqDelDecAVX2State struct {
	sLPCQ14 [maxSubFrameLength + nsqLpcBufLength][8]int32
	sAR2Q14 [maxShapeLpcOrder][8]int32

	lfARQ14  [8]int32
	diffQ14  [8]int32
	seed     [8]int32
	seedInit [8]int32
	rdQ10    [8]int32

	// ring holds the sample history rows (NSQ_del_dec_sample_struct). A
	// state replacement does not move the rows: ringLane[r][k] holds the row
	// index 2p of the lane p of row r that carries state k's history, and
	// the replacement rewrites these maps with one byte shuffle per eight
	// rows. Every map store covers a whole 32-byte block of eight rows, so
	// later map loads always forward from a single store.
	ring     nsqDelDecAVX2Ring
	ringLane [decisionDelay][maxDelDecStates]uint8

	// Subframe coefficients broadcast as c<<16: the high half of the
	// MulWidenEven product of x and c<<16 is silk_SMULWB(x, c).
	aQ12  [maxLPCOrder][8]int32
	arQ13 [maxShapeLpcOrder][8]int32
}

// nsqDelDecAVX2Ring holds the per-sample history of every state, one state
// per row lane.
type nsqDelDecAVX2Ring struct {
	randState [decisionDelay][8]int32
	qQ10      [decisionDelay][8]int32
	xqQ14     [decisionDelay][8]int32
	predQ15   [decisionDelay][8]int32
	shapeQ14  [decisionDelay][8]int32
}

// nsqDelDecAVX2LaneIdentity is a ringLane row that maps every state to its
// own lane.
var nsqDelDecAVX2LaneIdentity = [maxDelDecStates]uint8{0, 2, 4, 6}

// nsqDelDecAVX2RowReset holds, for each row of an eight-row ringLane block,
// the byte mask of that row and the identity map masked to it.
var nsqDelDecAVX2RowReset = func() (t [8]struct{ mask, identity [32]uint8 }) {
	for r := range t {
		for k := range maxDelDecStates {
			t[r].mask[4*r+k] = 0xFF
			t[r].identity[4*r+k] = nsqDelDecAVX2LaneIdentity[k]
		}
	}
	return t
}()

// nsqDelDecAVX2Consts holds the loop-invariant constants of the quantizer
// already broadcast to every lane. quantizeSubframe loads them from memory: a
// register broadcast builds each one from a 128-bit temporary, and the
// compiler spills and reloads spare temporaries with legacy SSE moves, each of
// which costs an SSE/AVX state transition while the 256-bit lanes are live.
var nsqDelDecAVX2Consts = struct {
	one, lowHalf, int32Max, randMultiplier, randIncrement, rLimitHi, rLimitLo,
	levelAdjust, stepNearZero, step, expiredPenalty [8]int32
	// laneIDs numbers the state of each lane; the odd lanes carry no state.
	laneIDs  [8]int32
	identity [8]uint32
}{
	laneIDs:        [8]int32{0, -1, 1, -1, 2, -1, 3, -1},
	identity:       [8]uint32{0, 1, 2, 3, 4, 5, 6, 7},
	one:            nsqDelDecAVX2Splat(1),
	lowHalf:        nsqDelDecAVX2Splat(0xFFFF),
	int32Max:       nsqDelDecAVX2Splat(silk_int32_MAX),
	randMultiplier: nsqDelDecAVX2Splat(196314165),
	randIncrement:  nsqDelDecAVX2Splat(907633515),
	rLimitHi:       nsqDelDecAVX2Splat(30 << 10),
	rLimitLo:       nsqDelDecAVX2Splat(-(31 << 10)),
	levelAdjust:    nsqDelDecAVX2Splat(quantLevelAdjQ10),
	stepNearZero:   nsqDelDecAVX2Splat(1024 - quantLevelAdjQ10),
	step:           nsqDelDecAVX2Splat(1024),
	expiredPenalty: nsqDelDecAVX2Splat(silk_int32_MAX >> 4),
}

func nsqDelDecAVX2Splat(v int32) [8]int32 {
	return [8]int32{v, v, v, v, v, v, v, v}
}

// nsqDelDecAVX2Tables holds the lane selections of the state replacement:
// padPerm[d][s] copies state lane s into lane d and keeps the other lanes
// (Int32x8 Permute), laneBcast[s] copies state lane s into every lane,
// mapPerm[d][s] makes state d take state s's entry in every ringLane row of a
// 32-byte block (byte shuffle), and laneID[d] holds d in every lane.
var nsqDelDecAVX2Tables = func() (t struct {
	padPerm   [maxDelDecStates][maxDelDecStates][8]uint32
	laneBcast [maxDelDecStates][8]uint32
	mapPerm   [maxDelDecStates][maxDelDecStates][32]int8
	laneID    [maxDelDecStates][8]int32
}) {
	for d := range maxDelDecStates {
		for s := range maxDelDecStates {
			for i := range 8 {
				t.padPerm[d][s][i] = uint32(i)
			}
			t.padPerm[d][s][2*d] = uint32(2 * s)
			t.padPerm[d][s][2*d+1] = uint32(2*s + 1)
			for b := range 32 {
				src := b
				if b&3 == d {
					src = b&^3 | s
				}
				// VPSHUFB indexes within each 16-byte half.
				t.mapPerm[d][s][b] = int8(src & 15)
			}
		}
		for i := range 8 {
			t.laneBcast[d][i] = uint32(2*d + i&1)
			t.laneID[d][i] = int32(d)
		}
	}
	return t
}()

// nsqDelDecAVX2Supports reports whether the frame fits
// silk_NSQ_del_dec_avx2: prediction orders up to MAX_LPC_ORDER and even
// shaping orders up to MAX_SHAPE_LPC_ORDER.
func nsqDelDecAVX2Supports(params *NSQParams) bool {
	return params.PredLPCOrder > 0 && params.PredLPCOrder <= maxLPCOrder &&
		params.ShapeLPCOrder > 0 && params.ShapeLPCOrder <= maxShapeLpcOrder && params.ShapeLPCOrder&1 == 0
}

// noiseShapeQuantizeDelDecAVX2 runs the frame on the structure-of-arrays
// states (silk_NSQ_del_dec_avx2) and returns the winner's seed. It produces
// the same pulses, xq and NSQ state as noiseShapeQuantizeDelDecStates.
//
//go:noinline
func noiseShapeQuantizeDelDecAVX2(nsq *NSQState, input []int16, params *NSQParams, f *nsqDelDecFrame) int {
	st := &nsq.delDecAVX2
	subfrLength := params.SubfrLength
	ltpMemLength := params.LTPMemLength
	nStates := f.nStates

	st.ring = nsqDelDecAVX2Ring{}
	for r := range st.ringLane {
		st.ringLane[r] = nsqDelDecAVX2LaneIdentity
	}
	if ltpMemLength-1 >= 0 && ltpMemLength-1 < len(nsq.sLTPShpQ14) {
		st.ring.shapeQ14[0] = nsqDelDecAVX2Row(nsq.sLTPShpQ14[ltpMemLength-1])
	}
	st.rdQ10 = [8]int32{}
	st.lfARQ14 = nsqDelDecAVX2Row(nsq.sLFARShpQ14)
	st.diffQ14 = nsqDelDecAVX2Row(nsq.sDiffShpQ14)
	for s := range maxDelDecStates {
		st.seed[2*s] = int32((s + params.Seed) & 3)
	}
	st.seedInit = st.seed
	for i := range nsqLpcBufLength {
		st.sLPCQ14[i] = nsqDelDecAVX2Row(nsq.sLPCQ14[i])
	}
	for i := range maxShapeLpcOrder {
		st.sAR2Q14[i] = nsqDelDecAVX2Row(nsq.sAR2Q14[i])
	}

	lag := int(nsq.lagPrev)
	offsetQ10 := int32(getQuantizationOffset(params.SignalType, params.QuantOffsetType))
	var delayedGainQ10 [decisionDelay]int32
	smplBufIdx := 0

	nsq.sLTPShpBufIdx = ltpMemLength
	nsq.sLTPBufIdx = ltpMemLength

	subfr := 0
	frameOffset := 0
	for k := range params.NbSubfr {
		aQ12 := params.PredCoefQ12[((k>>1)|(1-f.lsfInterpFlag))*maxLPCOrder:]
		bQ14 := params.LTPCoefQ14[k*ltpOrderConst:]
		arShpQ13 := params.ARShpQ13[k*maxShapeLpcOrder:]

		harmShapeFIRPackedQ14 := silk_RSHIFT(params.HarmShapeGainQ14[k], 2)
		harmShapeFIRPackedQ14 |= silk_LSHIFT32(silk_RSHIFT(params.HarmShapeGainQ14[k], 1), 16)

		nsq.rewhiteFlag = 0
		if params.SignalType == typeVoiced {
			lag = int(params.PitchL[k])
			if (k & (3 - (f.lsfInterpFlag << 1))) == 0 {
				if k == 2 {
					// RESET DELAYED DECISIONS
					winner := st.winner(nStates)
					for s := range nStates {
						if s != winner {
							st.rdQ10[2*s] += silk_int32_MAX >> 4
						}
					}
					var ring nsqDelDecRing
					st.stateRing(winner, &ring)
					nsqDelDecFlushWinner(nsq, f, &ring, smplBufIdx, frameOffset, nsqDelDecResetGainQ16(params), 14)
					subfr = 0
				}
				nsqDelDecRewhiten(nsq, params, f, k, lag, aQ12)
			}
		}

		if gainAdjQ16, changed := nsqDelDecScaleShared(nsq, input[frameOffset:frameOffset+subfrLength], f, k, params); changed {
			st.scale(gainAdjQ16)
		}

		st.quantizeSubframe(nsq, f, params.SignalType, aQ12, bQ14, arShpQ13, lag, harmShapeFIRPackedQ14,
			params.TiltQ14[k], params.LFShpQ14[k], params.GainsQ16[k], params.LambdaQ10, offsetQ10, subfr, subfrLength,
			params.ShapeLPCOrder, params.PredLPCOrder, params.WarpingQ16, &smplBufIdx, frameOffset, &delayedGainQ10)

		frameOffset += subfrLength
		subfr++
	}

	winner := st.winner(nStates)
	var ring nsqDelDecRing
	st.stateRing(winner, &ring)
	nsqDelDecFlushWinner(nsq, f, &ring, smplBufIdx, params.FrameLength, params.GainsQ16[params.NbSubfr-1]>>6, 8)
	for i := range nsqLpcBufLength {
		nsq.sLPCQ14[i] = st.sLPCQ14[subfrLength+i][2*winner]
	}
	for i := range maxShapeLpcOrder {
		nsq.sAR2Q14[i] = st.sAR2Q14[i][2*winner]
	}
	nsq.sLFARShpQ14 = st.lfARQ14[2*winner]
	nsq.sDiffShpQ14 = st.diffQ14[2*winner]
	return int(st.seedInit[2*winner])
}

// nsqDelDecAVX2Row broadcasts v to the four state lanes of a row.
func nsqDelDecAVX2Row(v int32) [8]int32 {
	return [8]int32{v, 0, v, 0, v, 0, v, 0}
}

// winner returns the first of the nStates states with the lowest RD_Q10.
func (st *nsqDelDecAVX2State) winner(nStates int) int {
	winner := 0
	for s := 1; s < nStates; s++ {
		if st.rdQ10[2*s] < st.rdQ10[2*winner] {
			winner = s
		}
	}
	return winner
}

// scale applies gain_adj_Q16 to every state lane (the state loops of
// silk_nsq_del_dec_scale_states_avx2).
func (st *nsqDelDecAVX2State) scale(gainAdjQ16 int32) {
	for s := range maxDelDecStates {
		st.lfARQ14[2*s] = silk_SMULWW(gainAdjQ16, st.lfARQ14[2*s])
		st.diffQ14[2*s] = silk_SMULWW(gainAdjQ16, st.diffQ14[2*s])
		for i := range nsqLpcBufLength {
			st.sLPCQ14[i][2*s] = silk_SMULWW(gainAdjQ16, st.sLPCQ14[i][2*s])
		}
		for i := range maxShapeLpcOrder {
			st.sAR2Q14[i][2*s] = silk_SMULWW(gainAdjQ16, st.sAR2Q14[i][2*s])
		}
		for i := range decisionDelay {
			st.ring.predQ15[i][2*s] = silk_SMULWW(gainAdjQ16, st.ring.predQ15[i][2*s])
			st.ring.shapeQ14[i][2*s] = silk_SMULWW(gainAdjQ16, st.ring.shapeQ14[i][2*s])
		}
	}
}

// stateRing copies state k's history from the ring rows into ring.
func (st *nsqDelDecAVX2State) stateRing(k int, ring *nsqDelDecRing) {
	for r := range decisionDelay {
		lane := st.ringLane[r][k]
		ring.randState[r] = st.ring.randState[r][lane]
		ring.qQ10[r] = st.ring.qQ10[r][lane]
		ring.xqQ14[r] = st.ring.xqQ14[r][lane]
		ring.predQ15[r] = st.ring.predQ15[r][lane]
		ring.shapeQ14[r] = st.ring.shapeQ14[r][lane]
	}
}

// nsqHighHalves moves the high 32 bits of each 64-bit lane of p into its
// state lane.
func nsqHighHalves(p archsimd.Int64x4) archsimd.Int32x8 {
	return p.AsUint64x4().ShiftAllRight(32).AsInt32x8()
}

// nsqSMULWB is silk_mm_smulwb_epi32: silk_SMULWB of each state lane of x
// with the coefficient c, broadcast as c<<16.
func nsqSMULWB(x, c archsimd.Int32x8) archsimd.Int32x8 {
	return nsqHighHalves(x.MulWidenEven(c))
}

// nsqAddSat is silk_ADD_SAT32 on each lane; int32Max holds silk_int32_MAX
// in every lane.
func nsqAddSat(a, b, int32Max archsimd.Int32x8) archsimd.Int32x8 {
	r := a.Add(b)
	overflow := a.Xor(r).And(b.Xor(r)).ShiftAllRight(31)
	sat := a.AsUint32x8().ShiftAllRight(31).AsInt32x8().Add(int32Max)
	return r.Xor(r.Xor(sat).And(overflow))
}

// nsqSubSat is silk_SUB_SAT32 on each lane; int32Max holds silk_int32_MAX
// in every lane.
func nsqSubSat(a, b, int32Max archsimd.Int32x8) archsimd.Int32x8 {
	r := a.Sub(b)
	overflow := a.Xor(b).And(a.Xor(r)).ShiftAllRight(31)
	sat := a.AsUint32x8().ShiftAllRight(31).AsInt32x8().Add(int32Max)
	return r.Xor(r.Xor(sat).And(overflow))
}

// nsqRShiftRound is silk_RSHIFT_ROUND(a, shift) on each lane, shift >= 2;
// one holds 1 in every lane.
func nsqRShiftRound(a archsimd.Int32x8, shift uint64, one archsimd.Int32x8) archsimd.Int32x8 {
	return a.ShiftAllRight(shift - 1).Add(one).ShiftAllRight(1)
}

// nsqHMin returns the minimum of the state lanes of v in every state lane.
// The odd lanes of v must not be below that minimum.
func nsqHMin(v archsimd.Int32x8, swapHalves archsimd.Uint32x8) archsimd.Int32x8 {
	v = v.Min(v.Permute(swapHalves))
	return v.Min(v.PermuteScalarsGrouped(2, 3, 0, 1))
}

// nsqHMax returns the maximum of the state lanes of v in every state lane.
// The odd lanes of v must not be above that maximum.
func nsqHMax(v archsimd.Int32x8, swapHalves archsimd.Uint32x8) archsimd.Int32x8 {
	v = v.Max(v.Permute(swapHalves))
	return v.Max(v.PermuteScalarsGrouped(2, 3, 0, 1))
}

// nsqFirstEqual returns the first state lane of v equal to m, which holds
// the value in every state lane. The 0x55 mask drops the odd lanes.
func nsqFirstEqual(v, m archsimd.Int32x8) int {
	return (bits.TrailingZeros8(v.Equal(m).ToBits()&0x55) >> 1) & (maxDelDecStates - 1)
}

// nsqWarpSection runs allpass section j of the noise shape feedback: it
// stores tmp[j], accumulates its AR_shp_Q13[j] term and advances the chain
// by one row (see quantizeSubframe).
func nsqWarpSection(sAR, arQ13 *[maxShapeLpcOrder][8]int32, j int, perm archsimd.Uint32x8, warp, prev, cur, x, tmp, acc archsimd.Int32x8) (archsimd.Int32x8, archsimd.Int32x8, archsimd.Int32x8, archsimd.Int32x8, archsimd.Int32x8) {
	next := archsimd.LoadInt32x8Array(&sAR[j+2]).Permute(perm)
	tmp.StoreArray(&sAR[j])
	acc = acc.Add(tmp.MulWidenEven(archsimd.LoadInt32x8Array(&arQ13[j])).AsInt32x8())
	sj := nsqSMULWB(x, warp)
	return cur, next, next.Sub(prev).Sub(sj), prev.Add(sj), acc
}

// nsqPermuteRows applies perm to the live sLPC_Q14 rows of a state
// replacement.
func nsqPermuteRows(rows *[nsqLpcBufLength - 1][8]int32, perm archsimd.Uint32x8) {
	for t := range rows {
		archsimd.LoadInt32x8Array(&rows[t]).Permute(perm).StoreArray(&rows[t])
	}
}

// nsqResetRingLane maps every state of ringLane row r to its own lane,
// rewriting the row's whole 32-byte block.
func nsqResetRingLane(maps *[decisionDelay][maxDelDecStates]uint8, r int) {
	block := (*[32]uint8)(unsafe.Pointer(&maps[r&^7]))
	reset := &nsqDelDecAVX2RowReset[r&7]
	archsimd.LoadUint8x32Array(block).AndNot(archsimd.LoadUint8x32Array(&reset.mask)).
		Or(archsimd.LoadUint8x32Array(&reset.identity)).StoreArray(block)
}

// nsqRemapRing makes state dst take state src's history in every ringLane
// row.
func nsqRemapRing(maps *[decisionDelay][maxDelDecStates]uint8, perm archsimd.Int8x32) {
	blocks := (*[decisionDelay / 8][32]uint8)(unsafe.Pointer(maps))
	for i := range blocks {
		archsimd.LoadUint8x32Array(&blocks[i]).PermuteOrZeroGrouped(perm).StoreArray(&blocks[i])
	}
}

// quantizeSubframe is silk_noise_shape_quantizer_del_dec_avx2 for one
// subframe: every state advances as one vector lane. Lanes at and above
// nStates compute unused values that the masked winner and replacement
// searches never select.
//
//go:noinline
func (st *nsqDelDecAVX2State) quantizeSubframe(
	nsq *NSQState,
	f *nsqDelDecFrame,
	signalType int,
	aQ12, bQ14, arShpQ13 []int16,
	lag int,
	harmShapeFIRPackedQ14 int32,
	tiltQ14, lfShpQ14, gainQ16, lambdaQ10, offsetQ10 int32,
	subfr, length, shapingLPCOrder, predictLPCOrder, warpingQ16 int,
	smplBufIdx *int,
	frameOffset int,
	delayedGainQ10 *[decisionDelay]int32,
) {
	xQ10 := f.xScQ10[:length]
	pulses := f.pulses
	xq := f.pxq
	sLTPQ15 := f.sLTPQ15
	decDelay := f.decDelay
	bQ14 = bQ14[:ltpOrderConst:ltpOrderConst]
	aQ12 = aQ12[:predictLPCOrder]
	arShpQ13 = arShpQ13[:shapingLPCOrder]
	for i, c := range aQ12 {
		archsimd.BroadcastInt32x8(int32(c) << 16).StoreArray(&st.aQ12[i])
	}
	for i, c := range arShpQ13 {
		archsimd.BroadcastInt32x8(int32(c) << 16).StoreArray(&st.arQ13[i])
	}

	shpLagPtrIdx := nsq.sLTPShpBufIdx - lag + harmShapeFirTaps/2
	predLagPtrIdx := nsq.sLTPBufIdx - lag + ltpOrderConst/2
	gainQ10 := gainQ16 >> 6
	localSmplBufIdx := *smplBufIdx
	localShpBufIdx := nsq.sLTPShpBufIdx
	localLTPBufIdx := nsq.sLTPBufIdx

	c := &nsqDelDecAVX2Consts
	laneIDs := archsimd.LoadInt32x8Array(&c.laneIDs)
	valid := archsimd.BroadcastInt32x8(int32(f.nStates)).Greater(laneIDs).
		And(laneIDs.Greater(archsimd.BroadcastInt32x8(-1)))
	// minFill and maxFill turn every lane without a state into the identity
	// of the masked minimum and maximum searches (silk_mm_mask_hmin_epi32).
	minFill := archsimd.BroadcastInt32x8(silkInt32Min).IfElse(valid, archsimd.BroadcastInt32x8(silk_int32_MAX))
	maxFill := archsimd.BroadcastInt32x8(silk_int32_MAX).IfElse(valid, archsimd.BroadcastInt32x8(silkInt32Min))
	swapHalves := archsimd.LoadUint32x8Array(&[8]uint32{4, 5, 6, 7, 0, 1, 2, 3})

	one := archsimd.LoadInt32x8Array(&c.one)
	zero := archsimd.Int32x8{}
	warp := archsimd.BroadcastInt32x8(int32(int16(warpingQ16)) << 16)
	tilt := archsimd.BroadcastInt32x8(int32(int16(tiltQ14)) << 16)
	lfShpLo := archsimd.BroadcastInt32x8(int32(int16(lfShpQ14)) << 16)
	lfShpHi := archsimd.BroadcastInt32x8((lfShpQ14 >> 16) << 16)
	offset := archsimd.BroadcastInt32x8(offsetQ10)
	lambda := archsimd.BroadcastInt32x8(int32(uint16(lambdaQ10))).AsInt16x16()
	lowHalf := archsimd.LoadInt32x8Array(&c.lowHalf)
	useRDO := lambdaQ10 > 2048
	rdoOffset := archsimd.BroadcastInt32x8(lambdaQ10/2 - 512)
	shapeHalf := archsimd.BroadcastInt32x8(int32(shapingLPCOrder >> 1))
	predHalf := archsimd.BroadcastInt32x8(int32(predictLPCOrder >> 1))
	int32Max := archsimd.LoadInt32x8Array(&c.int32Max)
	randMultiplier := archsimd.LoadInt32x8Array(&c.randMultiplier)
	randIncrement := archsimd.LoadInt32x8Array(&c.randIncrement)
	rLimitHi := archsimd.LoadInt32x8Array(&c.rLimitHi)
	rLimitLo := archsimd.LoadInt32x8Array(&c.rLimitLo)
	levelAdjust := archsimd.LoadInt32x8Array(&c.levelAdjust)
	stepNearZero := archsimd.LoadInt32x8Array(&c.stepNearZero)
	step := archsimd.LoadInt32x8Array(&c.step)
	expiredPenalty := archsimd.LoadInt32x8Array(&c.expiredPenalty)

	seed := archsimd.LoadInt32x8Array(&st.seed)
	seedInit := archsimd.LoadInt32x8Array(&st.seedInit)
	rd := archsimd.LoadInt32x8Array(&st.rdQ10)
	lfAR := archsimd.LoadInt32x8Array(&st.lfARQ14)
	diff := archsimd.LoadInt32x8Array(&st.diffQ14)

	sAR := &st.sAR2Q14
	arQ13 := &st.arQ13
	identity := archsimd.LoadUint32x8Array(&c.identity)
	sARPerm := identity
	for i := range length {
		var ltpPredQ14 int32
		if signalType == typeVoiced {
			ltpPredQ14 = 2
			if predLagPtrIdx >= ltpOrderConst-1 && predLagPtrIdx < len(sLTPQ15) {
				ltpPredQ14 = silk_SMLAWB(ltpPredQ14, sLTPQ15[predLagPtrIdx-0], int32(bQ14[0]))
				ltpPredQ14 = silk_SMLAWB(ltpPredQ14, sLTPQ15[predLagPtrIdx-1], int32(bQ14[1]))
				ltpPredQ14 = silk_SMLAWB(ltpPredQ14, sLTPQ15[predLagPtrIdx-2], int32(bQ14[2]))
				ltpPredQ14 = silk_SMLAWB(ltpPredQ14, sLTPQ15[predLagPtrIdx-3], int32(bQ14[3]))
				ltpPredQ14 = silk_SMLAWB(ltpPredQ14, sLTPQ15[predLagPtrIdx-4], int32(bQ14[4]))
			} else {
				for tap := range ltpOrderConst {
					if idx := predLagPtrIdx - tap; idx >= 0 && idx < len(sLTPQ15) {
						ltpPredQ14 = silk_SMLAWB(ltpPredQ14, sLTPQ15[idx], int32(bQ14[tap]))
					}
				}
			}
			ltpPredQ14 <<= 1
			predLagPtrIdx++
		}

		var nLTPQ14 int32
		if lag > 0 {
			var shp0, shp1, shp2 int32
			if shpLagPtrIdx >= harmShapeFirTaps-1 && shpLagPtrIdx < len(nsq.sLTPShpQ14) {
				shp0 = nsq.sLTPShpQ14[shpLagPtrIdx]
				shp1 = nsq.sLTPShpQ14[shpLagPtrIdx-1]
				shp2 = nsq.sLTPShpQ14[shpLagPtrIdx-2]
			} else {
				if shpLagPtrIdx >= 0 && shpLagPtrIdx < len(nsq.sLTPShpQ14) {
					shp0 = nsq.sLTPShpQ14[shpLagPtrIdx]
				}
				if shpLagPtrIdx >= 1 && shpLagPtrIdx-1 < len(nsq.sLTPShpQ14) {
					shp1 = nsq.sLTPShpQ14[shpLagPtrIdx-1]
				}
				if shpLagPtrIdx >= 2 && shpLagPtrIdx-2 < len(nsq.sLTPShpQ14) {
					shp2 = nsq.sLTPShpQ14[shpLagPtrIdx-2]
				}
			}
			nLTPQ14 = silk_SMULWB(silk_ADD_SAT32(shp0, shp2), harmShapeFIRPackedQ14)
			nLTPQ14 = silk_SMLAWT(nLTPQ14, shp1, harmShapeFIRPackedQ14)
			nLTPQ14 = ltpPredQ14 - (nLTPQ14 << 2)
			shpLagPtrIdx++
		}

		// Generate dither
		seed = seed.Mul(randMultiplier).Add(randIncrement)

		// Short-term prediction (silk_noise_shape_quantizer_short_prediction_x4):
		// win holds the 16 newest sLPC_Q14 rows, oldest first, and acc
		// carries order>>1 plus the silk_SMULWB terms in the high half of
		// each 64-bit lane. The loops of this sample body are written out
		// in place: a call would spill every live vector.
		win := (*[nsqLpcBufLength][8]int32)(st.sLPCQ14[i : i+nsqLpcBufLength])
		term := func(t int) archsimd.Int32x8 {
			return archsimd.LoadInt32x8Array(&win[nsqLpcBufLength-1-t]).MulWidenEven(archsimd.LoadInt32x8Array(&st.aQ12[t])).AsInt32x8()
		}
		var acc archsimd.Int32x8
		switch predictLPCOrder {
		case maxLPCOrder:
			acc0 := predHalf.Add(term(0)).Add(term(2)).Add(term(4)).Add(term(6)).Add(term(8)).Add(term(10)).Add(term(12)).Add(term(14))
			acc1 := term(1).Add(term(3)).Add(term(5)).Add(term(7)).Add(term(9)).Add(term(11)).Add(term(13)).Add(term(15))
			acc = acc0.Add(acc1)
		case 10:
			acc0 := predHalf.Add(term(0)).Add(term(2)).Add(term(4)).Add(term(6)).Add(term(8))
			acc1 := term(1).Add(term(3)).Add(term(5)).Add(term(7)).Add(term(9))
			acc = acc0.Add(acc1)
		default:
			acc = predHalf
			for t := range predictLPCOrder {
				acc = acc.Add(term(t))
			}
		}
		lpcPredQ14 := nsqHighHalves(acc.AsInt64x4()).ShiftAllLeft(4)

		// Noise shape feedback: the warped allpass sections. Section j
		// computes tmp[j+1] = a[j] + s[j] with s[j] =
		// silk_SMULWB(a[j+1]-tmp[j], warping) from the old rows a, which
		// the pending replacement permutation sARPerm maps as they load.
		// The chain carries the multiplier operand x[j] = a[j+1]-tmp[j]
		// instead of tmp: x[j+1] = (a[j+2]-a[j]) - s[j] in wrapping int32
		// arithmetic, which leaves one multiply, shift and subtract per
		// section on the serial path.
		prev := archsimd.LoadInt32x8Array(&sAR[0]).Permute(sARPerm)
		cur := archsimd.LoadInt32x8Array(&sAR[1]).Permute(sARPerm)
		tmp := diff.Add(nsqSMULWB(prev, warp))
		x := cur.Sub(tmp)
		acc = shapeHalf
		if shapingLPCOrder == maxShapeLpcOrder {
			// Constant row indices give every load and store a fixed offset.
			prev, cur, x, tmp, acc = nsqWarpSection(sAR, arQ13, 0, sARPerm, warp, prev, cur, x, tmp, acc)
			prev, cur, x, tmp, acc = nsqWarpSection(sAR, arQ13, 1, sARPerm, warp, prev, cur, x, tmp, acc)
			prev, cur, x, tmp, acc = nsqWarpSection(sAR, arQ13, 2, sARPerm, warp, prev, cur, x, tmp, acc)
			prev, cur, x, tmp, acc = nsqWarpSection(sAR, arQ13, 3, sARPerm, warp, prev, cur, x, tmp, acc)
			prev, cur, x, tmp, acc = nsqWarpSection(sAR, arQ13, 4, sARPerm, warp, prev, cur, x, tmp, acc)
			prev, cur, x, tmp, acc = nsqWarpSection(sAR, arQ13, 5, sARPerm, warp, prev, cur, x, tmp, acc)
			prev, cur, x, tmp, acc = nsqWarpSection(sAR, arQ13, 6, sARPerm, warp, prev, cur, x, tmp, acc)
			prev, cur, x, tmp, acc = nsqWarpSection(sAR, arQ13, 7, sARPerm, warp, prev, cur, x, tmp, acc)
			prev, cur, x, tmp, acc = nsqWarpSection(sAR, arQ13, 8, sARPerm, warp, prev, cur, x, tmp, acc)
			prev, cur, x, tmp, acc = nsqWarpSection(sAR, arQ13, 9, sARPerm, warp, prev, cur, x, tmp, acc)
			prev, cur, x, tmp, acc = nsqWarpSection(sAR, arQ13, 10, sARPerm, warp, prev, cur, x, tmp, acc)
			prev, cur, x, tmp, acc = nsqWarpSection(sAR, arQ13, 11, sARPerm, warp, prev, cur, x, tmp, acc)
			prev, cur, x, tmp, acc = nsqWarpSection(sAR, arQ13, 12, sARPerm, warp, prev, cur, x, tmp, acc)
			prev, cur, x, tmp, acc = nsqWarpSection(sAR, arQ13, 13, sARPerm, warp, prev, cur, x, tmp, acc)
			prev, cur, x, tmp, acc = nsqWarpSection(sAR, arQ13, 14, sARPerm, warp, prev, cur, x, tmp, acc)
			prev, cur, x, tmp, acc = nsqWarpSection(sAR, arQ13, 15, sARPerm, warp, prev, cur, x, tmp, acc)
			prev, cur, x, tmp, acc = nsqWarpSection(sAR, arQ13, 16, sARPerm, warp, prev, cur, x, tmp, acc)
			prev, cur, x, tmp, acc = nsqWarpSection(sAR, arQ13, 17, sARPerm, warp, prev, cur, x, tmp, acc)
			prev, cur, x, tmp, acc = nsqWarpSection(sAR, arQ13, 18, sARPerm, warp, prev, cur, x, tmp, acc)
			prev, cur, x, tmp, acc = nsqWarpSection(sAR, arQ13, 19, sARPerm, warp, prev, cur, x, tmp, acc)
			prev, cur, x, tmp, acc = nsqWarpSection(sAR, arQ13, 20, sARPerm, warp, prev, cur, x, tmp, acc)
			prev, cur, x, tmp, acc = nsqWarpSection(sAR, arQ13, 21, sARPerm, warp, prev, cur, x, tmp, acc)
		} else {
			for j := 0; j+2 < shapingLPCOrder; j++ {
				prev, cur, x, tmp, acc = nsqWarpSection(sAR, arQ13, j, sARPerm, warp, prev, cur, x, tmp, acc)
			}
		}
		_ = cur
		tmp.StoreArray(&sAR[shapingLPCOrder-2])
		acc = acc.Add(tmp.MulWidenEven(archsimd.LoadInt32x8Array(&arQ13[shapingLPCOrder-2])).AsInt32x8())
		tmp = prev.Add(nsqSMULWB(x, warp))
		tmp.StoreArray(&sAR[shapingLPCOrder-1])
		acc = acc.Add(tmp.MulWidenEven(archsimd.LoadInt32x8Array(&arQ13[shapingLPCOrder-1])).AsInt32x8())
		sARPerm = identity
		nARQ14 := nsqHighHalves(acc.AsInt64x4()).ShiftAllLeft(1)
		nARQ14 = nARQ14.Add(nsqSMULWB(lfAR, tilt)).ShiftAllLeft(2)

		// The newest row was written after the last replacement, so its lanes
		// are the states' own.
		shape := archsimd.LoadInt32x8Array(&st.ring.shapeQ14[localSmplBufIdx])
		nLFQ14 := nsqSMULWB(shape, lfShpLo).Add(nsqSMULWB(lfAR, lfShpHi)).ShiftAllLeft(2)

		// Input minus prediction plus noise feedback
		tmpA := nsqAddSat(nARQ14, nLFQ14, int32Max)
		tmpB := lpcPredQ14.Add(archsimd.BroadcastInt32x8(nLTPQ14))
		tmpA = nsqRShiftRound(nsqSubSat(tmpB, tmpA, int32Max), 4, one)
		xBcast := archsimd.BroadcastInt32x8(xQ10[i])
		rQ10 := xBcast.Sub(tmpA)

		// Flip sign depending on dither
		sign := seed.Or(one)
		rQ10 = rQ10.MulSign(sign)
		rQ10 = rQ10.Min(rLimitHi).Max(rLimitLo)

		// Find two quantization level candidates and measure their
		// rate-distortion
		q1Q10 := rQ10.Sub(offset)
		q1Q0 := q1Q10.ShiftAllRight(10)
		if useRDO {
			t := q1Q10.Abs().Sub(rdoOffset)
			q1Q0 = t.MulSign(q1Q10.Or(one)).ShiftAllRight(10).IfElse(t.Greater(zero), q1Q10.ShiftAllRight(31))
		}
		q1Q10 = q1Q0.ShiftAllLeft(10).Sub(levelAdjust.MulSign(q1Q0)).Add(offset)
		zeroOrMinus1 := q1Q0.Add(q1Q0.AsUint32x8().ShiftAllRight(31).AsInt32x8()).Equal(zero)
		q2Q10 := q1Q10.Add(stepNearZero.IfElse(zeroOrMinus1, step))

		// silk_SMULBB through VPMADDWD: the high 16 bits of lambda and of one
		// rr operand are zero, so each lane is the product of the low halves.
		rr1 := rQ10.Sub(q1Q10)
		rr2 := rQ10.Sub(q2Q10)
		rd1 := q1Q10.Abs().AsInt16x16().DotProductPairs(lambda).Add(rr1.AsInt16x16().DotProductPairs(rr1.And(lowHalf).AsInt16x16())).ShiftAllRight(10)
		rd2 := q2Q10.Abs().AsInt16x16().DotProductPairs(lambda).Add(rr2.AsInt16x16().DotProductPairs(rr2.And(lowHalf).AsInt16x16())).ShiftAllRight(10)

		firstIsQ1 := rd2.Greater(rd1)
		ss0Q := q1Q10.IfElse(firstIsQ1, q2Q10)
		ss1Q := q2Q10.IfElse(firstIsQ1, q1Q10)
		ss0RD := rd.Add(rd1.Min(rd2))
		ss1RD := rd.Add(rd1.Max(rd2))

		// Update states for the best quantization
		ltpPred := archsimd.BroadcastInt32x8(ltpPredQ14)
		xQ10Q14 := xBcast.ShiftAllLeft(4)
		ss0Exc := ss0Q.ShiftAllLeft(4).MulSign(sign).Add(ltpPred)
		ss0Xq := ss0Exc.Add(lpcPredQ14)
		ss0Diff := ss0Xq.Sub(xQ10Q14)
		ss0LFAR := ss0Diff.Sub(nARQ14)
		ss0Shp := nsqSubSat(ss0LFAR, nLFQ14, int32Max)

		localSmplBufIdx--
		if localSmplBufIdx < 0 {
			localSmplBufIdx = decisionDelay - 1
		}
		lastSmplIdx := localSmplBufIdx + decDelay
		if lastSmplIdx >= decisionDelay {
			lastSmplIdx -= decisionDelay
		}

		// Find winner
		rdMasked := ss0RD.Max(minFill)
		winner := nsqFirstEqual(rdMasked, nsqHMin(rdMasked, swapHalves))

		// Increase RD values of expired states
		lastLanes := &st.ringLane[lastSmplIdx]
		// Each map byte widens to the low half of a 64-bit lane: the row
		// index of the state's lane.
		lastPerm := archsimd.BroadcastUint32x4(*(*uint32)(unsafe.Pointer(lastLanes))).AsUint8x16().ExtendLo4ToUint64().AsUint32x8()
		rand := archsimd.LoadInt32x8Array(&st.ring.randState[lastSmplIdx]).Permute(lastPerm)
		winnerLane := lastLanes[winner]
		sameRand := rand.Equal(archsimd.BroadcastInt32x8(st.ring.randState[lastSmplIdx][winnerLane&7]))
		penalty := expiredPenalty.AndNot(sameRand.ToInt32x8())
		ss0RD = ss0RD.Add(penalty)
		ss1RD = ss1RD.Add(penalty)

		// Replace a state if the best of the second set outperforms the
		// worst of the first set
		rdMaxMasked := ss0RD.Min(maxFill)
		rdMin2Masked := ss1RD.Max(minFill)
		rdMaxV := nsqHMax(rdMaxMasked, swapHalves)
		rdMin2V := nsqHMin(rdMin2Masked, swapHalves)
		if rdMaxV.Greater(rdMin2V).ToBits()&1 != 0 {
			dst := nsqFirstEqual(rdMaxMasked, rdMaxV)
			src := nsqFirstEqual(rdMin2Masked, rdMin2V)
			padPerm := archsimd.LoadUint32x8Array(&nsqDelDecAVX2Tables.padPerm[dst][src])
			nsqPermuteRows((*[nsqLpcBufLength - 1][8]int32)(st.sLPCQ14[i+1:i+nsqLpcBufLength]), padPerm)
			// The next sample's allpass chain applies the permutation to the
			// sAR2 rows as it loads them.
			sARPerm = padPerm
			seed = seed.Permute(padPerm)
			seedInit = seedInit.Permute(padPerm)
			nsqRemapRing(&st.ringLane, archsimd.LoadInt8x32Array(&nsqDelDecAVX2Tables.mapPerm[dst][src]))
			winnerLane = lastLanes[winner]

			ss1Exc := ss1Q.ShiftAllLeft(4).MulSign(sign).Add(ltpPred)
			ss1Xq := ss1Exc.Add(lpcPredQ14)
			ss1Diff := ss1Xq.Sub(xQ10Q14)
			ss1LFAR := ss1Diff.Sub(nARQ14)
			ss1Shp := nsqSubSat(ss1LFAR, nLFQ14, int32Max)

			isDst := laneIDs.Equal(archsimd.LoadInt32x8Array(&nsqDelDecAVX2Tables.laneID[dst]))
			bcast := archsimd.LoadUint32x8Array(&nsqDelDecAVX2Tables.laneBcast[src])
			ss0Q = ss1Q.Permute(bcast).IfElse(isDst, ss0Q)
			ss0RD = ss1RD.Permute(bcast).IfElse(isDst, ss0RD)
			ss0Exc = ss1Exc.Permute(bcast).IfElse(isDst, ss0Exc)
			ss0Xq = ss1Xq.Permute(bcast).IfElse(isDst, ss0Xq)
			ss0Diff = ss1Diff.Permute(bcast).IfElse(isDst, ss0Diff)
			ss0LFAR = ss1LFAR.Permute(bcast).IfElse(isDst, ss0LFAR)
			ss0Shp = ss1Shp.Permute(bcast).IfElse(isDst, ss0Shp)
		}

		// Write samples from winner to output and long-term filter states
		if subfr > 0 || i >= decDelay {
			lane := winnerLane & 7
			if outIdx := frameOffset + i - decDelay; outIdx >= 0 && outIdx < len(pulses) {
				pulses[outIdx] = int8(silk_RSHIFT_ROUND(st.ring.qQ10[lastSmplIdx][lane], 10))
				xq[outIdx] = nsqXqQ0Exact(st.ring.xqQ14[lastSmplIdx][lane], delayedGainQ10[lastSmplIdx], 8)
			}
			if shpOutIdx := localShpBufIdx - decDelay; shpOutIdx >= 0 && shpOutIdx < len(nsq.sLTPShpQ14) {
				nsq.sLTPShpQ14[shpOutIdx] = st.ring.shapeQ14[lastSmplIdx][lane]
			}
			if ltpOutIdx := localLTPBufIdx - decDelay; ltpOutIdx >= 0 && ltpOutIdx < len(sLTPQ15) {
				sLTPQ15[ltpOutIdx] = st.ring.predQ15[lastSmplIdx][lane]
			}
		}
		localShpBufIdx++
		localLTPBufIdx++

		// Update states
		seed = seed.Add(nsqRShiftRound(ss0Q, 10, one))
		lfAR = ss0LFAR
		diff = ss0Diff
		rd = ss0RD
		ss0Xq.StoreArray(&st.sLPCQ14[nsqLpcBufLength+i])
		ss0Xq.StoreArray(&st.ring.xqQ14[localSmplBufIdx])
		ss0Q.StoreArray(&st.ring.qQ10[localSmplBufIdx])
		ss0Exc.ShiftAllLeft(1).StoreArray(&st.ring.predQ15[localSmplBufIdx])
		ss0Shp.StoreArray(&st.ring.shapeQ14[localSmplBufIdx])
		seed.StoreArray(&st.ring.randState[localSmplBufIdx])
		nsqResetRingLane(&st.ringLane, localSmplBufIdx)
		delayedGainQ10[localSmplBufIdx] = gainQ10
	}

	nsq.sLTPShpBufIdx = localShpBufIdx
	nsq.sLTPBufIdx = localLTPBufIdx
	*smplBufIdx = localSmplBufIdx

	for t := range arShpQ13 {
		archsimd.LoadInt32x8Array(&sAR[t]).Permute(sARPerm).StoreArray(&sAR[t])
	}
	seed.StoreArray(&st.seed)
	seedInit.StoreArray(&st.seedInit)
	rd.StoreArray(&st.rdQ10)
	lfAR.StoreArray(&st.lfARQ14)
	diff.StoreArray(&st.diffQ14)

	// The 256-bit lanes leave the upper register halves dirty; clear them
	// before the copy and the scalar SSE code that follows, so no legacy SSE
	// instruction runs in the dirty state.
	archsimd.ClearAVXUpperBits()

	// Update LPC states
	copy(st.sLPCQ14[:nsqLpcBufLength], st.sLPCQ14[length:length+nsqLpcBufLength])
}
