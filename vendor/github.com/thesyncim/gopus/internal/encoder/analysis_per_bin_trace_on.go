//go:build gopus_celt_trace && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package encoder

const analysisPerBinTraceEnabled = true

type analysisPerBinTraceSnapshot struct {
	Bin       int32
	AvgMod    float32
	Tonality  float32
	Tonality2 float32
	Noisiness float32
}

var analysisPerBinTraceHook func(analysisPerBinTraceSnapshot)
