//go:build gopus_celt_trace && !gopus_fixed_point

package celt

// CELTQuantBandTraceStage identifies one actual quantization-stage boundary.
type CELTQuantBandTraceStage uint32

const (
	CELTQuantBandTraceTheta       CELTQuantBandTraceStage = 1
	CELTQuantBandTracePVQ         CELTQuantBandTraceStage = 2
	CELTQuantBandTraceStereoMerge CELTQuantBandTraceStage = 3
	CELTQuantBandTraceBandOutput  CELTQuantBandTraceStage = 4
	CELTQuantBandTraceRDOSelect   CELTQuantBandTraceStage = 5
)

const celtQuantBandTraceEnabled = true

const (
	celtQuantBandTraceMaxEvents = 64
	celtQuantBandTraceMaxWidth  = 256
)

// CELTQuantBandTraceSnapshot records an actual encoder quantization boundary
// for the selected band. Vector arrays are valid only during the callback; copy the
// snapshot value to retain it. Metadata follows libopus codec widths.
type CELTQuantBandTraceSnapshot struct {
	Stage          uint32
	Ordinal        uint32
	ThetaOrdinal   uint32
	Band           uint32
	N              uint32
	B              uint32
	B0             uint32
	LM             int32
	Channels       uint32
	Encode         uint32
	Stereo         uint32
	ThetaRound     int32
	RangeBefore    uint32
	RangeAfter     uint32
	TellFracBefore uint32
	TellFracAfter  uint32

	BBefore         int32
	BAfter          int32
	FillBefore      int32
	FillAfter       int32
	QN              int32
	PulseCap        int32
	Offset          int32
	RawIthetaQ30    int32
	Itheta          int32
	IthetaQ30       int32
	Inv             int32
	IMid            int32
	ISide           int32
	Delta           int32
	QAlloc          int32
	RemainingBefore int32
	RemainingAfter  int32
	EnergyL         float32
	EnergyR         float32
	K               int32
	Spread          int32
	Resynth         int32
	Collapse        uint32
	Gain            float32
	Mid             float32
	Dist0           float32
	Dist1           float32
	SelectedRound   int32

	XBefore [celtQuantBandTraceMaxWidth]float32
	YBefore [celtQuantBandTraceMaxWidth]float32
	XAfter  [celtQuantBandTraceMaxWidth]float32
	YAfter  [celtQuantBandTraceMaxWidth]float32
}

// WithCELTQuantBandTraceHookForTesting installs a bounded callback for one
// band's actual encode calls. It is intended for serial tests; callback data
// must be copied during the call. The return value reports event/width overflow.
func WithCELTQuantBandTraceHookForTesting(targetBand int32, hook func(*CELTQuantBandTraceSnapshot), fn func()) (overflow bool) {
	previousHook, previousTarget := celtQuantBandTraceHook, celtQuantBandTraceTarget
	previousOrdinal, previousThetaOrdinal := celtQuantBandTraceOrdinal, celtQuantBandTraceThetaOrdinal
	previousCount, previousOverflow := celtQuantBandTraceCount, celtQuantBandTraceOverflow
	previousHasTheta := celtQuantBandTraceHasTheta
	previousLastTheta, previousLastBandOutput := celtQuantBandTraceLastTheta, celtQuantBandTraceLastBandOut
	celtQuantBandTraceHook = hook
	celtQuantBandTraceTarget = targetBand
	celtQuantBandTraceOrdinal = 0
	celtQuantBandTraceThetaOrdinal = ^uint32(0)
	celtQuantBandTraceCount = 0
	celtQuantBandTraceOverflow = false
	celtQuantBandTraceHasTheta = false
	celtQuantBandTraceLastTheta = quantBandTraceContext{}
	celtQuantBandTraceLastBandOut = quantBandTraceContext{}
	defer func() {
		overflow = celtQuantBandTraceOverflow
		celtQuantBandTraceHook, celtQuantBandTraceTarget = previousHook, previousTarget
		celtQuantBandTraceOrdinal, celtQuantBandTraceThetaOrdinal = previousOrdinal, previousThetaOrdinal
		celtQuantBandTraceCount, celtQuantBandTraceOverflow = previousCount, previousOverflow
		celtQuantBandTraceHasTheta = previousHasTheta
		celtQuantBandTraceLastTheta, celtQuantBandTraceLastBandOut = previousLastTheta, previousLastBandOutput
	}()
	fn()
	return overflow
}

