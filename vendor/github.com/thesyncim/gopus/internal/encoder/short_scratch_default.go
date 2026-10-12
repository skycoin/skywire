//go:build !gopus_fixed_point

package encoder

func (e *Encoder) reserveFixedShortScratch(_, _ int) {}
