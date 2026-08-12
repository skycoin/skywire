//go:build js && wasm

package cosmos

import (
	"fmt"
	"syscall/js"
)

// fpsMonitor is a small built-in FPS overlay replacing the gl-bench
// dependency of the original library.
type fpsMonitor struct {
	el         js.Value
	frames     int
	lastUpdate float64
}

func newFPSMonitor(canvas js.Value) *fpsMonitor {
	document := js.Global().Get("document")
	el := document.Call("createElement", "div")
	style := el.Get("style")
	style.Call("setProperty", "position", "absolute")
	style.Call("setProperty", "top", "0")
	style.Call("setProperty", "right", "0")
	style.Call("setProperty", "padding", "4px 8px")
	style.Call("setProperty", "font", "12px monospace")
	style.Call("setProperty", "color", "#0f0")
	style.Call("setProperty", "background", "rgba(0,0,0,0.5)")
	style.Call("setProperty", "z-index", "10")
	style.Call("setProperty", "pointer-events", "none")
	el.Set("textContent", "-- fps")
	parent := canvas.Get("parentNode")
	if parent.Truthy() {
		parent.Call("appendChild", el)
	}
	return &fpsMonitor{el: el}
}

func (f *fpsMonitor) frame(now float64) {
	f.frames++
	if f.lastUpdate == 0 {
		f.lastUpdate = now
	}
	if now-f.lastUpdate >= 1000 {
		fps := float64(f.frames) * 1000 / (now - f.lastUpdate)
		f.el.Set("textContent", fmt.Sprintf("%.0f fps", fps))
		f.frames = 0
		f.lastUpdate = now
	}
}

func (f *fpsMonitor) destroy() {
	if f.el.Truthy() {
		f.el.Call("remove")
	}
}
