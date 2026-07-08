package dred

// FeatureWindow is the payload-bounded DRED feature index window used by
// libopus's opus_decode_native() concealment path before any model
// processing. RecoverableFeatureFrames reflects the parsed payload latents
// retained in Result.Availability.
type FeatureWindow struct {
	FeaturesPerFrame         int
	NeededFeatureFrames      int
	FeatureOffsetBase        int
	MaxFeatureIndex          int
	RecoverableFeatureFrames int
	MissingPositiveFrames    int
}

// FeatureWindow mirrors the feature_offset indexing math in opus_decode_native()
// for a given decoded DRED result and concealment request.
func (r Result) FeatureWindow(decodeOffsetSamples, frameSizeSamples, initFrames int) FeatureWindow {
	if r.Request.SampleRate <= 0 || frameSizeSamples <= 0 {
		return FeatureWindow{}
	}
	f10 := r.Request.SampleRate / 100
	if f10 <= 0 {
		return FeatureWindow{}
	}
	if initFrames < 0 {
		initFrames = 0
	}
	featuresPerFrame := max(frameSizeSamples/f10, 1)
	neededFeatureFrames := initFrames + featuresPerFrame
	featureOffsetBase := initFrames - 2 + floorDiv(decodeOffsetSamples+r.Availability.OffsetSamples, f10)
	maxFeatureIndex := 4*r.Availability.MaxLatents - 1

	recoverable := 0
	missingPositive := 0
	for i := range neededFeatureFrames {
		featureOffset := featureOffsetBase - i
		if featureOffset < 0 {
			continue
		}
		if featureOffset <= maxFeatureIndex {
			recoverable++
		} else {
			missingPositive++
		}
	}

	return FeatureWindow{
		FeaturesPerFrame:         featuresPerFrame,
		NeededFeatureFrames:      neededFeatureFrames,
		FeatureOffsetBase:        featureOffsetBase,
		MaxFeatureIndex:          maxFeatureIndex,
		RecoverableFeatureFrames: recoverable,
		MissingPositiveFrames:    missingPositive,
	}
}

// FillFeatureOffsets writes the feature offsets libopus would probe, from
// newest to oldest, into dst and returns the number of entries written.
func (w FeatureWindow) FillFeatureOffsets(dst []int32) int {
	n := min(w.NeededFeatureFrames, len(dst))
	for i := 0; i < n; i++ {
		dst[i] = int32(w.FeatureOffsetBase - i)
	}
	return n
}

func floorDiv(num, den int) int {
	if den <= 0 {
		return 0
	}
	q := num / den
	r := num % den
	if r != 0 && num < 0 {
		q--
	}
	return q
}

func floorDiv32(num, den int32) int32 {
	if den <= 0 {
		return 0
	}
	q := num / den
	r := num % den
	if r != 0 && num < 0 {
		q--
	}
	return q
}
