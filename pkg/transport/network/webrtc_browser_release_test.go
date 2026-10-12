//go:build js && wasm

// Package network pkg/transport/network/webrtc_browser_release_test.go c2-net-transport
// covers what a browser WebRTC conn lets go of. An unreleased js.Func or an
// open signaling stream kept every conn, and its dmsg stream, in memory.
package network

import (
	"syscall/js"
	"testing"
)

const fakeClosableDC = `(() => ({
	readyState: 'open',
	binaryType: '',
	bufferedAmount: 0,
	close: function () {},
	send: function () {},
}))()`

const fakePC = `(() => ({ close: function () {} }))()`

type closeCounter struct{ n int }

func (c *closeCounter) Close() error { c.n++; return nil }

func TestWebRTCConnReleasesSignalingAndHandlers(t *testing.T) {
	dc := js.Global().Call("eval", fakeClosableDC)
	pc := js.Global().Call("eval", fakePC)
	sig := &closeCounter{}
	c := newWebRTCConn(dc, pc, sig)

	events := []string{"onopen", "onmessage", "onclose", "onerror", "onbufferedamountlow"}
	for _, ev := range events {
		if dc.Get(ev).Type() != js.TypeFunction {
			t.Fatalf("%s not installed", ev)
		}
	}

	c.releaseSignaling()
	if sig.n != 1 {
		t.Fatalf("signaling closed %d times after the handoff, want 1", sig.n)
	}

	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if sig.n != 1 {
		t.Fatalf("Close closed the released signaling stream again (%d closes)", sig.n)
	}
	for _, ev := range events {
		if !dc.Get(ev).IsNull() {
			t.Fatalf("%s still attached after Close", ev)
		}
	}
	if c.handlers != nil {
		t.Fatal("handlers kept after Close")
	}
}
