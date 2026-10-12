//go:build !gopus_celt_trace || gopus_fixed_point || gopus_qext || gopus_dred || gopus_osce || gopus_custom_modes

package encoder

const analysisMLPTraceEnabled = false

type analysisMLPTraceSnapshot struct {
	Frame          int32
	Dense0Calls    int32
	GRUCalls       int32
	Dense2Calls    int32
	Dense0Input    [25]float32
	Dense0Output   [32]float32
	GRUInput       [32]float32
	GRUStateBefore [24]float32
	GRUStateAfter  [24]float32
	Dense2Input    [24]float32
	Dense2Output   [2]float32
}

var analysisMLPTraceHook func(analysisMLPTraceSnapshot)
