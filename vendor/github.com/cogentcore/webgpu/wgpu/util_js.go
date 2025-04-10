//go:build js

package wgpu

import (
	"log/slog"
	"syscall/js"
	"unsafe"
)

// BytesToJS converts the given bytes to a js Uint8ClampedArray
// by using the global wasm memory bytes. This avoids the
// copying present in [js.CopyBytesToJS].
func BytesToJS(b []byte) js.Value {
	ptr := uintptr(unsafe.Pointer(&b[0]))
	// We directly pass the offset and length to the constructor to avoid calling subarray or slice,
	// thereby improving performance and safety (this fixes a detached array buffer crash).
	return js.Global().Get("Uint8ClampedArray").New(js.Global().Get("wasm").Get("instance").Get("exports").Get("mem").Get("buffer"), ptr, len(b))
}

// mapSlice can be used to transform one slice into another by providing a
// function to do the mapping.
func mapSlice[S, T any](slice []S, fn func(S) T) []T {
	if slice == nil {
		return nil
	}
	result := make([]T, len(slice))
	for i, v := range slice {
		result[i] = fn(v)
	}
	return result
}

// await is a helper function roughly equivalent to await in JS.
func await(promise js.Value) js.Value {
	result := make(chan js.Value)
	promise.Call("then", js.FuncOf(func(this js.Value, args []js.Value) any {
		result <- args[0]
		return nil
	}), js.FuncOf(func(this js.Value, args []js.Value) any {
		slog.Error("wgpu.await: promise rejected", "reason", args[0])
		result <- js.Null()
		return nil
	}))
	return <-result
}

// no-ops
func SetLogLevel(level LogLevel) {}
func GetVersion() Version        { return 0 }
