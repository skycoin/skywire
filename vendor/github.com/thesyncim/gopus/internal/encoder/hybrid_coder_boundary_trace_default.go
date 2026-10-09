//go:build !gopus_celt_trace || gopus_fixed_point

package encoder

import "github.com/thesyncim/gopus/internal/rangecoding"

const (
	hybridCoderBoundarySILKExit uint32 = iota + 1
	hybridCoderBoundaryCELTEntry
	hybridCoderBoundaryCELTExit
)

func recordHybridCoderBoundary(_ *rangecoding.Encoder, _ uint32) {}
