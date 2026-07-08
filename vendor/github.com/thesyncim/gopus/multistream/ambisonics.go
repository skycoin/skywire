// Ambisonics support for Opus multistream encoder (channel mapping families 2 and 3).
// This file implements ambisonics validation and channel mapping generation
// per RFC 7845 and the libopus reference implementation.
//
// Ambisonics is a full-sphere surround sound technique that encodes spatial audio
// as a set of spherical harmonic coefficients. The number of channels depends on
// the ambisonics order:
//   - Order 0 (W only): 1 channel
//   - Order 1 (FOA): 4 channels (W, Y, Z, X)
//   - Order 2 (SOA): 9 channels
//   - Order 3 (TOA): 16 channels
//   - Order N: (N+1)^2 channels
//
// Additionally, 2 "non-diegetic" stereo channels may be added for head-locked audio.
//
// Family 2: ACN/SN3D ordering, all mono streams except optional stereo non-diegetic pair
// Family 3: ACN/SN3D ordering, uses stereo coupled streams for channel pairs
//
// Reference: RFC 7845 Section 5.1.1.2, libopus opus_multistream_encoder.c

package multistream

import (
	"errors"

	"github.com/thesyncim/gopus/internal/opusmath"
)

// Errors for ambisonics validation.
var (
	// ErrInvalidAmbisonicsChannels indicates an invalid channel count for ambisonics.
	// Valid counts are (order+1)^2 or (order+1)^2 + 2 for orders 0-14.
	ErrInvalidAmbisonicsChannels = errors.New("multistream: invalid ambisonics channel count")

	// ErrAmbisonicsChannelsTooHigh indicates too many channels for ambisonics (max 227).
	ErrAmbisonicsChannelsTooHigh = errors.New("multistream: ambisonics channel count exceeds maximum (227)")

	// ErrInvalidMappingFamily indicates an invalid mapping family.
	ErrInvalidMappingFamily = errors.New("multistream: invalid mapping family for ambisonics (must be 2 or 3)")

	// ErrProjectionOrderUnsupported indicates the ambisonics order is outside the range
	// supported by mapping family 3 (projection). libopus 1.6.1 provides pre-computed
	// mixing and demixing matrices for orders 1..5 (orderPlusOne 2..6) only.
	//
	// Valid channel counts for projection (family 3):
	//   Order 1 (FOA):    4 or 6 channels
	//   Order 2 (SOA):    9 or 11 channels
	//   Order 3 (TOA):   16 or 18 channels
	//   Order 4 (4thOA): 25 or 27 channels
	//   Order 5 (5thOA): 36 or 38 channels
	//
	// Higher orders (6-14, channels 49-227) are valid ambisonics but are not supported
	// by projection encoding; use mapping family 2 for those orders.
	//
	// Reference: libopus src/mapping_matrix.c (mapping_matrix_*_mixing/_demixing tables),
	// src/opus_projection_encoder.c:opus_projection_ambisonics_encoder_get_size
	ErrProjectionOrderUnsupported = errors.New("multistream: ambisonics order not supported by projection (family 3); valid orders are 1-5")
)

func isqrt32(n int) int {
	if n <= 0 {
		return 0
	}
	return int(opusmath.ISqrt32(uint32(n)))
}

// ValidateAmbisonics validates that the channel count is valid for ambisonics
// encoding (mapping family 2) and returns the expected number of streams and
// coupled streams.
//
// Valid ambisonics channel counts are:
//   - (order+1)^2 for pure ambisonics (e.g., 1, 4, 9, 16, 25...)
//   - (order+1)^2 + 2 for ambisonics with non-diegetic stereo (e.g., 6, 11, 18, 27...)
//
// For family 2:
//   - streams = acn_channels + (nondiegetic > 0 ? 1 : 0)
//   - coupled = (nondiegetic > 0 ? 1 : 0)
//
// The maximum supported channel count is 227 (order 14 + 2 non-diegetic).
//
// Reference: libopus opus_multistream_encoder.c:validate_ambisonics
func ValidateAmbisonics(channels int) (streams, coupledStreams int, err error) {
	if channels < 1 {
		return 0, 0, ErrInvalidAmbisonicsChannels
	}
	if channels > 227 {
		return 0, 0, ErrAmbisonicsChannelsTooHigh
	}

	// Calculate order+1 from channel count
	orderPlusOne := isqrt32(channels)
	acnChannels := orderPlusOne * orderPlusOne
	nondiegeticChannels := channels - acnChannels

	// Non-diegetic channels must be 0 or 2
	if nondiegeticChannels != 0 && nondiegeticChannels != 2 {
		return 0, 0, ErrInvalidAmbisonicsChannels
	}

	// For family 2: mostly mono streams, with optional one stereo stream for non-diegetic
	streams = acnChannels
	coupledStreams = 0
	if nondiegeticChannels != 0 {
		streams++          // Add one stream for the non-diegetic pair
		coupledStreams = 1 // That stream is coupled (stereo)
	}

	return streams, coupledStreams, nil
}

