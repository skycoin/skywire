//go:build !tinygo && !(js && wasm)

// Package network pkg/transport/network/webrtc_capability_native.go c2-net-transport
package network

// WebRTCAvailable reports whether this runtime can construct a peer connection.
// Native builds carry pion, which always can.
func WebRTCAvailable() bool { return true }
