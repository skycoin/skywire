package dred

// Request mirrors the lightweight opus_dred_parse() request parameters that
// affect how much cached redundancy is usable.
type Request struct {
	MaxDREDSamples int
	SampleRate     int
}

// Result bundles parsed DRED metadata with the request-bounded coverage
// derived from it.
type Result struct {
	Request      Request
	Parsed       Parsed
	Availability Availability
}

// ForRequest evaluates a parsed DRED payload against an
// opus_dred_parse()-style request.
func (p Parsed) ForRequest(req Request) Result {
	return Result{
		Request:      req,
		Parsed:       p,
		Availability: p.Availability(req.MaxDREDSamples, req.SampleRate),
	}
}

// FillQuantizerLevels writes the request-bounded libopus quantizer schedule
// into dst and returns the number of entries written.
func (r Result) FillQuantizerLevels(dst []int32) int {
	n := min(r.Availability.MaxLatents, len(dst))
	for i := 0; i < n; i++ {
		dst[i] = r.Parsed.Header.QuantizerLevel(i)
	}
	return n
}

// MaxAvailableSamples mirrors opus_dred_parse()'s positive sample-count return
// for the request-bounded result.
func (r Result) MaxAvailableSamples() int {
	return r.Availability.AvailableSamples
}