var (
	celtQuantBandTraceHook         func(*CELTQuantBandTraceSnapshot)
	celtQuantBandTraceTarget       int32 = -1
	celtQuantBandTraceOrdinal      uint32
	celtQuantBandTraceThetaOrdinal uint32 = ^uint32(0)
	celtQuantBandTraceCount        uint32
	celtQuantBandTraceOverflow     bool
	celtQuantBandTraceHasTheta     bool
	celtQuantBandTraceLastTheta    quantBandTraceContext
	celtQuantBandTraceLastBandOut  quantBandTraceContext
)

type quantBandTraceState struct {
	event                CELTQuantBandTraceSnapshot
	active               bool
	overrideThetaOrdinal bool
}

type quantBandTraceContext struct {
	band, n, blocks, b0, channels uint32
	lm, thetaRound                int32
	encode, stereo                uint32
	thetaOrdinal                  uint32
}

type quantBandTraceRestorePoint struct {
	thetaOrdinal uint32
	hasTheta     bool
	lastTheta    quantBandTraceContext
}

// Recursive quant_partition siblings resume the enclosing split's local theta
// after a nested child returns. Save that active diagnostic context before
// recursion and restore it afterward; descendants remain in the event stream.
func saveQuantBandTraceContext() quantBandTraceRestorePoint {
	return quantBandTraceRestorePoint{
		thetaOrdinal: celtQuantBandTraceThetaOrdinal,
		hasTheta:     celtQuantBandTraceHasTheta,
		lastTheta:    celtQuantBandTraceLastTheta,
	}
}

func restoreQuantBandTraceContext(state quantBandTraceRestorePoint) {
	celtQuantBandTraceThetaOrdinal = state.thetaOrdinal
	celtQuantBandTraceHasTheta = state.hasTheta
	celtQuantBandTraceLastTheta = state.lastTheta
}

func beginQuantBandTrace(ctx *bandCtx, stage CELTQuantBandTraceStage, n, B, B0, lm int, stereo bool) quantBandTraceState {
	var state quantBandTraceState
	if ctx == nil || !ctx.encode || ctx.band != int(celtQuantBandTraceTarget) || celtQuantBandTraceHook == nil {
		return state
	}
	state.active = true
	e := &state.event
	e.Stage = uint32(stage)
	e.Band = uint32(ctx.band)
	e.N = uint32(n)
	e.B = uint32(B)
	e.B0 = uint32(B0)
	e.LM = int32(lm)
	e.Channels = uint32(ctx.channels)
	e.Encode = 1
	if stereo {
		e.Stereo = 1
	}
	e.ThetaRound = int32(ctx.thetaRound)
	e.ThetaOrdinal = celtQuantBandTraceThetaOrdinal
	e.RangeBefore, e.TellFracBefore = quantBandTraceCoderState(ctx)
	e.RemainingBefore = int32(ctx.remainingBits)
	return state
}

func quantBandTraceCoderState(ctx *bandCtx) (uint32, uint32) {
	if ctx == nil || !ctx.encode || ctx.re == nil {
		return 0, 0
	}
	rng, _ := ctx.re.State()
	return rng, uint32(ctx.re.TellFrac())
}

func quantBandTraceCurrentContext() quantBandTraceContext {
	return celtQuantBandTraceLastTheta
}

func quantBandTraceLastBandOutputContext() quantBandTraceContext {
	return celtQuantBandTraceLastBandOut
}

func applyQuantBandTraceContext(state *quantBandTraceState, context quantBandTraceContext) {
	if state == nil || !state.active {
		return
	}
	e := &state.event
	e.Band = context.band
	e.N = context.n
	e.B = context.blocks
	e.B0 = context.b0
	e.LM = context.lm
	e.Channels = context.channels
	e.Encode = context.encode
	e.Stereo = context.stereo
	e.ThetaRound = context.thetaRound
	e.ThetaOrdinal = context.thetaOrdinal
	state.overrideThetaOrdinal = true
}

func copyQuantBandTraceVector(dst *[celtQuantBandTraceMaxWidth]float32, src []celtNorm, n int) bool {
	if n < 0 || n > len(dst) || n > len(src) {
		return false
	}
	for i := 0; i < n; i++ {
		dst[i] = float32(src[i])
	}
	return true
}