// ValidateAmbisonicsFamily3 validates channel count for ambisonics family 3
// (projection-based ambisonics) and returns streams/coupled configuration.
//
// Family 3 uses maximum stereo coupling:
//   - streams = (channels + 1) / 2
//   - coupled = channels / 2
//
// libopus 1.6.1 projection encoding supports only orders 1..5 (orderPlusOne 2..6),
// with optional non-diegetic stereo channels.  Pre-computed mixing and demixing
// matrices exist for FOA/SOA/TOA/4thOA/5thOA only.  Higher orders (6-14) are
// valid ambisonics channel counts but are not supported by family 3; use
// mapping family 2 (ValidateAmbisonics) for orders 6-14.
//
// Valid channel counts:
//
//	Order 1 (FOA):    4 or 6 channels
//	Order 2 (SOA):    9 or 11 channels
//	Order 3 (TOA):   16 or 18 channels
//	Order 4 (4thOA): 25 or 27 channels
//	Order 5 (5thOA): 36 or 38 channels
//
// Reference: libopus src/opus_projection_encoder.c:get_streams_from_channels,
// src/mapping_matrix.c (mapping_matrix_foa…fifthoa mixing/demixing tables)
func ValidateAmbisonicsFamily3(channels int) (streams, coupledStreams int, err error) {
	if channels < 1 {
		return 0, 0, ErrInvalidAmbisonicsChannels
	}
	if channels > 227 {
		return 0, 0, ErrAmbisonicsChannelsTooHigh
	}

	// Validate this is a valid ambisonics channel count.
	orderPlusOne := isqrt32(channels)
	acnChannels := orderPlusOne * orderPlusOne
	nondiegeticChannels := channels - acnChannels

	if nondiegeticChannels != 0 && nondiegeticChannels != 2 {
		return 0, 0, ErrInvalidAmbisonicsChannels
	}

	// libopus projection supports only orderPlusOne in [2, 6] (orders 1..5).
	// Order 0 (1 or 3 channels) and orders 6-14 are rejected here even though
	// they are valid ambisonics channel counts.
	// Reference: libopus src/opus_projection_encoder.c:get_streams_from_channels,
	// src/mapping_matrix.c mapping_matrix_*_mixing/_demixing (foa..fifthoa only)
	if orderPlusOne < 2 || orderPlusOne > 6 {
		return 0, 0, ErrProjectionOrderUnsupported
	}

	// Family 3 (projection): maximum coupling. The projection encoder pairs the
	// post-mixing channels into stereo streams, with a trailing mono stream for
	// an odd channel count. The mixing/demixing matrix carries the inter-channel
	// energy.
	// Reference: libopus src/opus_projection_encoder.c get_streams_from_channels():
	//   streams = (channels + 1) / 2
	//   coupled_streams = channels / 2
	streams = (channels + 1) / 2
	coupledStreams = channels / 2

	return streams, coupledStreams, nil
}

// GetAmbisonicsOrder returns the ambisonics order and non-diegetic channel count
// from a given channel count.
//
// The order is derived from: channels = (order+1)^2 + nondiegetic
// where nondiegetic is 0 or 2.
//
// Returns:
//   - order: the ambisonics order (0 for mono, 1 for FOA, 2 for SOA, etc.)
//   - nondiegetic: number of non-diegetic channels (0 or 2)
//   - err: error if channel count is invalid
//
// Reference: libopus opus_projection_encoder.c:get_order_plus_one_from_channels
func GetAmbisonicsOrder(channels int) (order, nondiegetic int, err error) {
	if channels < 1 {
		return 0, 0, ErrInvalidAmbisonicsChannels
	}
	if channels > 227 {
		return 0, 0, ErrAmbisonicsChannelsTooHigh
	}

	orderPlusOne := isqrt32(channels)
	acnChannels := orderPlusOne * orderPlusOne
	nondiegeticChannels := channels - acnChannels

	if nondiegeticChannels != 0 && nondiegeticChannels != 2 {
		return 0, 0, ErrInvalidAmbisonicsChannels
	}

	return orderPlusOne - 1, nondiegeticChannels, nil
}

