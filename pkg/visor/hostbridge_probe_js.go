//go:build js

// Package visor pkg/visor/hostbridge_probe_js.go c3-vis-wasm
package visor

import (
	"context"
	"sync"
	"syscall/js"
	"time"
)

// hostBridgeProbeWait outlasts the page's own 60 s probe timeout, so a user
// answering the prompt is waited for.
const hostBridgeProbeWait = 65 * time.Second

var hostBridgeProbed sync.Map

// probeLocalAccess asks the page to open url before the visor dials it, until
// one probe has opened. The visor runs in a worker, which cannot raise the
// browser's local network access prompt, and the page can (exec-worker.js
// __skywireLocalProbe).
func probeLocalAccess(ctx context.Context, url string) {
	probe := js.Global().Get("__skywireLocalProbe")
	if probe.Type() != js.TypeFunction {
		return
	}
	if _, done := hostBridgeProbed.Load(url); done {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, hostBridgeProbeWait)
	defer cancel()
	opened := make(chan bool, 1)
	cb := js.FuncOf(func(_ js.Value, args []js.Value) any {
		ok := len(args) > 0 && args[0].Truthy()
		select {
		case opened <- ok:
		default:
		}
		return nil
	})
	probe.Invoke(url).Call("then", cb)
	select {
	case ok := <-opened:
		cb.Release()
		if ok {
			hostBridgeProbed.Store(url, true)
		}
	case <-ctx.Done():
		// The promise may still settle and call cb, so it is not released.
	}
}
