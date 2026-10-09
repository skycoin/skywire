//go:build !gopus_celt_trace || gopus_fixed_point || gopus_qext || gopus_dred || gopus_osce || gopus_custom_modes

package encoder

const analysisMeanStdTraceEnabled = false

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

func analysisMeanStdTraceSetFrame(int32)                             {}
func analysisMeanStdTraceSetChunk(int32)                             {}
func analysisMeanStdTraceBegin(int32, float32, []float32, []float32) {}
func analysisMeanStdTraceSetCMeanNew([]float32)                      {}
func analysisMeanStdTraceSetStdInput([]float32, []float32, []float32, []float32, []float32, []float32) {
}
func analysisMeanStdTraceSetStdNew([]float32) {}
func analysisMeanStdTraceFinish()             {}
