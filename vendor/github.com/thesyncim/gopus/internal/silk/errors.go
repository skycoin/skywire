package silk

import "errors"

var (
	// ErrInvalidResampleRate indicates an invalid source rate for resampling.
	ErrInvalidResampleRate = errors.New("silk: invalid resample rate")

	// ErrMismatchedLengths indicates mismatched slice lengths in stereo unmixing.
	ErrMismatchedLengths = errors.New("silk: mismatched slice lengths")

	// ErrNoLBRRData indicates no LBRR (FEC) data is available in the packet.
	// LBRR data is only encoded when the encoder has FEC enabled and speech activity
	// exceeds the threshold.
	ErrNoLBRRData = errors.New("silk: no LBRR data available for FEC recovery")
)
