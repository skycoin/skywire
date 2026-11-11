//go:build js

// Package visor pkg/visor/netonline_js.go c3-vis-wasm
package visor

import (
	"context"
	"syscall/js"
)

// watchBrowserOnline follows the browser's online and offline events. The
// visor runs in a worker or a page, and both raise them on the global scope
// (https://html.spec.whatwg.org/multipage/system-state.html#dom-navigator-online).
func watchBrowserOnline(ctx context.Context, v *Visor) {
	global := js.Global()
	if global.Get("addEventListener").Type() != js.TypeFunction {
		return
	}
	if nav := global.Get("navigator"); nav.Type() == js.TypeObject && nav.Get("onLine").Type() == js.TypeBoolean && !nav.Get("onLine").Bool() {
		v.reportNetwork(false, "browser", "")
	}
	online := js.FuncOf(func(js.Value, []js.Value) any {
		go v.reportNetwork(true, "browser", "browser online")
		return nil
	})
	offline := js.FuncOf(func(js.Value, []js.Value) any {
		go v.reportNetwork(false, "browser", "")
		return nil
	})
	global.Call("addEventListener", "online", online)
	global.Call("addEventListener", "offline", offline)
	go func() {
		<-ctx.Done()
		global.Call("removeEventListener", "online", online)
		global.Call("removeEventListener", "offline", offline)
		online.Release()
		offline.Release()
	}()
}
