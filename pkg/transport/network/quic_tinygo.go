//go:build tinygo

// Package network pkg/transport/network/quic_tinygo.go
//
// QUIC carrier stub for TinyGo. quic-go (and pkg/skyquic) don't compile under
// TinyGo, and a browser has no raw UDP anyway, so the QUIC transport is
// unavailable on this target. MakeClient routes the QUIC case here; the real
// constructor lives in quic.go (//go:build !tinygo). This keeps quic-go out of
// the TinyGo transport graph while leaving dmsg (and the future webrtc) intact.
package network

import "errors"

func makeQuicClient(_ *resolvedClient, _ int) (Client, error) {
	return nil, errors.New("network: QUIC transport not supported in this build (TinyGo)")
}
