// Channel-mapping-table helpers: the default (Vorbis/family 1) layouts and the
// routines that interpret a mapping byte into a (stream, channel-in-stream) pair.
//
// Reference: RFC 7845 Section 5.1.1 (Channel Mapping)

package multistream

import "errors"

// ErrInvalidDefaultMappingChannels indicates a channel count outside the default mapping range.
var ErrInvalidDefaultMappingChannels = errors.New("multistream: invalid default mapping channel count (must be 1-8)")

// DefaultMapping returns the default Vorbis-style (mapping family 1) configuration
// for a given channel count.
//
// Returns:
//   - streams: total number of elementary Opus streams (N)
//   - coupledStreams: number of coupled (stereo) streams (M), where first M streams are stereo
//   - mapping: channel mapping table where mapping[i] indicates the source for output channel i
//   - err: error if channel count is invalid for the default mapping (must be 1-8)
//
// The mapping table encodes which decoded channel feeds each output channel:
//   - Values 0 to 2*M-1: from coupled streams (even=left, odd=right of stereo pair)
//   - Values 2*M to N+M-1: from uncoupled (mono) streams
//   - Value 255: silent channel
//
// Reference: RFC 7845 Section 5.1.1
func DefaultMapping(channels int) (streams, coupledStreams int, mapping []byte, err error) {
	switch channels {
	case 1:
		// Mono: 1 uncoupled stream
		return 1, 0, []byte{0}, nil
	case 2:
		// Stereo: 1 coupled stream
		return 1, 1, []byte{0, 1}, nil
	case 3:
		// 3.0: 1 coupled (L/R) + 1 uncoupled (C)
		// Output order: L, C, R
		// mapping[0]=0 -> coupled stream 0, left
		// mapping[1]=2 -> uncoupled stream 1, mono (index 2 = 2*1 + 0)
		// mapping[2]=1 -> coupled stream 0, right
		return 2, 1, []byte{0, 2, 1}, nil
	case 4:
		// Quad: 2 coupled streams
		// Output order: FL, FR, RL, RR
		return 2, 2, []byte{0, 1, 2, 3}, nil
	case 5:
		// 5.0: 2 coupled + 1 uncoupled (C)
		// Output order: FL, C, FR, RL, RR
		// mapping[0]=0 -> coupled 0, left (FL)
		// mapping[1]=4 -> uncoupled stream 2, mono (index 4 = 2*2 + 0)
		// mapping[2]=1 -> coupled 0, right (FR)
		// mapping[3]=2 -> coupled 1, left (RL)
		// mapping[4]=3 -> coupled 1, right (RR)
		return 3, 2, []byte{0, 4, 1, 2, 3}, nil
	case 6:
		// 5.1 surround: 2 coupled + 2 uncoupled (C, LFE)
		// Output order: FL, C, FR, RL, RR, LFE
		// Streams: 0=FL/FR (coupled), 1=RL/RR (coupled), 2=C (mono), 3=LFE (mono)
		return 4, 2, []byte{0, 4, 1, 2, 3, 5}, nil
	case 7:
		// 6.1 surround: 3 coupled + 1 uncoupled (LFE)
		// Output order: FL, C, FR, SL, SR, RC, LFE
		// Streams: 0=FL/FR, 1=SL/SR, 2=C/RC (coupled), 3=LFE (mono)
		return 4, 3, []byte{0, 4, 1, 2, 3, 5, 6}, nil
	case 8:
		// 7.1 surround: 3 coupled + 2 uncoupled (C, LFE)
		// Output order: FL, C, FR, SL, SR, RL, RR, LFE
		// Streams: 0=FL/FR, 1=SL/SR, 2=RL/RR (coupled), 3=C, 4=LFE (mono)
		return 5, 3, []byte{0, 6, 1, 2, 3, 4, 5, 7}, nil
	default:
		return 0, 0, nil, ErrInvalidDefaultMappingChannels
	}
}

// streamChannels returns the number of channels decoded by a given stream.
// Coupled streams (index < coupledStreams) produce 2 channels (stereo).
// Uncoupled streams produce 1 channel (mono).
func streamChannels(streamIdx, coupledStreams int) int {
	if streamIdx < coupledStreams {
		return 2 // Coupled stream = stereo
	}
	return 1 // Uncoupled stream = mono
}

// resolveMapping interprets a mapping table entry to determine which stream
// and channel within that stream provides the audio for an output channel.
//
// Parameters:
//   - mappingIdx: the value from the mapping table (mapping[outputChannel])
//   - coupledStreams: number of coupled (stereo) streams (M)
//
// Returns:
//   - streamIdx: index of the source stream (0 to N-1)
//   - chanInStream: channel within the stream (0=mono/left, 1=right for stereo)
//
// Special case: If mappingIdx == 255, returns (-1, -1) indicating a silent channel.
//
// Mapping interpretation:
//   - 0 to 2*M-1: coupled streams (even = left, odd = right)
//   - 2*M to N+M-1: uncoupled (mono) streams
//   - 255: silent channel
func resolveMapping(mappingIdx byte, coupledStreams int) (streamIdx, chanInStream int) {
	idx := int(mappingIdx)

	// Silent channel
	if idx == 255 {
		return -1, -1
	}

	// Coupled streams: indices 0 to 2*M-1
	if idx < 2*coupledStreams {
		streamIdx = idx / 2
		chanInStream = idx % 2 // 0 = left, 1 = right
		return streamIdx, chanInStream
	}

	// Uncoupled streams: indices 2*M to N+M-1
	// Stream index = coupledStreams + (mappingIdx - 2*coupledStreams)
	streamIdx = coupledStreams + (idx - 2*coupledStreams)
	chanInStream = 0 // Mono stream has only one channel
	return streamIdx, chanInStream
}
