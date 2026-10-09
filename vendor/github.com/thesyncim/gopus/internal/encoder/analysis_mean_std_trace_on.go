//go:build gopus_celt_trace && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package encoder

const analysisMeanStdTraceEnabled = true

type analysisMeanStdTraceSnapshot struct {
	Frame     int32
	Chunk     int32
	Count     int32
	Alpha     float32
	StdActive bool
	CMeanOld  [4]float32
	BFCC      [4]float32
	Mem0      [4]float32
	Mem8      [4]float32
	Mem16     [4]float32
	Mem24     [4]float32
	Feature   [11]float32
	CMeanNew  [4]float32
	StdOld    [9]float32
	StdNew    [9]float32
}

var analysisMeanStdTraceHook func(analysisMeanStdTraceSnapshot)

var analysisMeanStdTraceMaxCount int32 = 5

var analysisMeanStdTraceFrame int32
var analysisMeanStdTraceChunk int32
var analysisMeanStdTraceCurrent analysisMeanStdTraceSnapshot

func analysisMeanStdTraceSetFrame(frame int32) {
	analysisMeanStdTraceFrame = frame
}

func analysisMeanStdTraceSetChunk(chunk int32) {
	analysisMeanStdTraceChunk = chunk
}

func analysisMeanStdTraceBegin(count int32, alpha float32, cmeanOld, bfcc []float32) {
	analysisMeanStdTraceCurrent = analysisMeanStdTraceSnapshot{
		Frame: analysisMeanStdTraceFrame,
		Chunk: analysisMeanStdTraceChunk,
		Count: count,
		Alpha: alpha,
	}
	copy(analysisMeanStdTraceCurrent.CMeanOld[:], cmeanOld)
	copy(analysisMeanStdTraceCurrent.BFCC[:], bfcc)
}

func analysisMeanStdTraceSetCMeanNew(cmeanNew []float32) {
	copy(analysisMeanStdTraceCurrent.CMeanNew[:], cmeanNew)
}

func analysisMeanStdTraceSetStdInput(mem0, mem8, mem16, mem24, feature, stdOld []float32) {
	analysisMeanStdTraceCurrent.StdActive = true
	copy(analysisMeanStdTraceCurrent.Mem0[:], mem0)
	copy(analysisMeanStdTraceCurrent.Mem8[:], mem8)
	copy(analysisMeanStdTraceCurrent.Mem16[:], mem16)
	copy(analysisMeanStdTraceCurrent.Mem24[:], mem24)
	copy(analysisMeanStdTraceCurrent.Feature[:], feature)
	copy(analysisMeanStdTraceCurrent.StdOld[:], stdOld)
}

func analysisMeanStdTraceSetStdNew(stdNew []float32) {
	copy(analysisMeanStdTraceCurrent.StdNew[:], stdNew)
}

func analysisMeanStdTraceFinish() {
	analysisMeanStdTraceHook(analysisMeanStdTraceCurrent)
}
