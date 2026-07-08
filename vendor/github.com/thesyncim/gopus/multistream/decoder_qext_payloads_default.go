//go:build !gopus_qext

package multistream

type streamQEXTPayloads struct{}

func (p *streamQEXTPayloads) frame(_ int) []byte {
	return nil
}

func (p *streamQEXTPayloads) collect(_ []byte, _, _ int) {}
