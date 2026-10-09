//go:build !gopus_fixed_point

package multistream

func (d *Decoder) decodeFixedProjectionFloat32(_ []byte, _ []float32, _ int) (int, bool, error) {
	return 0, false, nil
}

func (d *Decoder) decodeFixedProjectionInt16(_ []byte, _ int) ([]int16, bool, error) {
	return nil, false, nil
}

func (d *Decoder) decodeFixedProjectionInt24(_ []byte, _ int) ([]int32, bool, error) {
	return nil, false, nil
}

func (d *Decoder) decodeFixedOutputInt24(_ []byte, _ int) ([]int32, bool, error) {
	return nil, false, nil
}
