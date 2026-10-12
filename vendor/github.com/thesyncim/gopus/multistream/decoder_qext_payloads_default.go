//go:build !gopus_qext

package multistream

import "github.com/thesyncim/gopus/internal/celt"

type streamQEXTPayloads struct{}

func configureStreamNative96kCELT(_ *celt.Decoder) {}

func (p *streamQEXTPayloads) frame(_ int) []byte {
	return nil
}

func (p *streamQEXTPayloads) collect(_ []byte, _, _ int) {}
