//go:build !gopus_fixed_point

package multistream

func (d *Decoder) decodeFixedOutputFloat32(_ []byte, _ []float32, _ int) (int, bool, error) {
	return 0, false, nil
}

func (d *Decoder) decodeFixedOutputInt16(_ []byte, _ int) ([]int16, bool, error) {
	return nil, false, nil
}
