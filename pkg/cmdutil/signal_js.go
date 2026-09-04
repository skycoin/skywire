//go:build js && wasm

// Package cmdutil pkg/cmdutil/signal_js.go c0-com-util
//
// js/wasm: the browser has no POSIX signals; os.Interrupt is the only
// os.Signal value the runtime defines, and nothing ever delivers it. Kept so
// SignalContext compiles and behaves as an ordinary cancelable context.
package cmdutil

import (
	"os"
	"runtime"
	"syscall/js"
)

func listenSignals() []os.Signal {
	return []os.Signal{os.Interrupt}
}

// notifyPlatformInterrupt registers a JS-callable interrupt for this
// instance: globalThis.__skywireSignals[SKYWIRE_EXEC_ID] — invoked by the
// page (the shell's Ctrl+C handler via skywire-exec.js) to deliver
// os.Interrupt exactly as a terminal would. Instances launched without an
// id (a bare harness) register nothing.
func notifyPlatformInterrupt(ch chan<- os.Signal) {
	id := os.Getenv("SKYWIRE_EXEC_ID")
	if id == "" {
		return
	}
	g := js.Global()
	reg := g.Get("__skywireSignals")
	if !reg.Truthy() {
		reg = g.Get("Object").New()
		g.Set("__skywireSignals", reg)
	}
	reg.Set(id, js.FuncOf(func(js.Value, []js.Value) any {
		select {
		case ch <- os.Interrupt:
		default:
		}
		return nil
	}))

	// SIGQUIT parity: globalThis.__skywireDump[SKYWIRE_EXEC_ID]() returns the
	// full goroutine dump as a string — the diagnostic a native process gives
	// on SIGQUIT, which the browser otherwise has no way to ask for. A wedged
	// or CPU-spinning instance (a scheduler pegged by a zero-delay timer loop
	// shows only runtime frames in a CDP CPU profile) names the looping
	// goroutine here, from DevTools or the serve harness's js-eval bridge.
	dumps := g.Get("__skywireDump")
	if !dumps.Truthy() {
		dumps = g.Get("Object").New()
		g.Set("__skywireDump", dumps)
	}
	dumps.Set(id, js.FuncOf(func(js.Value, []js.Value) any {
		buf := make([]byte, 1<<22)
		n := runtime.Stack(buf, true)
		for n == len(buf) && len(buf) < 1<<26 {
			buf = make([]byte, len(buf)*2)
			n = runtime.Stack(buf, true)
		}
		return string(buf[:n])
	}))
}