func captureQuantBandTracePair(state *quantBandTraceState, x, y []celtNorm, n int, before bool) {
	if state == nil || !state.active {
		return
	}
	var ok bool
	if before {
		ok = copyQuantBandTraceVector(&state.event.XBefore, x, n)
		if y != nil {
			ok = copyQuantBandTraceVector(&state.event.YBefore, y, n) && ok
		}
	} else {
		ok = copyQuantBandTraceVector(&state.event.XAfter, x, n)
		if y != nil {
			ok = copyQuantBandTraceVector(&state.event.YAfter, y, n) && ok
		}
	}
	if !ok {
		celtQuantBandTraceOverflow = true
	}
}

func emitQuantBandTrace(state *quantBandTraceState, ctx *bandCtx) uint32 {
	if state == nil || !state.active {
		return ^uint32(0)
	}
	if celtQuantBandTraceCount >= celtQuantBandTraceMaxEvents {
		celtQuantBandTraceOverflow = true
		return ^uint32(0)
	}
	e := &state.event
	e.Ordinal = celtQuantBandTraceOrdinal
	if e.Stage == uint32(CELTQuantBandTraceTheta) {
		e.ThetaOrdinal = e.Ordinal
		celtQuantBandTraceThetaOrdinal = e.Ordinal
		celtQuantBandTraceHasTheta = true
		celtQuantBandTraceLastTheta = quantBandTraceContext{
			band: e.Band, n: e.N, blocks: e.B, b0: e.B0, channels: e.Channels,
			lm: e.LM, encode: e.Encode, stereo: e.Stereo,
			thetaRound: e.ThetaRound, thetaOrdinal: e.Ordinal,
		}
	} else if !state.overrideThetaOrdinal {
		e.ThetaOrdinal = celtQuantBandTraceThetaOrdinal
	}
	e.RangeAfter, e.TellFracAfter = quantBandTraceCoderState(ctx)
	if ctx != nil {
		e.RemainingAfter = int32(ctx.remainingBits)
	}
	celtQuantBandTraceOrdinal++
	celtQuantBandTraceCount++
	snapshot := *e
	celtQuantBandTraceHook(&snapshot)
	return e.ThetaOrdinal
}

func beginQuantThetaTrace(ctx *bandCtx, x, y []celtNorm, n, b, B, B0, lm int, stereo bool, fill int) quantBandTraceState {
	state := beginQuantBandTrace(ctx, CELTQuantBandTraceTheta, n, B, B0, lm, stereo)
	if !state.active {
		return state
	}
	state.event.BBefore = int32(b)
	state.event.FillBefore = int32(fill)
	state.event.EnergyL = float32(ctx.bandEnergy(0))
	state.event.EnergyR = float32(ctx.bandEnergy(1))
	captureQuantBandTracePair(&state, x, y, n, true)
	return state
}

func finishQuantThetaTrace(state *quantBandTraceState, ctx *bandCtx, sctx *splitCtx, x, y []celtNorm, n, b, fill, qn, pulseCap, offset, rawIthetaQ30 int) {
	if state == nil || !state.active {
		return
	}
	e := &state.event
	e.BAfter = int32(b)
	e.FillAfter = int32(fill)
	e.QN = int32(qn)
	e.PulseCap = int32(pulseCap)
	e.Offset = int32(offset)
	e.RawIthetaQ30 = int32(rawIthetaQ30)
	if sctx != nil {
		e.Itheta = int32(sctx.itheta)
		e.IthetaQ30 = int32(sctx.ithetaQ30)
		e.Inv = int32(sctx.inv)
		e.IMid = int32(sctx.imid)
		e.ISide = int32(sctx.iside)
		e.Delta = int32(sctx.delta)
		e.QAlloc = int32(sctx.qalloc)
	}
	e.FillAfter = int32(fill)
	captureQuantBandTracePair(state, x, y, n, false)
	emitQuantBandTrace(state, ctx)
}

