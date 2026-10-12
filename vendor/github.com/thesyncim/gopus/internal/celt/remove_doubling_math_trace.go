//go:build gopus_celt_trace && gopus_remove_doubling_trace && !gopus_fixed_point

package celt

import (
	"sync"
	"sync/atomic"
)

const removeDoublingMathTraceCaptureEnabled = true

const (
	removeDoublingTraceMaxYY   = 512
	removeDoublingTraceMaxDual = 16
	removeDoublingTraceMaxGain = 16
)

var removeDoublingMathTraceState struct {
	mu      sync.Mutex
	active  atomic.Bool
	trace   EncodeRemoveDoublingMathTrace
	yyLimit int
}

func beginRemoveDoublingMathTrace(buffer []float32, maxPeriod, n, t0 int) bool {
	removeDoublingMathTraceState.mu.Lock()
	defer removeDoublingMathTraceState.mu.Unlock()
	if removeDoublingMathTraceState.active.Load() || len(buffer) == 0 || maxPeriod <= 0 || n <= 0 || t0 < 0 {
		return false
	}
	maxHalf, nHalf, t0Half := maxPeriod>>1, n>>1, t0>>1
	if maxHalf <= 0 || maxHalf > removeDoublingTraceMaxYY || nHalf <= 0 || maxHalf+nHalf > len(buffer) {
		return false
	}
	if t0Half >= maxHalf {
		t0Half = maxHalf - 1
	}
	yyLimit := min(t0Half+(2*t0Half+2)/4, maxHalf)
	if yyLimit <= 0 || yyLimit > removeDoublingTraceMaxYY {
		return false
	}
	removeDoublingMathTraceState.trace = EncodeRemoveDoublingMathTrace{}
	removeDoublingMathTraceState.yyLimit = yyLimit
	removeDoublingMathTraceState.active.Store(true)
	return true
}

func finishRemoveDoublingMathTrace() (EncodeRemoveDoublingMathTrace, bool) {
	removeDoublingMathTraceState.mu.Lock()
	defer removeDoublingMathTraceState.mu.Unlock()
	removeDoublingMathTraceState.active.Store(false)
	trace := removeDoublingMathTraceState.trace
	complete := !trace.Overflow &&
		int(trace.YYCount) == removeDoublingMathTraceState.yyLimit &&
		trace.DualCount > 0 && trace.DualCount == trace.GainCount
	removeDoublingMathTraceState.yyLimit = 0
	return trace, complete
}

func recordRemoveDoublingDual(first, second float32) {
	if !removeDoublingMathTraceState.active.Load() {
		return
	}
	removeDoublingMathTraceState.mu.Lock()
	defer removeDoublingMathTraceState.mu.Unlock()
	if !removeDoublingMathTraceState.active.Load() {
		return
	}
	trace := &removeDoublingMathTraceState.trace
	if trace.DualCount >= removeDoublingTraceMaxDual {
		trace.Overflow = true
		return
	}
	index := int(trace.DualCount)
	trace.DualFirst[index] = first
	trace.DualSecond[index] = second
	trace.DualCount++
}

func recordRemoveDoublingYY(index int, xBefore, xAfter, updated, lookup float32) {
	if !removeDoublingMathTraceState.active.Load() {
		return
	}
	removeDoublingMathTraceState.mu.Lock()
	defer removeDoublingMathTraceState.mu.Unlock()
	if !removeDoublingMathTraceState.active.Load() {
		return
	}
	trace := &removeDoublingMathTraceState.trace
	if trace.YYCount >= removeDoublingTraceMaxYY || index != int(trace.YYCount)+1 || index > removeDoublingMathTraceState.yyLimit {
		trace.Overflow = true
		return
	}
	trace.YY[trace.YYCount] = EncodeRemoveDoublingYYTrace{
		Index: int32(index), XBefore: xBefore, XAfter: xAfter, UpdatedYY: updated, LookupYY: lookup,
	}
	trace.YYCount++
}

func recordRemoveDoublingGain(xy, xx, yy, denominator, root, gain float32) {
	if !removeDoublingMathTraceState.active.Load() {
		return
	}
	removeDoublingMathTraceState.mu.Lock()
	defer removeDoublingMathTraceState.mu.Unlock()
	if !removeDoublingMathTraceState.active.Load() {
		return
	}
	trace := &removeDoublingMathTraceState.trace
	if trace.GainCount >= removeDoublingTraceMaxGain {
		trace.Overflow = true
		return
	}
	trace.Gains[trace.GainCount] = EncodeRemoveDoublingGainTrace{
		XY: xy, XX: xx, YY: yy, Denominator: denominator, Sqrt: root, Gain: gain,
	}
	trace.GainCount++
}
