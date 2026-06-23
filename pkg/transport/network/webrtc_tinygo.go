//go:build tinygo

// Package network pkg/transport/network/webrtc_tinygo.go
//
// WebRTC carrier stub for TinyGo. pion/webrtc doesn't compile under TinyGo, and
// the browser carrier (syscall/js RTCPeerConnection, reusing the proven
// cmd/dmsg-wasm webrtc_js.go adapter) is wired in a follow-up. Until then both
// dial and accept fail closed on TinyGo builds.
package network

import (
	"context"
	"errors"
	"io"
	"net"
)

var errWebRTCUnsupported = errors.New("webrtc: not yet supported on this TinyGo build")

func webrtcDial(_ context.Context, _ io.ReadWriteCloser, _ []string) (net.Conn, error) {
	return nil, errWebRTCUnsupported
}

// Start implements Client: WebRTC accept is unavailable under TinyGo for now.
func (c *webrtcClient) Start() error { return errWebRTCUnsupported }
