package dred

import "github.com/thesyncim/gopus/internal/rangecoding"

// Parsed is the low-cost libopus-shaped DRED parse result retained before any
// model-backed processing stage.
type Parsed struct {
	Header         Header
	PayloadLatents int
}

// ParsePayload decodes the lightweight DRED metadata from a payload body with
// the temporary experimental prefix already stripped.
func ParsePayload(payload []byte, dredFrameOffset int) (Parsed, error) {
	var rd rangecoding.Decoder
	header, err := parseHeaderWithDecoder(payload, dredFrameOffset, &rd)
	if err != nil {
		return Parsed{}, err
	}
	return Parsed{
		Header:         header,
		PayloadLatents: payloadLatents(payload, header, &rd),
	}, nil
}

// FillQuantizerLevels writes the request-bounded libopus quantizer schedule
// into dst and returns the number of entries written.
func (p Parsed) FillQuantizerLevels(dst []int32, maxDredSamples, sampleRate int) int {
	return p.ForRequest(Request{
		MaxDREDSamples: maxDredSamples,
		SampleRate:     sampleRate,
	}).FillQuantizerLevels(dst)
}
