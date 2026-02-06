//go:build js && wasm

package ui

import (
	"syscall/js"
)

// InputHandler manages mouse and keyboard input
type InputHandler struct {
	canvas js.Value

	// Mouse state
	MouseX, MouseY   float64
	MouseDown        bool
	MouseJustPressed bool
	MouseJustUp      bool
	WheelDelta       float64

	// Keyboard state
	KeysPressed map[string]bool
	KeysJustPressed map[string]bool

	// Callbacks stored for cleanup
	callbacks []js.Func
}

// NewInputHandler creates a new input handler attached to a canvas element
func NewInputHandler(canvasID string) *InputHandler {
	doc := js.Global().Get("document")
	canvas := doc.Call("getElementById", canvasID)

	h := &InputHandler{
		canvas:          canvas,
		KeysPressed:     make(map[string]bool),
		KeysJustPressed: make(map[string]bool),
	}

	h.setup()
	return h
}

func (h *InputHandler) setup() {
	// Mouse move
	moveHandler := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		e := args[0]
		rect := h.canvas.Call("getBoundingClientRect")
		h.MouseX = e.Get("clientX").Float() - rect.Get("left").Float()
		h.MouseY = e.Get("clientY").Float() - rect.Get("top").Float()
		return nil
	})
	h.canvas.Call("addEventListener", "mousemove", moveHandler)
	h.callbacks = append(h.callbacks, moveHandler)

	// Mouse down
	downHandler := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		e := args[0]
		if e.Get("button").Int() == 0 { // Left button
			h.MouseDown = true
			h.MouseJustPressed = true
		}
		return nil
	})
	h.canvas.Call("addEventListener", "mousedown", downHandler)
	h.callbacks = append(h.callbacks, downHandler)

	// Mouse up
	upHandler := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		e := args[0]
		if e.Get("button").Int() == 0 {
			h.MouseDown = false
			h.MouseJustUp = true
		}
		return nil
	})
	h.canvas.Call("addEventListener", "mouseup", upHandler)
	h.callbacks = append(h.callbacks, upHandler)

	// Mouse wheel
	wheelHandler := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		e := args[0]
		e.Call("preventDefault")
		h.WheelDelta = -e.Get("deltaY").Float() / 100.0
		return nil
	})
	h.canvas.Call("addEventListener", "wheel", wheelHandler)
	h.callbacks = append(h.callbacks, wheelHandler)

	// Keyboard - attach to document for global capture
	doc := js.Global().Get("document")

	keyDownHandler := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		e := args[0]
		key := e.Get("key").String()
		if !h.KeysPressed[key] {
			h.KeysJustPressed[key] = true
		}
		h.KeysPressed[key] = true
		return nil
	})
	doc.Call("addEventListener", "keydown", keyDownHandler)
	h.callbacks = append(h.callbacks, keyDownHandler)

	keyUpHandler := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		e := args[0]
		key := e.Get("key").String()
		h.KeysPressed[key] = false
		return nil
	})
	doc.Call("addEventListener", "keyup", keyUpHandler)
	h.callbacks = append(h.callbacks, keyUpHandler)
}

// Update should be called at the end of each frame to reset per-frame state
func (h *InputHandler) Update() {
	h.MouseJustPressed = false
	h.MouseJustUp = false
	h.WheelDelta = 0
	for k := range h.KeysJustPressed {
		delete(h.KeysJustPressed, k)
	}
}

// IsKeyJustPressed returns true if the key was just pressed this frame
func (h *InputHandler) IsKeyJustPressed(key string) bool {
	return h.KeysJustPressed[key]
}

// Cleanup releases all event listeners
func (h *InputHandler) Cleanup() {
	for _, cb := range h.callbacks {
		cb.Release()
	}
}
