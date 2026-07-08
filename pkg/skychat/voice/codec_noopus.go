//go:build !opus

// Package voice pkg/skychat/voice/codec_noopus.go c2-app-chat
//
// Default (no `opus` build tag): the Opus codec is unavailable, so voice uses
// the PCM passthrough. Build with `-tags opus` (and libopus present) to enable
// Opus — see codec_opus.go.
package voice

import "errors"

// ErrOpusUnavailable is returned by NewOpusCodec when the binary was not built
// with the `opus` tag.
var ErrOpusUnavailable = errors.New("voice: opus codec not built (rebuild with -tags opus and libopus installed)")

// NewOpusCodec is unavailable without the `opus` build tag.
func NewOpusCodec() (Codec, error) { return nil, ErrOpusUnavailable }
