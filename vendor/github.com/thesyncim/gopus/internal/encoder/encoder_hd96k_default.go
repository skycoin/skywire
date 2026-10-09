//go:build !gopus_qext

package encoder

import "github.com/thesyncim/gopus/internal/celt"

func configureCELTEncoderForSampleRate(_ *celt.Encoder, _ int32) {}
