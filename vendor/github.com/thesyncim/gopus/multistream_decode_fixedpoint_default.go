//go:build !gopus_fixed_point

package gopus

// fixedDecodeInt16 / fixedDecodeInt24 are no-ops in the default build: the
// FIXED_POINT integer multistream decode is gated behind gopus_fixed_point, so
// the public int16/int24 wrappers always use the float conversion. These stubs
// keep the call sites identical across builds at zero cost (they are inlined to
// a constant-false return).
func (d *MultistreamDecoder) fixedDecodeInt16(_ []byte, _ []int16, _ int) (bool, error) {
	return false, nil
}

func (d *MultistreamDecoder) fixedDecodeInt24(_ []byte, _ []int32, _ int) (bool, error) {
	return false, nil
}
