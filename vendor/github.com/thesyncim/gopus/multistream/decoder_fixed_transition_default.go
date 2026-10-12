//go:build !gopus_fixed_point

package multistream

func (d *streamState) captureFixedCELTTransition(_ []float32, _, _ int, _ bool) {}
func (d *streamState) captureFixedSILKTransition(_ []float32, _ int, _ bool)    {}
