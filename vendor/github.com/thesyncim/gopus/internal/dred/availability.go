package dred

// Availability summarizes the low-cost request-bounded DRED coverage that
// libopus exposes through opus_dred_parse().
type Availability struct {
	FeatureFrames    int
	MaxLatents       int
	OffsetSamples    int
	EndSamples       int
	AvailableSamples int
}

// Availability reports the request-bounded DRED coverage derived from the
// parsed header and the opus_dred_parse() request parameters.
func (h Header) Availability(maxDredSamples, sampleRate int) Availability {
	offsetSamples := h.OffsetSamples(sampleRate)
	endSamples := h.EndSamples(sampleRate)
	featureFrames := RequestedFeatureFrames(maxDredSamples, sampleRate)
	maxLatents := MaxLatentsForRequest(maxDredSamples, sampleRate)
	availableSamples := max(maxLatents*LatentSpanSamples(sampleRate)-offsetSamples, 0)
	return Availability{
		FeatureFrames:    featureFrames,
		MaxLatents:       maxLatents,
		OffsetSamples:    offsetSamples,
		EndSamples:       endSamples,
		AvailableSamples: availableSamples,
	}
}

// Availability reports the request-bounded DRED coverage for a fully parsed
// payload. It refines Header.Availability by clamping MaxLatents (and the
// derived AvailableSamples) to the number of latents actually present in the
// payload.
func (p Parsed) Availability(maxDredSamples, sampleRate int) Availability {
	avail := p.Header.Availability(maxDredSamples, sampleRate)
	if avail.MaxLatents > p.PayloadLatents {
		avail.MaxLatents = p.PayloadLatents
		avail.AvailableSamples = max(avail.MaxLatents*LatentSpanSamples(sampleRate)-avail.OffsetSamples, 0)
	}
	return avail
}
