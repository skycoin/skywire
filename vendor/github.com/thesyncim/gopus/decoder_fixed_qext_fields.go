//go:build gopus_fixed_point && gopus_qext

package gopus

import (
	"github.com/thesyncim/gopus/internal/fixedpoint"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

type decoderFixedQEXTFields struct {
	decoder             *fixedpoint.QEXTCELTDecoder
	redundantDec        rangecoding.Decoder
	res                 []int32
	invalid             bool
	hybridActive        bool
	hybridPayload       []byte
	hybridChannels      int
	redundantRange      uint32
	redundantRangeValid bool
}
