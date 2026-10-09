//go:build gopus_celt_trace && !gopus_fixed_point

package celt

import "github.com/thesyncim/gopus/internal/rangecoding"

const tfEncodeTraceMaxBits = 64

// TFEncodeBitTrace records one actual range-coded TF decision.
type TFEncodeBitTrace struct {
	Ordinal        int32
	Symbol         int32
	LogP           uint32
	SelectBit      bool
	RangeBefore    uint32
	TellFracBefore int32
	TellBefore     int32
	RangeAfter     uint32
	TellFracAfter  int32
	TellAfter      int32
}

// TFEncodeTraceSnapshot captures the selected call to TFEncodeWithSelect.
// TFResBefore contains the raw flags passed to the encoder; TFResAfter contains
// the mapped flags the CELT allocator receives after tf_select is applied.
type TFEncodeTraceSnapshot struct {
	Coder             *rangecoding.Encoder
	Start             int32
	End               int32
	LM                int32
	EntryTell         int32
	StorageBits       uint32
	Transient         bool
	TFSelectInput     int32
	TFSelectReserved  bool
	TFSelectEncoded   bool
	TFSelectValue     int32
	TFSelectEffective int32
	TFResBefore       []int32
	TFResBudgeted     []int32
	TFResAfter        []int32
	Bits              []TFEncodeBitTrace
	CallCount         uint32
	ForeignCalls      uint32
	Overflow          bool
	Complete          bool
}

var tfEncodeTraceForTesting struct {
	enabled bool
	coder   *rangecoding.Encoder
	trace   TFEncodeTraceSnapshot
}

// EnableTFEncodeTraceForTesting arms one actual range encoder. It is available
// only in CELT diagnostic builds.
func EnableTFEncodeTraceForTesting() {
	tfEncodeTraceForTesting.enabled = true
	tfEncodeTraceForTesting.coder = nil
	tfEncodeTraceForTesting.trace = TFEncodeTraceSnapshot{
		Bits: make([]TFEncodeBitTrace, 0, 32),
	}
}

// DisableTFEncodeTraceForTesting stops recording without changing the snapshot.
func DisableTFEncodeTraceForTesting() {
	tfEncodeTraceForTesting.enabled = false
}

// TFEncodeTraceForTesting returns the bounded actual-call snapshot.
func TFEncodeTraceForTesting() TFEncodeTraceSnapshot {
	return tfEncodeTraceForTesting.trace
}

func beginTFEncodeTrace(re *rangecoding.Encoder, start, end int, transient bool, tfRes []int32, lm, tfSelect int) int {
	s := &tfEncodeTraceForTesting
	if !s.enabled {
		return -1
	}
	if s.coder == nil {
		s.coder = re
		s.trace.Coder = re
	} else if re != s.coder {
		s.trace.ForeignCalls++
		s.trace.Overflow = true
		return -1
	}
	s.trace.CallCount++
	if s.trace.CallCount != 1 || start < 0 || end < start || end > len(tfRes) || lm < 0 || lm > 3 {
		s.trace.Overflow = true
		return -1
	}
	s.trace.Start = int32(start)
	s.trace.End = int32(end)
	s.trace.LM = int32(lm)
	s.trace.EntryTell = int32(re.Tell())
	s.trace.StorageBits = uint32(re.StorageBits())
	s.trace.Transient = transient
	s.trace.TFSelectInput = int32(tfSelect)
	s.trace.TFResBefore = append(s.trace.TFResBefore[:0], tfRes[start:end]...)
	firstLogP := 4
	if transient {
		firstLogP = 2
	}
	s.trace.TFSelectReserved = lm > 0 && re.Tell()+firstLogP+1 <= re.StorageBits()
	return 0
}

func tfEncodeTraceBit(trace int, re *rangecoding.Encoder, symbol int, logp uint, selectBit bool) {
	if trace < 0 {
		re.EncodeBit(symbol, logp)
		return
	}
	s := &tfEncodeTraceForTesting
	if !s.enabled || re != s.coder || len(s.trace.Bits) >= tfEncodeTraceMaxBits {
		s.trace.Overflow = true
		re.EncodeBit(symbol, logp)
		return
	}
	bit := TFEncodeBitTrace{
		Ordinal:        int32(len(s.trace.Bits)),
		Symbol:         int32(symbol),
		LogP:           uint32(logp),
		SelectBit:      selectBit,
		RangeBefore:    re.Range(),
		TellFracBefore: int32(re.TellFrac()),
		TellBefore:     int32(re.Tell()),
	}
	re.EncodeBit(symbol, logp)
	bit.RangeAfter = re.Range()
	bit.TellFracAfter = int32(re.TellFrac())
	bit.TellAfter = int32(re.Tell())
	s.trace.Bits = append(s.trace.Bits, bit)
	if selectBit {
		if s.trace.TFSelectEncoded {
			s.trace.Overflow = true
		}
		s.trace.TFSelectEncoded = true
		s.trace.TFSelectValue = int32(symbol)
	}
}

func recordTFEncodeBudgeted(trace int, re *rangecoding.Encoder, tfRes []int32) {
	if trace < 0 {
		return
	}
	s := &tfEncodeTraceForTesting
	if !s.enabled || re != s.coder || int(s.trace.End) > len(tfRes) {
		s.trace.Overflow = true
		return
	}
	s.trace.TFResBudgeted = append(s.trace.TFResBudgeted[:0], tfRes[s.trace.Start:s.trace.End]...)
}

func finishTFEncodeTrace(trace int, re *rangecoding.Encoder, tfRes []int32, effectiveSelect int) {
	if trace < 0 {
		return
	}
	s := &tfEncodeTraceForTesting
	if !s.enabled || re != s.coder || int(s.trace.End) > len(tfRes) {
		s.trace.Overflow = true
		return
	}
	s.trace.TFSelectEffective = int32(effectiveSelect)
	s.trace.TFResAfter = append(s.trace.TFResAfter[:0], tfRes[s.trace.Start:s.trace.End]...)
	s.trace.Complete = true
}
