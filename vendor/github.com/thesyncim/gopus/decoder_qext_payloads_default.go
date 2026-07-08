//go:build !gopus_qext

package gopus

type decoderQEXTPayloads struct{}

func (p *decoderQEXTPayloads) frame(_ int) []byte {
	return nil
}

func (p *decoderQEXTPayloads) collect(_ []byte, _, _ int) {}
