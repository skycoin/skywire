//go:build !gopus_qext

package gopus

type decoderHD96kFields struct{}

func (d *Decoder) is96kHz() bool { return false }

func (d *Decoder) decode96kFloat32(_ []byte, _ []float32) (int, error) {
	panic("decode96k called without gopus_qext build tag")
}

func (d *Decoder) decodeInt1696k(_ []byte, _ []int16) (int, error) {
	panic("decodeInt1696k called without gopus_qext build tag")
}

func (d *Decoder) decodeInt2496k(_ []byte, _ []int32) (int, error) {
	panic("decodeInt2496k called without gopus_qext build tag")
}

func init96kDecoder(_ *Decoder) {}