func beginQuantPVQTrace(ctx *bandCtx, x []celtNorm, n, k, spread, B, lm int, gain opusVal16, resynth bool) quantBandTraceState {
	state := beginQuantBandTrace(ctx, CELTQuantBandTracePVQ, n, B, B, lm, ctx != nil && ctx.channels == 2)
	if !state.active {
		return state
	}
	if celtQuantBandTraceHasTheta {
		// C alg_quant inherits stereo/thetaRound and thetaOrdinal from the
		// current compute_theta. Its quant_partition caller supplies leaf LM;
		// the leaf's local B0 equals the actual alg_quant B.
		applyQuantBandTraceContext(&state, celtQuantBandTraceLastTheta)
		state.overrideThetaOrdinal = false
		state.event.N = uint32(n)
		state.event.B = uint32(B)
		state.event.B0 = uint32(B)
		state.event.LM = int32(lm)
	}
	state.event.K = int32(k)
	state.event.Spread = int32(spread)
	state.event.Gain = float32(gain)
	if resynth {
		state.event.Resynth = 1
	}
	captureQuantBandTracePair(&state, x, nil, n, true)
	return state
}

func finishQuantPVQTrace(state *quantBandTraceState, ctx *bandCtx, x []celtNorm, n, collapse int) {
	if state == nil || !state.active {
		return
	}
	state.event.Collapse = uint32(collapse)
	captureQuantBandTracePair(state, x, nil, n, false)
	emitQuantBandTrace(state, ctx)
}

func beginQuantStereoMergeTrace(ctx *bandCtx, x, y []celtNorm, n, B, lm int, mid opusVal16, context quantBandTraceContext) quantBandTraceState {
	state := beginQuantBandTrace(ctx, CELTQuantBandTraceStereoMerge, n, B, B, lm, true)
	if !state.active {
		return state
	}
	applyQuantBandTraceContext(&state, context)
	state.event.Mid = float32(mid)
	captureQuantBandTracePair(&state, x, y, n, true)
	return state
}

func finishQuantStereoMergeTrace(state *quantBandTraceState, ctx *bandCtx, x, y []celtNorm, n int) {
	if state == nil || !state.active {
		return
	}
	captureQuantBandTracePair(state, x, y, n, false)
	emitQuantBandTrace(state, ctx)
}

func beginQuantBandOutputTrace(ctx *bandCtx, x, y []celtNorm, n, b, B, lm int) quantBandTraceState {
	state := beginQuantBandTrace(ctx, CELTQuantBandTraceBandOutput, n, B, B, lm, true)
	if !state.active {
		return state
	}
	state.event.BBefore = int32(b)
	captureQuantBandTracePair(&state, x, y, n, true)
	return state
}

func finishQuantBandOutputTrace(state *quantBandTraceState, ctx *bandCtx, x, y []celtNorm, n, collapse int) uint32 {
	if state == nil || !state.active {
		return ^uint32(0)
	}
	state.event.Collapse = uint32(collapse)
	captureQuantBandTracePair(state, x, y, n, false)
	thetaOrdinal := emitQuantBandTrace(state, ctx)
	if state.active {
		celtQuantBandTraceLastBandOut = quantBandTraceContext{
			band: state.event.Band, n: state.event.N, blocks: state.event.B, b0: state.event.B0,
			channels: state.event.Channels, lm: state.event.LM, encode: state.event.Encode, stereo: state.event.Stereo,
			thetaRound: state.event.ThetaRound, thetaOrdinal: thetaOrdinal,
		}
	}
	return thetaOrdinal
}

func setQuantBandOutputTraceContext(state *quantBandTraceState, context quantBandTraceContext) {
	applyQuantBandTraceContext(state, context)
}

func beginQuantRDOTrace(ctx *bandCtx, x, y []celtNorm, n, b, B, lm int) quantBandTraceState {
	state := beginQuantBandTrace(ctx, CELTQuantBandTraceRDOSelect, n, B, B, lm, true)
	if !state.active {
		return state
	}
	state.event.BBefore = int32(b)
	return state
}

func finishQuantRDOTrace(state *quantBandTraceState, ctx *bandCtx, x, y []celtNorm, n, selectedRound int, dist0, dist1 float32, context quantBandTraceContext) {
	if state == nil || !state.active {
		return
	}
	state.event.Dist0 = dist0
	state.event.Dist1 = dist1
	state.event.SelectedRound = int32(selectedRound)
	applyQuantBandTraceContext(state, context)
	captureQuantBandTracePair(state, x, y, n, false)
	emitQuantBandTrace(state, ctx)
}
