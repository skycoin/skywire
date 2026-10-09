//go:build !gopus_fixed_point

package gopus

// The fixed-point integer multistream output helpers are no-ops in the default
// build. These stubs keep the call sites identical across builds at zero cost.
func (d *MultistreamDecoder) fixedDecodePLCFloat32(_ []float32, _ int) (bool, error) {
	return false, nil
}

func (d *MultistreamDecoder) fixedDecodeInt16(_ []byte, _ []int16, _ int) (bool, error) {
	return false, nil
}

func (d *MultistreamDecoder) fixedDecodeInt24(_ []byte, _ []int32, _ int) (bool, error) {
	return false, nil
}