// AmbisonicsMapping generates the ACN-style channel mapping for ambisonics
// family 2 encoding.
//
// The mapping layout for family 2:
//   - First (streams - coupled_streams) entries map to mono streams
//   - Remaining entries map to coupled (stereo) streams
//
// For pure ambisonics (no non-diegetic):
//   - All streams are mono
//   - mapping[i] = i + 0 (since coupled_streams * 2 = 0)
//
// For ambisonics with non-diegetic:
//   - First (acn_channels) entries map to mono streams
//   - Last 2 entries map to the stereo non-diegetic stream
//
// Reference: libopus opus_multistream_encoder.c:opus_multistream_surround_encoder_init
func AmbisonicsMapping(channels int) ([]byte, error) {
	streams, coupledStreams, err := ValidateAmbisonics(channels)
	if err != nil {
		return nil, err
	}

	mapping := make([]byte, channels)

	// Per libopus:
	// for(i = 0; i < (*streams - *coupled_streams); i++)
	//    mapping[i] = i + (*coupled_streams * 2);
	// for(i = 0; i < *coupled_streams * 2; i++)
	//    mapping[i + (*streams - *coupled_streams)] = i;

	monoStreams := streams - coupledStreams
	coupledOffset := coupledStreams * 2

	// Map mono streams first
	for i := range monoStreams {
		mapping[i] = byte(i + coupledOffset)
	}

	// Map coupled streams after
	for i := 0; i < coupledStreams*2; i++ {
		mapping[monoStreams+i] = byte(i)
	}

	return mapping, nil
}

// AmbisonicsMappingFamily3 generates the channel mapping for ambisonics
// family 3 encoding. Family 3 uses projection-based encoding where channels
// are paired into stereo streams.
//
// The mapping is a simple sequential ordering where:
//   - Even channels (0, 2, 4...) are left channels of coupled streams
//   - Odd channels (1, 3, 5...) are right channels of coupled streams
//
// For odd channel counts, the last channel is in a mono stream.
func AmbisonicsMappingFamily3(channels int) ([]byte, error) {
	_, _, err := ValidateAmbisonicsFamily3(channels)
	if err != nil {
		return nil, err
	}

	mapping := make([]byte, channels)

	// Simple identity mapping for family 3
	for i := range channels {
		mapping[i] = byte(i)
	}

	return mapping, nil
}

// IsValidAmbisonicsChannelCount returns true if the channel count is valid
// for ambisonics encoding.
//
// Valid counts are (order+1)^2 or (order+1)^2 + 2 for orders 0-14:
// 1, 4, 6, 9, 11, 16, 18, 25, 27, 36, 38, 49, 51, 64, 66, 81, 83, 100, 102,
// 121, 123, 144, 146, 169, 171, 196, 198, 225, 227
func IsValidAmbisonicsChannelCount(channels int) bool {
	if channels < 1 || channels > 227 {
		return false
	}

	orderPlusOne := isqrt32(channels)
	acnChannels := orderPlusOne * orderPlusOne
	nondiegeticChannels := channels - acnChannels

	return nondiegeticChannels == 0 || nondiegeticChannels == 2
}

// AmbisonicsChannelCount returns the number of channels for a given
// ambisonics order with optional non-diegetic stereo pair.
//
// Parameters:
//   - order: ambisonics order (0-14)
//   - withNondiegetic: true to include 2 non-diegetic channels
//
// Returns the channel count, or 0 if order is out of range.
func AmbisonicsChannelCount(order int, withNondiegetic bool) int {
	if order < 0 || order > 14 {
		return 0
	}
	channels := (order + 1) * (order + 1)
	if withNondiegetic {
		channels += 2
	}
	return channels
}
