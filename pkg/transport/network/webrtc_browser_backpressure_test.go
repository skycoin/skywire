//go:build js && wasm

// Package network pkg/transport/network/webrtc_browser_backpressure_test.go c2-net-transport
// covers the browser send queue bound. A DataChannel queues whatever it is
// handed, so Write has to wait above dcHighWater until the queue drains.
package network

import (
	"net"
	"syscall/js"
	"testing"
	"time"
)

// fakeDC is a DataChannel whose queue the test drives. bufferedAmount is the
// real channel's count and bufferedHigh is the exec worker's proxy of it.
const fakeDC = `(() => {
	let sent = 0;
	return {
		readyState: 'open',
		binaryType: '',
		bufferedAmount: 0,
		bufferedHigh: false,
		send: function (u8) { sent += u8.length; },
		sentBytes: function () { return sent; },
	};
})()`

func newFakeConn(t *testing.T) (*webRTCConn, js.Value) {
	t.Helper()
	dc := js.Global().Call("eval", fakeDC)
	return newWebRTCConn(dc, js.Undefined(), nil), dc
}

// TestWebRTCWriteWaitsForTheQueueToDrain is the regression the fix exists for.
// Above the high-water mark a Write must not hand more to the channel, and it
// must resume when the channel reports it drained.
func TestWebRTCWriteWaitsForTheQueueToDrain(t *testing.T) {
	c, dc := newFakeConn(t)

	dc.Set("bufferedAmount", dcHighWater+1)
	done := make(chan error, 1)
	go func() {
		_, err := c.Write([]byte("hello"))
		done <- err
	}()

	select {
	case err := <-done:
		t.Fatalf("Write returned %v while the queue was over the high-water mark", err)
	case <-time.After(300 * time.Millisecond):
	}
	if n := dc.Call("sentBytes").Int(); n != 0 {
		t.Fatalf("sent %d bytes into a full queue, want 0", n)
	}

	// Drain, then fire the handler the conn installed on the channel.
	dc.Set("bufferedAmount", 0)
	dc.Call("onbufferedamountlow")

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Write after the drain: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Write never resumed after the queue drained")
	}
	if n := dc.Call("sentBytes").Int(); n != 5 {
		t.Errorf("sent %d bytes, want 5", n)
	}
}

// TestWebRTCWriteWaitsOnTheWorkerProxy covers the exec worker, where only the
// page can read bufferedAmount so it reports the crossings as bufferedHigh.
func TestWebRTCWriteWaitsOnTheWorkerProxy(t *testing.T) {
	c, dc := newFakeConn(t)

	dc.Set("bufferedHigh", true)
	done := make(chan error, 1)
	go func() {
		_, err := c.Write([]byte("hi"))
		done <- err
	}()

	select {
	case err := <-done:
		t.Fatalf("Write returned %v while the worker reported the queue high", err)
	case <-time.After(300 * time.Millisecond):
	}

	dc.Set("bufferedHigh", false)
	dc.Call("onbufferedamountlow")

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Write after the proxy cleared: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Write never resumed after the worker reported the drain")
	}
}

// TestWebRTCWriteHonoursItsDeadline checks that a queue which never drains
// surfaces as a timeout rather than parking the writer for good.
func TestWebRTCWriteHonoursItsDeadline(t *testing.T) {
	c, dc := newFakeConn(t)
	dc.Set("bufferedAmount", dcHighWater+1)

	if err := c.SetWriteDeadline(time.Now().Add(150 * time.Millisecond)); err != nil {
		t.Fatalf("SetWriteDeadline: %v", err)
	}

	start := time.Now()
	_, err := c.Write([]byte("hello"))
	var ne net.Error
	if !asNetError(err, &ne) || !ne.Timeout() {
		t.Fatalf("want a net.Error that reports Timeout, got %v (%T)", err, err)
	}
	if waited := time.Since(start); waited > 5*time.Second {
		t.Errorf("Write waited %v, far past its deadline", waited)
	}
	if n := dc.Call("sentBytes").Int(); n != 0 {
		t.Errorf("sent %d bytes despite timing out, want 0", n)
	}
}

// TestWebRTCWriteUnderTheMarkDoesNotWait guards against the bound throttling a
// channel whose queue is small, which is the common case.
func TestWebRTCWriteUnderTheMarkDoesNotWait(t *testing.T) {
	c, dc := newFakeConn(t)
	dc.Set("bufferedAmount", dcLowWater)

	if _, err := c.Write([]byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n := dc.Call("sentBytes").Int(); n != 5 {
		t.Errorf("sent %d bytes, want 5", n)
	}
}

// TestWebRTCConnArmsTheLowWaterThreshold checks the conn tells the channel when
// to report a drain, without which a blocked Write would wait for its deadline.
func TestWebRTCConnArmsTheLowWaterThreshold(t *testing.T) {
	_, dc := newFakeConn(t)
	if got := dc.Get("bufferedAmountLowThreshold"); got.Type() != js.TypeNumber || got.Int() != dcLowWater {
		t.Errorf("bufferedAmountLowThreshold = %v, want %d", got, dcLowWater)
	}
	if dc.Get("onbufferedamountlow").Type() != js.TypeFunction {
		t.Error("the conn installed no onbufferedamountlow handler")
	}
}

// asNetError is errors.As for net.Error, kept local so the test reads plainly.
func asNetError(err error, target *net.Error) bool {
	if ne, ok := err.(net.Error); ok {
		*target = ne
		return true
	}
	return false
}
