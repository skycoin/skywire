//go:build gopus_celt_trace && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package encoder

const analysisSpecVariabilityTraceEnabled = true

type analysisSpecVariabilityTraceSnapshot struct {
	LogE       [NbFrames][NbTBands]float32
	MinDist    [NbFrames]float32
	Sum        float32
	Normalized float32
	Result     float32
}

var analysisSpecVariabilityTraceHook func(analysisSpecVariabilityTraceSnapshot)
