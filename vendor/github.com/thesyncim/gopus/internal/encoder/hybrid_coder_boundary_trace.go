//go:build gopus_celt_trace && !gopus_fixed_point

package encoder

import "github.com/thesyncim/gopus/internal/rangecoding"

const (
	hybridCoderBoundarySILKExit uint32 = iota + 1
	hybridCoderBoundaryCELTEntry
	hybridCoderBoundaryCELTExit
	hybridCoderBoundaryCount
)

type hybridCoderBoundaryRecord struct {
	Frame uint32
	Stage uint32
	Coder *rangecoding.Encoder
	State rangecoding.BoundaryTraceSnapshot
}

type hybridCoderBoundaryTrace struct {
	targetFrame  uint32
	currentFrame uint32
	recordCount  uint32
	overflow     bool
	records      [hybridCoderBoundaryCount - 1]hybridCoderBoundaryRecord
}

var activeHybridCoderBoundaryTrace *hybridCoderBoundaryTrace

func recordHybridCoderBoundary(coder *rangecoding.Encoder, stage uint32) {
	trace := activeHybridCoderBoundaryTrace
	if trace == nil || trace.currentFrame != trace.targetFrame {
		return
	}
	if coder == nil || trace.recordCount >= uint32(len(trace.records)) {
		trace.overflow = true
		return
	}
	if stage != trace.recordCount+1 {
		trace.overflow = true
		return
	}
	record := &trace.records[trace.recordCount]
	record.Frame = trace.currentFrame
	record.Stage = stage
	record.Coder = coder
	if !coder.SnapshotBoundaryForTesting(&record.State) {
		trace.overflow = true
		return
	}
	trace.recordCount++
}

func beginHybridCoderBoundaryTraceForTesting(targetFrame uint32) *hybridCoderBoundaryTrace {
	trace := &hybridCoderBoundaryTrace{targetFrame: targetFrame}
	activeHybridCoderBoundaryTrace = trace
	return trace
}

func setHybridCoderBoundaryTraceFrameForTesting(frame uint32) {
	if activeHybridCoderBoundaryTrace != nil {
		activeHybridCoderBoundaryTrace.currentFrame = frame
	}
}

func endHybridCoderBoundaryTraceForTesting(trace *hybridCoderBoundaryTrace) {
	if activeHybridCoderBoundaryTrace == trace {
		activeHybridCoderBoundaryTrace = nil
	}
}
