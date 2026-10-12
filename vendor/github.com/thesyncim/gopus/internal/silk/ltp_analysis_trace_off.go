//go:build !gopus_silk_trace

package silk

const ltpAnalysisTraceEnabled = false

func ltpAnalysisTraceActive() bool { return false }

func recordSILKLTPAnalysisTrace(_ *Encoder, _ SILKLTPAnalysisTraceSnapshot) {}
