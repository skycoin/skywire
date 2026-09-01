//go:build js && wasm

// Package cmdutil pkg/cmdutil/signal_js.go c0-com-util
//
// js/wasm: the browser has no POSIX signals; os.Interrupt is the only
// os.Signal value the runtime defines, and nothing ever delivers it. Kept so
// SignalContext compiles and behaves as an ordinary cancelable context.
package cmdutil

import "os"

func listenSignals() []os.Signal {
	return []os.Signal{os.Interrupt}
}
