// Package network pkg/transport/network/quic_windows_skip_test.go
package network

import (
	"runtime"
	"testing"
)

// skipQUICOnWindows skips quic-go-based tests on Windows. quic-go drives UDP
// through Go's overlapped-I/O poller (and wraps the conn for the demux, so it
// can't even set the receive-buffer size), which faults with an access
// violation (0xc0000005, in internal/poll.(*FD).execIO / quic-go's sendQueue)
// when a socket is torn down with I/O in flight. It's a Go 1.26.x windows/amd64
// runtime defect, not a transport bug, and it's fatal to the whole package test
// binary — so guard every test that stands up a real QUIC/WebTransport endpoint.
// We stay on Go >= 1.26 (other deps require it), so skip rather than downgrade;
// drop this once a fixed Go 1.26.x lands.
func skipQUICOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("quic-go over UDP is unstable under Go's Windows netpoll; see overlapped-I/O crash in CI")
	}
}
