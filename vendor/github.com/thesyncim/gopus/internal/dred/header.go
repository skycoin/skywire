package dred

import (
	"errors"

	"github.com/thesyncim/gopus/internal/rangecoding"
)

var errInvalidHeader = errors.New("dred: invalid experimental header")

// Header mirrors the low-cost metadata libopus decodes before the model-heavy
// DRED processing stage.
type Header struct {
	Q0              int32
	DQ              int32
	QMax            int32
	ExtraOffset     int32
	DredOffset      int32
	DredFrameOffset int32
}

var dqTable = [...]int32{0, 2, 3, 4, 6, 8, 12, 16}

// OffsetSamples converts the parsed DRED offset from 2.5 ms units to samples
// at the caller's sample rate.
func (h Header) OffsetSamples(sampleRate int) int {
	return int(h.DredOffset) * sampleRate / 400
}

// EndSamples mirrors opus_dred_parse()'s dred_end output: the number of
// trailing silence samples between the DRED timestamp and the last usable DRED
// sample.
func (h Header) EndSamples(sampleRate int) int {
	offset := h.OffsetSamples(sampleRate)
	if offset < 0 {
		return -offset
	}
	return 0
}

// ParseHeader decodes the lightweight libopus DRED header from a payload body
// with the temporary extension prefix already stripped. dredFrameOffset is in
// 2.5 ms units, matching libopus dred_find_payload().
func ParseHeader(payload []byte, dredFrameOffset int) (Header, error) {
	var rd rangecoding.Decoder
	return parseHeaderWithDecoder(payload, dredFrameOffset, &rd)
}

// QuantizerLevel mirrors libopus compute_quantizer() for the parsed DRED
// quantizer metadata.
func (h Header) QuantizerLevel(i int) int32 {
	if i < 0 {
		i = 0
	}
	dq := max(h.DQ, 0)
	if dq >= int32(len(dqTable)) {
		dq = int32(len(dqTable) - 1)
	}
	quant := h.Q0 + (dqTable[dq]*int32(i)+8)/16
	if quant > h.QMax {
		return h.QMax
	}
	return quant
}
