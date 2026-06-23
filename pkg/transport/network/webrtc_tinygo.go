//go:build tinygo && !(js && wasm)

// Package network pkg/transport/network/webrtc_tinygo.go
//
// WebRTC carrier stub for non-browser TinyGo targets (wasip1, bare-metal): there
// is no RTCPeerConnection and pion doesn't compile here, so dial and accept fail
// closed. The browser carrier lives in webrtc_browser.go; Start + the dmsg
// signaling listener are shared (untagged) in webrtc.go and harmlessly produce a
// listener whose answerer attempts error out on these targets.
package network

import (
	"context"
	"errors"
	"io"
	"net"
)

var errWebRTCUnsupported = errors.New("webrtc: not supported on this TinyGo target")

func webrtcDial(_ context.Context, _ io.ReadWriteCloser, _ []string) (net.Conn, error) {
	return nil, errWebRTCUnsupported
}

func webrtcAccept(_ context.Context, _ io.ReadWriteCloser, _ []string) (net.Conn, error) {
	return nil, errWebRTCUnsupported
}
